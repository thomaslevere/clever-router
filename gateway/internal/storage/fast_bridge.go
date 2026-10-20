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

// isDatabaseFile returns true if the entry is an essential database or configuration secret file.
func isDatabaseFile(name string) bool {
	lower := strings.ToLower(name)
	base := filepath.Base(lower)
	if strings.HasSuffix(base, ".sqlite") || strings.HasSuffix(base, ".db") ||
		strings.HasSuffix(base, ".sqlite3") || base == "server.env" ||
		strings.HasSuffix(base, ".env") || base == "jwt-secret" ||
		base == "machine-id" || strings.HasSuffix(base, ".sqlite-wal") ||
		strings.HasSuffix(base, ".db-wal") || base == "config.yaml" {
		return true
	}
	return false
}

// inspectSnapshot quickly inspects an S3 archive's tar headers to determine if it exists,
// its size, and whether it contains database files.
func (b *FastVolumeBridge) inspectSnapshot(ctx context.Context, s3Key string) (exists bool, size int64, hasDB bool, err error) {
	if b.client == nil || s3Key == "" {
		return false, 0, false, nil
	}
	obj, err := b.client.GetObject(ctx, b.bucket, s3Key, minio.GetObjectOptions{})
	if err != nil {
		return false, 0, false, err
	}
	defer obj.Close()

	stat, err := obj.Stat()
	if err != nil || stat.Size <= 32 {
		return false, 0, false, nil
	}

	stream, err := openArchiveStream(obj)
	if err != nil {
		return true, stat.Size, false, nil
	}
	defer stream.Close()

	tr := tar.NewReader(stream)
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		if isDatabaseFile(hdr.Name) {
			return true, stat.Size, true, nil
		}
	}
	return true, stat.Size, false, nil
}

// HydrateContainer downloads a snapshot from S3, decompresses it, normalizes all tar UID/GIDs to 0 (root),
// and copies it directly into the target container directory via Docker API.
func (b *FastVolumeBridge) HydrateContainer(ctx context.Context, cli *client.Client, containerID, s3Key, targetParentDir string) error {
	return b.HydrateContainerCandidateKeys(ctx, cli, containerID, []string{s3Key}, targetParentDir)
}

// HydrateContainerCandidateKeys tries candidate s3Keys, prioritizing snapshots that contain
// active database files. If candidate keys lack database files, it automatically searches
// namespaces/ in S3 for historical snapshots belonging to the router or adapter.
func (b *FastVolumeBridge) HydrateContainerCandidateKeys(ctx context.Context, cli *client.Client, containerID string, candidateKeys []string, targetParentDir string) error {
	if b.client == nil || cli == nil {
		return nil
	}

	var bestKey string
	var fallbackKey string

	for _, s3Key := range candidateKeys {
		exists, size, hasDB, err := b.inspectSnapshot(ctx, s3Key)
		if err != nil || !exists || size <= 32 {
			continue
		}
		if hasDB {
			bestKey = s3Key
			break
		}
		if fallbackKey == "" {
			fallbackKey = s3Key
		}
	}

	// If no candidate key contains a database, search S3 namespaces for any historical snapshot with a database!
	if bestKey == "" {
		volPattern := "app_data"
		if strings.Contains(targetParentDir, "server") {
			volPattern = "app_server_data"
		}
		log.Printf("[fast-bridge] No primary candidate keys contained database files. Searching S3 bucket %q for historical snapshots matching *%s*...", b.bucket, volPattern)

		var largestHistoricalKey string
		var largestHistoricalSize int64
		opts := minio.ListObjectsOptions{
			Prefix:    "namespaces/",
			Recursive: true,
		}
		for obj := range b.client.ListObjects(ctx, b.bucket, opts) {
			if obj.Err != nil || obj.Size <= 1024 {
				continue
			}
			if !strings.Contains(obj.Key, volPattern) && !strings.Contains(obj.Key, "data") {
				continue
			}
			_, _, histHasDB, _ := b.inspectSnapshot(ctx, obj.Key)
			if histHasDB && obj.Size > largestHistoricalSize {
				largestHistoricalKey = obj.Key
				largestHistoricalSize = obj.Size
			}
		}
		if largestHistoricalKey != "" {
			log.Printf("[fast-bridge] Found historical snapshot %s (size: %d bytes, contains database). Auto-migrating to container %s!", largestHistoricalKey, largestHistoricalSize, containerID[:12])
			bestKey = largestHistoricalKey
		}
	}

	selectedKey := bestKey
	if selectedKey == "" {
		selectedKey = fallbackKey
	}
	if selectedKey == "" {
		return nil
	}

	return b.extractKeyToContainer(ctx, cli, containerID, selectedKey, targetParentDir)
}

func (b *FastVolumeBridge) extractKeyToContainer(ctx context.Context, cli *client.Client, containerID, s3Key, targetParentDir string) error {
	mu := b.getLock(s3Key)
	mu.Lock()
	defer mu.Unlock()

	obj, err := b.client.GetObject(ctx, b.bucket, s3Key, minio.GetObjectOptions{})
	if err != nil {
		return err
	}
	defer obj.Close()

	stat, err := obj.Stat()
	if err != nil || stat.Size <= 32 {
		return nil
	}

	log.Printf("[fast-bridge] Found valid snapshot %s (size: %d bytes) in S3. Hydrating into container %s:%s...", s3Key, stat.Size, containerID[:12], targetParentDir)

	decompStream, err := openArchiveStream(obj)
	if err != nil {
		return fmt.Errorf("decompress stream error for %s: %w", s3Key, err)
	}
	defer decompStream.Close()

	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()

		tr := tar.NewReader(decompStream)
		tw := tar.NewWriter(pw)
		defer tw.Close()

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
				baseName == "server.pid" {
				continue
			}

			// Protect container executable code from corrupted /app snapshots:
			if targetParentDir == "/app" || targetParentDir == "/" {
				topSegment := strings.Split(cleaned, "/")[0]
				if topSegment == "node_modules" || topSegment == "package.json" || topSegment == "package-lock.json" ||
					topSegment == "server.js" || topSegment == "index.js" || topSegment == "bin" || topSegment == "dist" ||
					topSegment == ".next" || topSegment == "build" || topSegment == "src" {
					continue
				}
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
	}()

	err = cli.CopyToContainer(ctx, containerID, targetParentDir, pr, container.CopyToContainerOptions{
		AllowOverwriteDirWithFile: true,
	})
	if err != nil {
		return fmt.Errorf("docker copy to container %s failed for %s: %w", containerID[:12], s3Key, err)
	}

	log.Printf("[fast-bridge] successfully hydrated %s from s3://%s/%s into container %s:%s", s3Key, b.bucket, s3Key, containerID[:12], targetParentDir)
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
	hasDB := false

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}

		if isDatabaseFile(header.Name) {
			hasDB = true
		}

		// Skip stale lockfiles and sockets during snapshot creation
		baseName := filepath.Base(header.Name)
		if strings.HasSuffix(baseName, ".pid") ||
			strings.HasSuffix(baseName, ".lock") ||
			strings.HasSuffix(baseName, ".sock") ||
			baseName == "pm2.pid" ||
			baseName == "server.pid" {
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

	// Safety check: before overwriting an existing snapshot, verify we aren't destroying database state
	if !hasDB {
		existingExists, _, existingHasDB, _ := b.inspectSnapshot(ctx, s3Key)
		if existingExists && existingHasDB {
			log.Printf("[fast-bridge] SAFETY GUARD: skipping snapshot for %s: container %s contains 0 database files, refusing to overwrite existing valid S3 database snapshot", s3Key, containerID[:12])
			return nil
		}
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

// CopyS3Object duplicates an existing snapshot key inside the bucket server-side in milliseconds,
// avoiding any re-reading or re-tarring of container directories.
func (b *FastVolumeBridge) CopyS3Object(ctx context.Context, srcKey, dstKey string) error {
	if b.client == nil || srcKey == "" || dstKey == "" || srcKey == dstKey {
		return nil
	}
	srcOpts := minio.CopySrcOptions{
		Bucket: b.bucket,
		Object: srcKey,
	}
	dstOpts := minio.CopyDestOptions{
		Bucket: b.bucket,
		Object: dstKey,
	}
	_, err := b.client.CopyObject(ctx, dstOpts, srcOpts)
	if err != nil {
		return fmt.Errorf("copy S3 object %s -> %s: %w", srcKey, dstKey, err)
	}
	return nil
}
