package storage

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// VolumeWatcher monitors a local volume scratch directory using Linux inotify (fsnotify)
// and debounces writes to trigger asynchronous S3 volume snapshots without blocking inference.
type VolumeWatcher struct {
	bridge       *FastVolumeBridge
	watcher      *fsnotify.Watcher
	localDir     string
	s3Key        string
	debounceTime time.Duration
	triggerChan  chan struct{}
	stopChan     chan struct{}
	closeOnce    sync.Once
}

var ignoredExtensions = []string{
	"-wal",
	"-shm",
	".journal",
	".lock",
	".tmp",
	".crswap",
	".swp",
}

func shouldIgnoreFile(path string) bool {
	base := filepath.Base(path)
	if len(base) > 0 && base[0] == '.' {
		return true
	}
	for _, ext := range ignoredExtensions {
		if len(path) >= len(ext) && path[len(path)-len(ext):] == ext {
			return true
		}
	}
	return false
}

// NewVolumeWatcher creates a new volume watcher for localDir with a configurable debounce duration.
func NewVolumeWatcher(bridge *FastVolumeBridge, localDir, s3Key string, debounceTime time.Duration) (*VolumeWatcher, error) {
	if debounceTime <= 0 {
		debounceTime = 5 * time.Second
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	return &VolumeWatcher{
		bridge:       bridge,
		watcher:      w,
		localDir:     localDir,
		s3Key:        s3Key,
		debounceTime: debounceTime,
		triggerChan:  make(chan struct{}, 1),
		stopChan:     make(chan struct{}),
	}, nil
}

// Start begins monitoring the filesystem directory recursively and processing debounce queues.
func (vw *VolumeWatcher) Start(ctx context.Context) {
	if err := os.MkdirAll(vw.localDir, 0755); err != nil {
		log.Printf("[watcher] error creating watch directory %s: %v", vw.localDir, err)
		return
	}

	// Add root directory
	_ = vw.watcher.Add(vw.localDir)

	// Recursively watch all existing subdirectories
	_ = filepath.Walk(vw.localDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && info.IsDir() {
			_ = vw.watcher.Add(path)
		}
		return nil
	})

	go vw.eventLoop(ctx)
	go vw.debounceLoop(ctx)
	log.Printf("[watcher] started inotify monitoring on %s (debounced %v -> %s)", vw.localDir, vw.debounceTime, vw.s3Key)
}

func (vw *VolumeWatcher) eventLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-vw.stopChan:
			return
		case event, ok := <-vw.watcher.Events:
			if !ok {
				return
			}

			// Filter out temporary/journal/WAL files to prevent constant disk I/O lockup
			if shouldIgnoreFile(event.Name) {
				continue
			}

			// If a new directory was created, add it to watcher
			if event.Has(fsnotify.Create) {
				if fi, err := os.Stat(event.Name); err == nil && fi.IsDir() {
					_ = vw.watcher.Add(event.Name)
				}
			}

			// Notify debounce channel on write, create, remove, or rename
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
				select {
				case vw.triggerChan <- struct{}{}:
				default:
					// Channel already has a pending trigger
				}
			}

		case err, ok := <-vw.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("[watcher] error on %s: %v", vw.localDir, err)
		}
	}
}

func (vw *VolumeWatcher) debounceLoop(ctx context.Context) {
	var timer *time.Timer
	var timerC <-chan time.Time

	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case <-vw.stopChan:
			if timer != nil {
				timer.Stop()
			}
			return
		case <-vw.triggerChan:
			if timer != nil {
				timer.Stop()
			}
			timer = time.NewTimer(vw.debounceTime)
			timerC = timer.C

		case <-timerC:
			timerC = nil
			if vw.bridge != nil {
				// Execute snapshot in background so debouncer never blocks
				go func() {
					snapCtx, snapCancel := context.WithTimeout(context.Background(), 2*time.Minute)
					defer snapCancel()
					if err := vw.bridge.StreamSnapshotToS3(snapCtx, vw.localDir, vw.s3Key); err != nil {
						log.Printf("[watcher] sync snapshot error for %s: %v", vw.s3Key, err)
					}
				}()
			}
		}
	}
}

// Close gracefully closes the filesystem watcher and stops debounce loops.
func (vw *VolumeWatcher) Close() {
	vw.closeOnce.Do(func() {
		close(vw.stopChan)
		_ = vw.watcher.Close()
	})
}
