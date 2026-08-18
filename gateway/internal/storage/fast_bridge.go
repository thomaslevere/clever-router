package storage

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/klauspost/compress/zstd"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// FastVolumeBridge provides high-throughput memory-to-S3 streaming
// with parallel Zstandard compression for container state persistence.
type FastVolumeBridge struct {
	client   *minio.Client
	bucket   string
	keyLocks sync.Map // map[string]*sync.Mutex
}

// NewFastVolumeBridge initializes MinIO S3 client with region and credentials.
func NewFastVolumeBridge(endpoint, accessKey, secretKey, bucket, region string, useSSL bool) (*FastVolumeBridge, error) {
	if endpoint == "" || accessKey == "" || secretKey == "" {
		return nil, fmt.Errorf("missing S3 credentials: endpoint, accessKey, and secretKey are required")
	}

	if region == "" {
		region = "us-east-1"
	}

	minioClient, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
		Region: region,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize MinIO client: %w", err)
	}

	// Verify or create bucket if it does not exist
	ctx := context.Background()
	exists, err := minioClient.BucketExists(ctx, bucket)
	if err == nil && !exists {
		if makeErr := minioClient.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: region}); makeErr != nil {
			log.Printf("[fast-bridge] warning: could not make bucket %q in region %q: %v", bucket, region, makeErr)
		} else {
			log.Printf("[fast-bridge] created S3 bucket %q (region: %s)", bucket, region)
		}
	} else if err != nil {
		log.Printf("[fast-bridge] warning: bucket check for %q: %v", bucket, err)
	}

	return &FastVolumeBridge{
		client: minioClient,
		bucket: bucket,
	}, nil
}

func (b *FastVolumeBridge) getLock(key string) *sync.Mutex {
	v, _ := b.keyLocks.LoadOrStore(key, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// GetRawObject returns a direct S3 object stream from Cellar.
func (b *FastVolumeBridge) GetRawObject(ctx context.Context, key string) (*minio.Object, error) {
	if b.client == nil {
		return nil, fmt.Errorf("minio client not initialized")
	}
	return b.client.GetObject(ctx, b.bucket, key, minio.GetObjectOptions{})
}

// openArchiveStream returns an io.ReadCloser that transparently decompresses Zstandard, Gzip,
// or passes through uncompressed tar streams based on magic header bytes.
func openArchiveStream(body io.Reader) (io.ReadCloser, error) {
	buf := make([]byte, 4)
	n, err := io.ReadFull(body, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	combined := io.MultiReader(bytes.NewReader(buf[:n]), body)

	// Check Zstandard magic: 0x28, 0xB5, 0x2F, 0xFD
	if n >= 4 && buf[0] == 0x28 && buf[1] == 0xb5 && buf[2] == 0x2f && buf[3] == 0xfd {
		zr, zerr := zstd.NewReader(combined)
		if zerr != nil {
			return nil, zerr
		}
		return zr.IOReadCloser(), nil
	}

	// Check Gzip magic: 0x1F, 0x8B
	if n >= 2 && buf[0] == 0x1f && buf[1] == 0x8b {
		gr, gerr := gzip.NewReader(combined)
		if gerr != nil {
			return nil, gerr
		}
		return gr, nil
	}

	// Uncompressed tar stream
	return io.NopCloser(combined), nil
}

// AutoRestoreFromS3 scans Cellar S3 for all snapshots under namespaces/ and pre-restores them to local storage.
func (b *FastVolumeBridge) AutoRestoreFromS3(ctx context.Context, localDataDir string) error {
	if b.client == nil {
		return nil
	}
	log.Printf("[boot] Starting pre-flight S3 auto-restore from bucket %q...", b.bucket)

	if err := os.MkdirAll(localDataDir, 0777); err != nil {
		return fmt.Errorf("failed to create base local data directory: %w", err)
	}

	opts := minio.ListObjectsOptions{
		Prefix:    "namespaces/",
		Recursive: true,
	}

	restoredCount := 0
	for obj := range b.client.ListObjects(ctx, b.bucket, opts) {
		if obj.Err != nil {
			log.Printf("[boot] warning: list S3 object error: %v", obj.Err)
			continue
		}
		if obj.Size <= 32 {
			continue
		}

		key := obj.Key
		parts := strings.Split(key, "/")
		if len(parts) < 3 {
			continue
		}
		namespace := parts[1]
		volFile := parts[2]
		sanitized := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(volFile, ".tar.zst"), ".tar.gz"), ".tar")
		if sanitized == "" {
			sanitized = "data"
		}

		targetDir := filepath.Join(localDataDir, namespace, sanitized)
		log.Printf("[boot] Found snapshot in S3: %s (size: %d bytes), extracting to %s...", key, obj.Size, targetDir)

		if err := b.HydrateFromS3(ctx, key, targetDir); err != nil {
			log.Printf("[boot] warning: failed to restore %s: %v", key, err)
			continue
		}
		restoredCount++
	}

	log.Printf("[boot] S3 auto-restore complete. Restored %d volume snapshot(s).", restoredCount)
	return nil
}

// HydrateFromS3 downloads and extracts an archive (.tar.zst, .tar.gz, .tar) from S3 into targetDir.
// If the archive does not exist yet (fresh boot), it returns nil.
func (b *FastVolumeBridge) HydrateFromS3(ctx context.Context, s3Key, targetDir string) error {
	mu := b.getLock(s3Key)
	mu.Lock()
	defer mu.Unlock()

	obj, err := b.client.GetObject(ctx, b.bucket, s3Key, minio.GetObjectOptions{})
	if err != nil {
		return err
	}
	defer obj.Close()

	stat, err := obj.Stat()
	if err != nil {
		// Key not found or empty - this is expected on first boot
		return nil
	}
	if stat.Size <= 32 {
		return nil
	}

	if err := os.MkdirAll(targetDir, 0777); err != nil {
		return fmt.Errorf("create target dir %s: %w", targetDir, err)
	}
	_ = os.Chmod(targetDir, 0777)

	decompStream, err := openArchiveStream(obj)
	if err != nil {
		return fmt.Errorf("open archive stream for %s: %w", s3Key, err)
	}
	defer decompStream.Close()

	tr := tar.NewReader(decompStream)
	count := 0
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar reader error: %w", err)
		}

		// Security: prevent Zip-Slip / path traversal vulnerability
		cleaned := filepath.Clean(header.Name)
		if strings.HasPrefix(cleaned, "..") || filepath.IsAbs(cleaned) {
			continue
		}

		// Skip stale lockfiles and sockets that crash SQLite / Node / PM2 on boot
		baseName := filepath.Base(cleaned)
		if strings.HasSuffix(baseName, ".pid") ||
			strings.HasSuffix(baseName, ".lock") ||
			strings.HasSuffix(baseName, ".sock") ||
			strings.HasSuffix(baseName, ".sock.lock") ||
			baseName == "pm2.pid" ||
			baseName == "server.pid" ||
			strings.HasSuffix(baseName, ".db-shm") ||
			strings.HasSuffix(baseName, ".db-wal") {
			continue
		}

		targetPath := filepath.Join(targetDir, cleaned)
		if header.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(targetPath, 0777); err != nil {
				return err
			}
			_ = os.Chmod(targetPath, 0777)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0777); err != nil {
			return err
		}

		// Enforce open write permissions (0666) regardless of original tar mode
		f, err := os.OpenFile(targetPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0666)
		if err != nil {
			return fmt.Errorf("open file %s: %w", targetPath, err)
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return fmt.Errorf("write file %s: %w", targetPath, err)
		}
		f.Close()
		_ = os.Chmod(targetPath, 0666)
		count++
	}

	_ = filepath.Walk(targetDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			_ = os.Chmod(path, 0777)
		} else {
			_ = os.Chmod(path, 0666)
		}
		return nil
	})
	log.Printf("[fast-bridge] hydrated %d files from s3://%s/%s -> %s", count, b.bucket, s3Key, targetDir)
	return nil
}

// StreamSnapshotToS3 compresses a local directory and uploads it directly to S3.
func (b *FastVolumeBridge) StreamSnapshotToS3(ctx context.Context, sourceDir, s3Key string) error {
	mu := b.getLock(s3Key)
	mu.Lock()
	defer mu.Unlock()

	if _, err := os.Stat(sourceDir); os.IsNotExist(err) {
		return nil
	}

	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(0))
	if err != nil {
		return fmt.Errorf("create zstd writer: %w", err)
	}
	tw := tar.NewWriter(zw)

	fileCount := 0
	err = filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// Skip temporary unreadable/removed files gracefully
			return nil
		}
		if info.IsDir() {
			return nil
		}
		// Skip socket or special device files
		if !info.Mode().IsRegular() {
			return nil
		}

		relPath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}

		header, err := tar.FileInfoHeader(info, info.Name())
		if err != nil {
			return err
		}
		header.Name = relPath
		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		data, err := os.ReadFile(path)
		if err != nil {
			// File might have been rotated or removed during walk; continue gracefully
			return nil
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
		fileCount++
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk sourceDir %s: %w", sourceDir, err)
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("close tar writer: %w", err)
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("close zstd writer: %w", err)
	}

	payload := buf.Bytes()
	if len(payload) == 0 || fileCount == 0 {
		return nil
	}

	_, err = b.client.PutObject(ctx, b.bucket, s3Key, bytes.NewReader(payload), int64(len(payload)), minio.PutObjectOptions{
		ContentType: "application/zstd",
	})
	if err != nil {
		return fmt.Errorf("put S3 object %s: %w", s3Key, err)
	}

	log.Printf("[fast-bridge] streamed snapshot (%d files, %d bytes) -> s3://%s/%s", fileCount, len(payload), b.bucket, s3Key)
	return nil
}

// HydrateContainer downloads a snapshot from S3, decompresses it, normalizes all tar UID/GIDs to 0 (root),
// and copies it directly into the target container directory via Docker API.
func (b *FastVolumeBridge) HydrateContainer(ctx context.Context, cli *client.Client, containerID, s3Key, targetParentDir string) error {
	return b.HydrateContainerCandidateKeys(ctx, cli, containerID, []string{s3Key}, targetParentDir)
}

// HydrateContainerCandidateKeys tries each candidate s3Key in order and extracts the first valid snapshot into the container.
func (b *FastVolumeBridge) HydrateContainerCandidateKeys(ctx context.Context, cli *client.Client, containerID string, candidateKeys []string, targetParentDir string) error {
	if b.client == nil || cli == nil {
		return nil
	}

	for _, s3Key := range candidateKeys {
		mu := b.getLock(s3Key)
		mu.Lock()

		obj, err := b.client.GetObject(ctx, b.bucket, s3Key, minio.GetObjectOptions{})
		if err != nil {
			mu.Unlock()
			continue
		}

		stat, err := obj.Stat()
		if err != nil || stat.Size <= 32 {
			obj.Close()
			mu.Unlock()
			continue
		}

		log.Printf("[fast-bridge] Found valid snapshot %s (size: %d bytes) in S3. Hydrating into container %s:%s...", s3Key, stat.Size, containerID[:12], targetParentDir)

		decompStream, err := openArchiveStream(obj)
		if err != nil {
			obj.Close()
			mu.Unlock()
			log.Printf("[fast-bridge] decompress stream error for %s: %v", s3Key, err)
			continue
		}

		pr, pw := io.Pipe()
		go func(key string, stream io.ReadCloser, o *minio.Object, lock *sync.Mutex) {
			defer o.Close()
			defer stream.Close()
			defer lock.Unlock()

			tr := tar.NewReader(stream)
			tw := tar.NewWriter(pw)
			defer func() {
				_ = tw.Close()
				_ = pw.Close()
			}()

			for {
				header, err := tr.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					_ = pw.CloseWithError(err)
					return
				}

				// Security: prevent Zip-Slip / path traversal vulnerability
				cleaned := filepath.Clean(header.Name)
				if strings.HasPrefix(cleaned, "..") || filepath.IsAbs(cleaned) {
					continue
				}

				// Skip stale lockfiles and sockets that crash SQLite / Node / PM2 on boot
				baseName := filepath.Base(cleaned)
				if strings.HasSuffix(baseName, ".pid") ||
					strings.HasSuffix(baseName, ".lock") ||
					strings.HasSuffix(baseName, ".sock") ||
					strings.HasSuffix(baseName, ".sock.lock") ||
					baseName == "pm2.pid" ||
					baseName == "server.pid" ||
					strings.HasSuffix(baseName, ".db-shm") ||
					strings.HasSuffix(baseName, ".db-wal") {
					continue
				}

				// Normalize UID and GID to 0 (root) so Docker userns remap never rejects the archive
				header.Uid = 0
				header.Gid = 0
				header.Uname = ""
				header.Gname = ""
				if header.Typeflag == tar.TypeDir {
					header.Mode = 0777
				} else {
					header.Mode = 0666
				}

				if err := tw.WriteHeader(header); err != nil {
					_ = pw.CloseWithError(err)
					return
				}
				if _, err := io.Copy(tw, tr); err != nil {
					_ = pw.CloseWithError(err)
					return
				}
			}
		}(s3Key, decompStream, obj, mu)

		err = cli.CopyToContainer(ctx, containerID, targetParentDir, pr, container.CopyToContainerOptions{
			AllowOverwriteDirWithFile: true,
		})
		if err != nil {
			log.Printf("[fast-bridge] docker copy to container %s failed for %s: %v", containerID[:12], s3Key, err)
			continue
		}

		log.Printf("[fast-bridge] successfully hydrated %s from s3://%s/%s into container %s:%s", s3Key, b.bucket, s3Key, containerID[:12], targetParentDir)
		return nil
	}

	return nil
}

// SnapshotContainer extracts a tar archive from the container directory using Docker API,
// compresses it with parallel Zstandard, and streams it directly to S3.
func (b *FastVolumeBridge) SnapshotContainer(ctx context.Context, cli *client.Client, containerID, srcDir, s3Key string) error {
	if b.client == nil || cli == nil {
		return nil
	}
	mu := b.getLock(s3Key)
	mu.Lock()
	defer mu.Unlock()

	reader, _, err := cli.CopyFromContainer(ctx, containerID, srcDir)
	if err != nil {
		return fmt.Errorf("docker copy from container %s (%s): %w", containerID[:12], srcDir, err)
	}
	defer reader.Close()

	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(0))
	if err != nil {
		return fmt.Errorf("create zstd writer: %w", err)
	}

	tr := tar.NewReader(reader)
	tw := tar.NewWriter(zw)
	fileCount := 0

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}

		// Skip stale lockfiles and sockets during snapshot creation
		baseName := filepath.Base(header.Name)
		if strings.HasSuffix(baseName, ".pid") ||
			strings.HasSuffix(baseName, ".lock") ||
			strings.HasSuffix(baseName, ".sock") ||
			baseName == "pm2.pid" ||
			baseName == "server.pid" ||
			strings.HasSuffix(baseName, ".db-shm") ||
			strings.HasSuffix(baseName, ".db-wal") {
			continue
		}

		// Normalize UID/GID for portability across all Docker hosts and userns configurations
		header.Uid = 0
		header.Gid = 0
		header.Uname = ""
		header.Gname = ""
		if header.Typeflag == tar.TypeDir {
			header.Mode = 0777
		} else {
			header.Mode = 0666
		}

		if err := tw.WriteHeader(header); err != nil {
			continue
		}
		if _, err := io.Copy(tw, tr); err != nil {
			continue
		}
		fileCount++
	}

	_ = tw.Close()
	if err := zw.Close(); err != nil {
		return fmt.Errorf("close zstd writer: %w", err)
	}

	// Avoid uploading empty archives
	if fileCount == 0 || buf.Len() <= 32 {
		return nil
	}

	payload := buf.Bytes()
	_, err = b.client.PutObject(ctx, b.bucket, s3Key, bytes.NewReader(payload), int64(len(payload)), minio.PutObjectOptions{
		ContentType: "application/zstd",
		UserMetadata: map[string]string{
			"clever-route-container": containerID,
			"clever-route-path":      srcDir,
		},
	})
	if err != nil {
		return fmt.Errorf("put S3 object %s: %w", s3Key, err)
	}

	log.Printf("[fast-bridge] streamed snapshot (%d files -> %d zstd bytes) from container %s:%s -> s3://%s/%s", fileCount, len(payload), containerID[:12], srcDir, b.bucket, s3Key)
	return nil
}
