package engine

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// Phase represents a stage of the sync pipeline.
type Phase string

const (
	PhaseProbe   Phase = "probe"
	PhasePlan    Phase = "plan"
	PhaseClone   Phase = "clone"
	PhaseLFS     Phase = "lfs"
	PhasePush    Phase = "push"
	PhaseCleanup Phase = "cleanup"
)

// ProgressFunc receives progress updates during engine operations.
// repoID identifies which repo (useful in batch), phase indicates
// the current pipeline stage, and msg provides human-readable detail.
type ProgressFunc func(repoID string, phase Phase, msg string)

// ProgressWriter is a Writer that reports bytes written at intervals.
// Useful for wrapping git command output to show throughput.
type ProgressWriter struct {
	mu       sync.Mutex
	total    int64
	last     int64
	lastTime time.Time
	onUpdate func(bytes int64, elapsed time.Duration)
}

// NewProgressWriter creates a writer that calls onUpdate periodically.
func NewProgressWriter(onUpdate func(bytes int64, elapsed time.Duration)) *ProgressWriter {
	return &ProgressWriter{
		lastTime: time.Now(),
		onUpdate: onUpdate,
	}
}

func (pw *ProgressWriter) Write(p []byte) (int, error) {
	n := len(p)
	pw.mu.Lock()
	pw.total += int64(n)
	if time.Since(pw.lastTime) > 500*time.Millisecond {
		pw.onUpdate(pw.total, time.Since(pw.lastTime))
		pw.lastTime = time.Now()
		pw.last = pw.total
	}
	pw.mu.Unlock()
	return n, nil
}

// NopProgress is a no-op progress function.
func NopProgress(_ string, _ Phase, _ string) {}

// TextProgress returns a ProgressFunc that writes human-readable status to w.
func TextProgress(w io.Writer) ProgressFunc {
	start := time.Now()
	return func(repoID string, phase Phase, msg string) {
		elapsed := time.Since(start).Truncate(time.Millisecond)
		fmt.Fprintf(w, "  [%s] %-8s %s\n", elapsed, phase, msg)
	}
}
