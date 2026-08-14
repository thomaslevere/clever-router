package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestVolumeWatcherLifecycle(t *testing.T) {
	tmpDir := t.TempDir()

	watcher, err := NewVolumeWatcher(nil, tmpDir, "test/key.tar.zst", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("NewVolumeWatcher error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watcher.Start(ctx)

	// Simulate file write
	testFile := filepath.Join(tmpDir, "test.sqlite-wal")
	if err := os.WriteFile(testFile, []byte("wal frame"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// Give watcher a moment to process inotify event
	time.Sleep(150 * time.Millisecond)

	// Close cleanly
	watcher.Close()
}
