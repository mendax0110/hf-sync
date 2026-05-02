package engine

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestTextProgress_Format(t *testing.T) {
	var buf bytes.Buffer
	progress := TextProgress(&buf)

	progress("user/repo", PhaseProbe, "discovering refs")

	output := buf.String()
	if !strings.Contains(output, "probe") {
		t.Errorf("expected 'probe' in output, got %q", output)
	}
	if !strings.Contains(output, "discovering refs") {
		t.Errorf("expected 'discovering refs' in output, got %q", output)
	}
	// Should have a timestamp like [0s] or [1ms].
	if !strings.Contains(output, "[") || !strings.Contains(output, "]") {
		t.Errorf("expected bracketed timestamp in output, got %q", output)
	}
}

func TestTextProgress_MultiplePhases(t *testing.T) {
	var buf bytes.Buffer
	progress := TextProgress(&buf)

	progress("repo", PhaseProbe, "start")
	progress("repo", PhasePlan, "planning")
	progress("repo", PhaseClone, "cloning")
	progress("repo", PhasePush, "pushing")

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 4 {
		t.Errorf("expected 4 lines, got %d: %q", len(lines), output)
	}
}

func TestNopProgress_NoOp(t *testing.T) {
	// Should not panic or produce side effects.
	NopProgress("repo", PhaseProbe, "test")
	NopProgress("", "", "")
}

func TestPhaseConstants(t *testing.T) {
	phases := []Phase{PhaseProbe, PhasePlan, PhaseClone, PhaseLFS, PhasePush, PhaseCleanup}
	seen := make(map[Phase]bool)

	for _, p := range phases {
		if p == "" {
			t.Error("phase constant should not be empty")
		}
		if seen[p] {
			t.Errorf("duplicate phase constant: %q", p)
		}
		seen[p] = true
	}
}

func TestProgressWriter_CountsBytes(t *testing.T) {
	var lastBytes int64
	pw := NewProgressWriter(func(bytes int64, elapsed time.Duration) {
		lastBytes = bytes
	})

	// Write enough data to potentially trigger the 500ms interval.
	data := make([]byte, 1024)
	for i := 0; i < 10; i++ {
		n, err := pw.Write(data)
		if err != nil {
			t.Fatalf("unexpected write error: %v", err)
		}
		if n != 1024 {
			t.Errorf("expected n=1024, got %d", n)
		}
	}

	// Total should be tracked even if callback wasn't fired (due to 500ms debounce).
	pw.mu.Lock()
	total := pw.total
	pw.mu.Unlock()

	if total != 10*1024 {
		t.Errorf("expected total=10240, got %d", total)
	}

	// If callback was fired, lastBytes should be > 0.
	_ = lastBytes // May or may not have been called depending on timing.
}

func TestProgressWriter_ReturnsCorrectLength(t *testing.T) {
	pw := NewProgressWriter(func(int64, time.Duration) {})

	testData := []byte("hello world")
	n, err := pw.Write(testData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len(testData) {
		t.Errorf("expected n=%d, got %d", len(testData), n)
	}
}
