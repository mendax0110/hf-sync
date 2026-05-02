package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/mendax0110/hf-sync/internal/hfapi"
)

func TestIsTransientGitError(t *testing.T) {
	tests := []struct {
		name   string
		output string
		err    error
		want   bool
	}{
		{"connection reset", "fatal: Connection reset by peer", errors.New("exit 128"), true},
		{"connection timeout", "fatal: Connection timed out", errors.New("exit 128"), true},
		{"dns failure", "fatal: Could not resolve host: github.com", errors.New("exit 128"), true},
		{"ssl error", "fatal: SSL_ERROR_SYSCALL, errno 54", errors.New("exit 128"), true},
		{"remote hung up", "fatal: The remote end hung up unexpectedly", errors.New("exit 128"), true},
		{"early eof", "error: RPC failed; result=18, HTTP code = 200\nfatal: early EOF", errors.New("exit 128"), true},
		{"unable to access", "fatal: unable to access 'https://...': Couldn't connect", errors.New("exit 128"), true},
		{"http 429", "error: HTTP 429 Too Many Requests", errors.New("exit 22"), true},
		{"http 502", "error: HTTP 502 Bad Gateway", errors.New("exit 22"), true},
		{"http 503", "error: HTTP 503 Service Unavailable", errors.New("exit 22"), true},
		{"http 504", "error: HTTP 504 Gateway Timeout", errors.New("exit 22"), true},
		{"connection refused", "fatal: Connection refused", errors.New("exit 128"), true},
		{"failed to connect", "fatal: failed to connect to github.com", errors.New("exit 128"), true},
		{"auth failure", "fatal: Authentication failed for 'https://...'", errors.New("exit 128"), false},
		{"pre-receive hook", "remote: error: pre-receive hook declined", errors.New("exit 1"), false},
		{"not found", "fatal: repository 'https://...' not found", errors.New("exit 128"), false},
		{"permission denied", "fatal: Could not read from remote repository.", errors.New("exit 128"), false},
		{"lfs rejection", "files larger than 10 MiB require git-lfs", errors.New("exit 1"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTransientGitError(tt.output, tt.err)
			if got != tt.want {
				t.Errorf("isTransientGitError(%q, %v) = %v, want %v", tt.output, tt.err, got, tt.want)
			}
		})
	}
}

func TestIsLFSRejection(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   bool
	}{
		{"size rejection", "remote: files larger than 10 MiB need to be tracked with Git LFS", true},
		{"git-lfs link", "See https://git-lfs.github.com for more info", true},
		{"binary files rejection", "remote: Your push was rejected because it contains binary files.", true},
		{"xet storage link", "remote: Please use https://huggingface.co/docs/hub/xet to store binary files.", true},
		{"normal push output", "To https://huggingface.co/datasets/...\n * [new branch]  main -> main", false},
		{"auth error", "fatal: Authentication failed", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isLFSRejection(tt.output)
			if got != tt.want {
				t.Errorf("isLFSRejection(%q) = %v, want %v", tt.output, got, tt.want)
			}
		})
	}
}

func TestRunGitRetry_SucceedsImmediately(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}

	client := hfapi.NewClient("tok")
	eng := New(client).WithRetries(3).WithGitTimeout(5 * time.Second)

	// Use a simple git command that always succeeds.
	out, err := eng.runGitRetry(context.Background(), "", "version")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) == 0 {
		t.Error("expected output from git version")
	}
}

func TestRunGitRetry_RespectsContext(t *testing.T) {
	client := hfapi.NewClient("tok")
	eng := New(client).WithRetries(3).WithGitTimeout(30 * time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := eng.runGitRetry(ctx, "", "version")
	if err == nil {
		t.Fatal("expected error with canceled context")
	}
}

func TestRunGitRetry_DoesNotRetryPermanentErrors(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}

	client := hfapi.NewClient("tok")
	eng := New(client).WithRetries(3).WithGitTimeout(5 * time.Second)

	// Run a git command that will fail permanently (bad subcommand).
	start := time.Now()
	_, err := eng.runGitRetry(context.Background(), "", "totally-invalid-subcommand")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from invalid git command")
	}

	// Should fail immediately without retrying (no backoff delay).
	if elapsed > 2*time.Second {
		t.Errorf("took %v — should not have retried a permanent error", elapsed)
	}
}

func TestRunGitRetry_RespectsTimeout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}

	client := hfapi.NewClient("tok")
	// Very short timeout to trigger kill.
	eng := New(client).WithRetries(0).WithGitTimeout(100 * time.Millisecond)

	// Run a command that would take a long time — the timeout should kill it.
	start := time.Now()
	_, err := eng.runGitRetry(context.Background(), "", "fetch", "https://this-will-timeout.example.invalid/repo.git")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from timeout")
	}

	// Should complete in well under 10 seconds due to timeout.
	if elapsed > 5*time.Second {
		t.Errorf("took %v — timeout should have been enforced at 100ms", elapsed)
	}
}

func TestRunGit_CapturesOutput(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}

	out, err := runGit(context.Background(), "", "version")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Contains(out, []byte("git version")) {
		t.Errorf("expected 'git version' in output, got %q", string(out))
	}
}

func TestRunGit_RespectsDir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}

	dir := t.TempDir()
	// git status in the temp dir (not a git repo) should fail.
	_, err := runGit(context.Background(), dir, "status")
	if err == nil {
		t.Error("expected error running git status in non-repo dir")
	}
}

func TestRunGit_FailsWithBadCommand(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}

	_, err := runGit(context.Background(), "", "not-a-real-command-xyz")
	if err == nil {
		t.Error("expected error for bad git subcommand")
	}
}

// TestScrubToken verifies credential scrubbing.
func TestScrubToken(t *testing.T) {
	tests := []struct {
		input string
		token string
		want  string
	}{
		{"https://x-token-auth:hf_secret123@huggingface.co/repo", "hf_secret123", "https://x-token-auth:***@huggingface.co/repo"},
		{"no token here", "mytoken123", "no token here"},
		{"empty token", "", "empty token"},
		{"hf_abc and hf_abc again", "hf_abc", "*** and *** again"},
	}

	for _, tt := range tests {
		got := scrubToken(tt.input, tt.token)
		if got != tt.want {
			t.Errorf("scrubToken(%q, %q) = %q, want %q", tt.input, tt.token, got, tt.want)
		}
	}
}

// TestInjectToken verifies token injection into git URLs.
func TestInjectToken(t *testing.T) {
	tests := []struct {
		name  string
		url   string
		token string
		want  string
	}{
		{"https with token", "https://github.com/org/repo.git", "ghp_abc", "https://x-token-auth:ghp_abc@github.com/org/repo.git"},
		{"empty token no-op", "https://github.com/org/repo.git", "", "https://github.com/org/repo.git"},
		{"already has user", "https://user:old@host.com/repo", "new-tok", "https://x-token-auth:new-tok@host.com/repo"},
		{"ssh url passthrough", "git@github.com:org/repo.git", "tok", "git@github.com:org/repo.git"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := injectToken(tt.url, tt.token)
			if got != tt.want {
				t.Errorf("injectToken(%q, %q) = %q, want %q", tt.url, tt.token, got, tt.want)
			}
		})
	}
}

// TestHasLFS just validates hasLFS doesn't panic.
func TestHasLFS(t *testing.T) {
	// We can't control whether git-lfs is installed, but this shouldn't panic.
	result := hasLFS()
	t.Logf("hasLFS() = %v", result)

	// Verify it's consistent.
	if hasLFS() != result {
		t.Error("hasLFS() returned inconsistent results")
	}
}

// TestNopProgress verifies NopProgress doesn't panic.
func TestNopProgress(t *testing.T) {
	NopProgress("repo", PhaseProbe, "msg")
}

// Verify exec.ExitError is detected.
func TestIsTransientGitError_ExitError(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}

	// Simulate what happens when git returns an exit code.
	cmd := exec.Command("git", "fetch", "https://nonexistent.example.invalid/repo.git")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Skip("git fetch unexpectedly succeeded")
	}

	// The output should indicate a transient-looking error (unable to access / could not resolve).
	result := isTransientGitError(string(out), err)
	t.Logf("isTransientGitError for DNS failure: %v (output: %q)", result, string(out))

	// DNS failures should be transient.
	if !result {
		// Could not resolve host → should be transient.
		if bytes.Contains(out, []byte("Could not resolve")) || bytes.Contains(out, []byte("unable to access")) || bytes.Contains(out, []byte("failed to connect")) {
			t.Error("expected DNS resolution failure to be transient")
		}
	}
}

// TestEngineWithMethodChaining ensures builder pattern works.
func TestEngineWithMethodChaining(t *testing.T) {
	client := hfapi.NewClient("tok")
	eng := New(client).
		WithRetries(5).
		WithGitTimeout(2 * time.Minute).
		WithProgress(func(string, Phase, string) {})

	if eng.retries != 5 {
		t.Errorf("retries = %d, want 5", eng.retries)
	}
	if eng.gitTimeout != 2*time.Minute {
		t.Errorf("gitTimeout = %v, want 2m", eng.gitTimeout)
	}
}

// Benchmark plan with many refs.
func BenchmarkPlan_100Refs(b *testing.B) {
	e := &Engine{}
	sourceRefs := make(map[string]string, 100)
	for i := 0; i < 100; i++ {
		sourceRefs[fmt.Sprintf("refs/heads/branch-%d", i)] = fmt.Sprintf("hash-%d", i)
	}
	targetRefs := make(map[string]string, 50)
	for i := 0; i < 50; i++ {
		targetRefs[fmt.Sprintf("refs/heads/branch-%d", i)] = fmt.Sprintf("old-hash-%d", i)
	}
	opts := SyncOptions{Tags: true, Prune: true, Force: true}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.plan(sourceRefs, targetRefs, opts)
	}
}
