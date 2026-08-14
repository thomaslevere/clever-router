package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStorageExplorerValidation(t *testing.T) {
	tmpDir := t.TempDir()
	allowedRoot := filepath.Join(tmpDir, "volumes")
	_ = os.MkdirAll(allowedRoot, 0755)

	exp := NewStorageExplorer(nil, []string{allowedRoot})

	// Valid path inside allowed root
	validPath, err := exp.ValidatePath(filepath.Join(allowedRoot, "router-1", "app_data"))
	if err != nil {
		t.Fatalf("expected valid path, got error: %v", err)
	}
	if !strings.HasPrefix(validPath, allowedRoot) {
		t.Errorf("expected path to start with %s, got %s", allowedRoot, validPath)
	}

	// Path traversal attempt
	_, err = exp.ValidatePath(filepath.Join(allowedRoot, "..", "..", "etc", "passwd"))
	if err == nil {
		t.Fatal("expected error for path traversal, got nil")
	}

	// Outside path attempt
	_, err = exp.ValidatePath("/etc/hosts")
	if err == nil {
		t.Fatal("expected error for outside path, got nil")
	}
}

func TestStorageExplorerLocalOps(t *testing.T) {
	tmpDir := t.TempDir()
	allowedRoot := filepath.Join(tmpDir, "volumes")
	subDir := filepath.Join(allowedRoot, "router-123")
	_ = os.MkdirAll(subDir, 0755)

	testFile := filepath.Join(subDir, "settings.json")
	content := []byte(`{"theme": "dark", "version": 2}`)
	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	exp := NewStorageExplorer(nil, []string{allowedRoot})

	// List directory
	items, err := exp.ListLocalDirectory(subDir)
	if err != nil {
		t.Fatalf("list local directory error: %v", err)
	}
	if len(items) != 1 || items[0].Name != "settings.json" {
		t.Fatalf("unexpected items: %+v", items)
	}

	// Preview file
	preview, err := exp.ReadFilePreview(testFile, 1024)
	if err != nil {
		t.Fatalf("read preview error: %v", err)
	}
	if preview != string(content) {
		t.Errorf("preview mismatch: got %q, want %q", preview, string(content))
	}

	// Delete file
	if err := exp.DeleteLocalFile(testFile); err != nil {
		t.Fatalf("delete file error: %v", err)
	}

	itemsAfter, err := exp.ListLocalDirectory(subDir)
	if err != nil || len(itemsAfter) != 0 {
		t.Errorf("expected 0 items after delete, got %d", len(itemsAfter))
	}
}

func TestCollectMetrics(t *testing.T) {
	tmpDir := t.TempDir()
	metrics := CollectMetrics(nil, tmpDir)

	if metrics.NumGoroutine == 0 {
		t.Errorf("expected NumGoroutine > 0")
	}
	if metrics.ScratchDisk.TotalBytes == 0 {
		t.Errorf("expected ScratchDisk.TotalBytes > 0")
	}
}
