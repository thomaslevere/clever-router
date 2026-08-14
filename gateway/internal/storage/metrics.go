package storage

import (
	"context"
	"runtime"
	"syscall"
	"time"
)

type SystemMetrics struct {
	NumGoroutine int       `json:"num_goroutine"`
	AllocMB      uint64    `json:"alloc_mb"`
	TotalAllocMB uint64    `json:"total_alloc_mb"`
	SysMB        uint64    `json:"sys_mb"`
	NumGC        uint32    `json:"num_gc"`
	ScratchDisk  DiskStats `json:"scratch_disk"`
	S3LatencyMs  int64     `json:"s3_latency_ms"`
	S3Connected  bool      `json:"s3_connected"`
	Timestamp    time.Time `json:"timestamp"`
}

type DiskStats struct {
	TotalBytes uint64  `json:"total_bytes"`
	FreeBytes  uint64  `json:"free_bytes"`
	UsedBytes  uint64  `json:"used_bytes"`
	UsedPct    float64 `json:"used_pct"`
}

// CollectMetrics gathers CPU/heap runtime statistics, scratch disk capacity, and Cellar S3 latency.
func CollectMetrics(bridge *FastVolumeBridge, scratchDir string) SystemMetrics {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	metrics := SystemMetrics{
		NumGoroutine: runtime.NumGoroutine(),
		AllocMB:      m.Alloc / 1024 / 1024,
		TotalAllocMB: m.TotalAlloc / 1024 / 1024,
		SysMB:        m.Sys / 1024 / 1024,
		NumGC:        m.NumGC,
		Timestamp:    time.Now(),
	}

	// 1. Calculate NVMe disk statistics using syscall Statfs
	if scratchDir != "" {
		var stat syscall.Statfs_t
		if err := syscall.Statfs(scratchDir, &stat); err == nil {
			total := stat.Blocks * uint64(stat.Bsize)
			free := stat.Bfree * uint64(stat.Bsize)
			used := uint64(0)
			if total > free {
				used = total - free
			}
			pct := 0.0
			if total > 0 {
				pct = (float64(used) / float64(total)) * 100.0
			}
			metrics.ScratchDisk = DiskStats{
				TotalBytes: total,
				FreeBytes:  free,
				UsedBytes:  used,
				UsedPct:    pct,
			}
		}
	}

	// 2. Measure Cellar S3 Ping Latency
	if bridge != nil && bridge.client != nil {
		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		exists, err := bridge.client.BucketExists(ctx, bridge.bucket)
		if err == nil && exists {
			metrics.S3Connected = true
			metrics.S3LatencyMs = time.Since(start).Milliseconds()
		}
	}

	return metrics
}
