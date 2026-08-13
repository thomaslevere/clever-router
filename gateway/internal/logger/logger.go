package logger

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/clever-route/gateway/internal/cache"
	"github.com/clever-route/gateway/internal/store"
)

// Hub is the centralized enterprise logging engine for CleverRoute.
type Hub struct {
	store *store.Store
	cache *cache.Cache

	fileMu   sync.Mutex
	logFile  *os.File
	filePath string
}

var globalHub *Hub
var hubMu sync.RWMutex

// Init initializes the global log hub.
func Init(st *store.Store, c *cache.Cache, logDir string) (*Hub, error) {
	if logDir == "" {
		logDir = "logs"
	}
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	filePath := filepath.Join(logDir, "cleverroute.log")
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}

	h := &Hub{
		store:    st,
		cache:    c,
		logFile:  f,
		filePath: filePath,
	}

	hubMu.Lock()
	globalHub = h
	hubMu.Unlock()

	return h, nil
}

// Log records a structured log entry across disk, PostgreSQL, Redis pub/sub, and stdout.
func (h *Hub) Log(ctx context.Context, level, source, routerSlug, msg string, meta store.Map) {
	if meta == nil {
		meta = store.Map{}
	}

	entry := &store.SystemLog{
		TS:         time.Now().UTC(),
		Level:      level,
		Source:     source,
		RouterSlug: routerSlug,
		Message:    msg,
		Metadata:   meta,
	}

	// 1. Stdout print
	log.Printf("[%s] [%s%s] %s", level, source, func() string {
		if routerSlug != "" {
			return ":" + routerSlug
		}
		return ""
	}(), msg)

	// 2. Append to persistent log file
	h.fileMu.Lock()
	if h.logFile != nil {
		metaBytes, _ := json.Marshal(meta)
		line := fmt.Sprintf("%s [%s] [%s%s] %s %s\n",
			entry.TS.Format(time.RFC3339),
			level,
			source,
			func() string {
				if routerSlug != "" {
					return ":" + routerSlug
				}
				return ""
			}(),
			msg,
			string(metaBytes),
		)
		_, _ = h.logFile.WriteString(line)
	}
	h.fileMu.Unlock()

	// 3. Insert into PostgreSQL system_logs in background
	if h.store != nil {
		go func(e *store.SystemLog) {
			bgCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = h.store.InsertSystemLog(bgCtx, e)
		}(entry)
	}

	// 4. Publish to Redis Pub/Sub for real-time WebSocket distribution
	if h.cache != nil {
		go func(e *store.SystemLog) {
			bgCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			b, err := json.Marshal(e)
			if err == nil {
				_ = h.cache.PublishEvent(bgCtx, "events:logs", string(b))
			}
		}(entry)
	}
}

// Close closes the underlying log file.
func (h *Hub) Close() error {
	h.fileMu.Lock()
	defer h.fileMu.Unlock()
	if h.logFile != nil {
		return h.logFile.Close()
	}
	return nil
}

// FilePath returns the active log file path.
func (h *Hub) FilePath() string {
	return h.filePath
}

// Convenience package-level helpers
func Info(source, routerSlug, msg string, meta store.Map) {
	hubMu.RLock()
	h := globalHub
	hubMu.RUnlock()
	if h != nil {
		h.Log(context.Background(), "INFO", source, routerSlug, msg, meta)
	}
}

func Warn(source, routerSlug, msg string, meta store.Map) {
	hubMu.RLock()
	h := globalHub
	hubMu.RUnlock()
	if h != nil {
		h.Log(context.Background(), "WARN", source, routerSlug, msg, meta)
	}
}

func Error(source, routerSlug, msg string, meta store.Map) {
	hubMu.RLock()
	h := globalHub
	hubMu.RUnlock()
	if h != nil {
		h.Log(context.Background(), "ERROR", source, routerSlug, msg, meta)
	}
}

func Debug(source, routerSlug, msg string, meta store.Map) {
	hubMu.RLock()
	h := globalHub
	hubMu.RUnlock()
	if h != nil {
		h.Log(context.Background(), "DEBUG", source, routerSlug, msg, meta)
	}
}
