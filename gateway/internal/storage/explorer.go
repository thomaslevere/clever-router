package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
)

type FileItem struct {
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	IsDir     bool      `json:"is_dir"`
	Mode      string    `json:"mode"`
	ModTime   time.Time `json:"mod_time"`
	Extension string    `json:"extension"`
}

type S3ObjectItem struct {
	Key          string    `json:"key"`
	Size         int64     `json:"size"`
	LastModified time.Time `json:"last_modified"`
	ETag         string    `json:"etag"`
	Namespace    string    `json:"namespace"`
}

type StorageExplorer struct {
	bridge       *FastVolumeBridge
	allowedRoots []string
}

func NewStorageExplorer(bridge *FastVolumeBridge, allowedRoots []string) *StorageExplorer {
	cleanedRoots := make([]string, 0, len(allowedRoots))
	for _, r := range allowedRoots {
		if strings.TrimSpace(r) != "" {
			cleanedRoots = append(cleanedRoots, filepath.Clean(strings.TrimSpace(r)))
		}
	}
	return &StorageExplorer{
		bridge:       bridge,
		allowedRoots: cleanedRoots,
	}
}

// ValidatePath guarantees no path traversal attacks (e.g. ../../etc/passwd)
func (e *StorageExplorer) ValidatePath(targetPath string) (string, error) {
	if targetPath == "" {
		if len(e.allowedRoots) > 0 {
			return e.allowedRoots[0], nil
		}
		return "", fmt.Errorf("no default root path configured")
	}

	cleanPath := filepath.Clean(targetPath)
	for _, root := range e.allowedRoots {
		if cleanPath == root || strings.HasPrefix(cleanPath, root+string(filepath.Separator)) {
			return cleanPath, nil
		}
	}
	return "", fmt.Errorf("access denied: requested path %q is outside authorized directories", targetPath)
}

// ListLocalDirectory returns files and sub-directories within the authorized path
func (e *StorageExplorer) ListLocalDirectory(targetPath string) ([]FileItem, error) {
	safePath, err := e.ValidatePath(targetPath)
	if err != nil {
		return nil, err
	}

	_ = os.MkdirAll(safePath, 0777)
	entries, err := os.ReadDir(safePath)
	if err != nil {
		return nil, fmt.Errorf("read directory %s: %w", safePath, err)
	}

	items := make([]FileItem, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		items = append(items, FileItem{
			Name:      entry.Name(),
			Path:      filepath.Join(safePath, entry.Name()),
			Size:      info.Size(),
			IsDir:     entry.IsDir(),
			Mode:      info.Mode().String(),
			ModTime:   info.ModTime(),
			Extension: filepath.Ext(entry.Name()),
		})
	}
	return items, nil
}

// ReadFilePreview reads the beginning of a file for display in the dashboard
func (e *StorageExplorer) ReadFilePreview(targetPath string, maxBytes int64) (string, error) {
	safePath, err := e.ValidatePath(targetPath)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(safePath)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("cannot preview directory %s", safePath)
	}

	file, err := os.Open(safePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	if maxBytes <= 0 || maxBytes > 2*1024*1024 {
		maxBytes = 512 * 1024 // default 512 KB limit
	}

	buf := make([]byte, maxBytes)
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		return "", err
	}

	return string(buf[:n]), nil
}

// DeleteLocalFile removes a local file or empty directory safely within sandbox
func (e *StorageExplorer) DeleteLocalFile(targetPath string) error {
	safePath, err := e.ValidatePath(targetPath)
	if err != nil {
		return err
	}

	// Safety: protect root directories from deletion
	for _, root := range e.allowedRoots {
		if safePath == root {
			return fmt.Errorf("cannot delete root directory %s", root)
		}
	}

	return os.RemoveAll(safePath)
}

// ListS3Objects returns all objects in the bucket categorized by namespace
func (e *StorageExplorer) ListS3Objects(ctx context.Context, prefix string) ([]S3ObjectItem, error) {
	if e.bridge == nil || e.bridge.client == nil {
		return nil, fmt.Errorf("cellar S3 storage bridge is not initialized")
	}

	opts := minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}

	var objects []S3ObjectItem
	for obj := range e.bridge.client.ListObjects(ctx, e.bridge.bucket, opts) {
		if obj.Err != nil {
			return nil, obj.Err
		}

		parts := strings.Split(obj.Key, "/")
		namespace := "root"
		if len(parts) > 1 && parts[0] == "namespaces" {
			namespace = parts[1]
		} else if len(parts) > 1 && parts[0] == "db" {
			namespace = "system-db"
		}

		objects = append(objects, S3ObjectItem{
			Key:          obj.Key,
			Size:         obj.Size,
			LastModified: obj.LastModified,
			ETag:         strings.Trim(obj.ETag, "\""),
			Namespace:    namespace,
		})
	}

	if objects == nil {
		objects = []S3ObjectItem{}
	}
	return objects, nil
}

// DeleteS3Object removes an object from the Cellar bucket
func (e *StorageExplorer) DeleteS3Object(ctx context.Context, key string) error {
	if e.bridge == nil || e.bridge.client == nil {
		return fmt.Errorf("cellar S3 storage bridge is not initialized")
	}
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("object key cannot be empty")
	}
	return e.bridge.client.RemoveObject(ctx, e.bridge.bucket, key, minio.RemoveObjectOptions{})
}
