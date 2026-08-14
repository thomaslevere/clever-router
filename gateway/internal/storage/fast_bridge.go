package storage

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

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

// NewFastVolumeBridge initializes MinIO S3 client.
func NewFastVolumeBridge(endpoint, accessKey, secretKey, bucket string, useSSL bool) (*FastVolumeBridge, error) {
	if endpoint == "" || accessKey == "" || secretKey == "" {
		return nil, fmt.Errorf("missing S3 credentials: endpoint, accessKey, and secretKey are required")
	}

	minioClient, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize MinIO client: %w", err)
	}

	// Verify or create bucket if it does not exist
	ctx := context.Background()
	exists, err := minioClient.BucketExists(ctx, bucket)
	if err == nil && !exists {
		if makeErr := minioClient.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); makeErr != nil {
			log.Printf("[fast-bridge] warning: could not make bucket %q: %v", bucket, makeErr)
		} else {
			log.Printf("[fast-bridge] created S3 bucket %q", bucket)
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

// HydrateFromS3 downloads and extracts a .tar.zst archive from S3 into targetDir.
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
	if stat.Size == 0 {
		return nil
	}

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("create target dir %s: %w", targetDir, err)
	}

	zr, err := zstd.NewReader(obj)
	if err != nil {
		return fmt.Errorf("zstd reader for %s: %w", s3Key, err)
	}
	defer zr.Close()

	tr := tar.NewReader(zr)
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

		targetPath := filepath.Join(targetDir, cleaned)
		if header.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return err
		}

		mode := header.FileInfo().Mode()
		if mode == 0 {
			mode = 0644
		}
		f, err := os.OpenFile(targetPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, mode)
		if err != nil {
			return fmt.Errorf("open file %s: %w", targetPath, err)
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return fmt.Errorf("write file %s: %w", targetPath, err)
		}
		f.Close()
		count++
	}

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
	if len(payload) == 0 {
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
