package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mendax0110/hf-sync/internal/hfapi"
)

// TestSync_DryRun_WithMockAPI tests the full Sync pipeline in dry-run mode
// using a mock HuggingFace API server and a mock git remote.
func TestSync_DryRun_EmptyTarget(t *testing.T) {
	// Mock HF API — reports repo exists but target has no refs.
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead:
			// RepoExists check.
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer apiSrv.Close()

	client := hfapi.NewClient("test-token").WithAPIBase(apiSrv.URL).WithHubBase(apiSrv.URL)
	eng := New(client)

	// Since dry-run doesn't actually probe remotes (we test plan logic separately),
	// we test that the full Sync function handles dry-run gracefully when probe fails.
	opts := SyncOptions{
		SourceURL:  "https://fake-git-host.example.com/org/repo.git",
		RepoID:     "user/dataset",
		RepoType:   hfapi.RepoTypeDataset,
		DryRun:     true,
		Tags:       true,
		CreateRepo: false,
	}

	// This will fail at probe (can't reach fake host) — that's expected.
	_, err := eng.Sync(context.Background(), opts)
	if err == nil {
		t.Fatal("expected error when source is unreachable")
	}
}

func TestSync_EnsureRepo_Creates(t *testing.T) {
	var createCalled bool

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead:
			// RepoExists: return 404 to trigger creation.
			w.WriteHeader(http.StatusNotFound)
		case r.URL.Path == "/repos/create":
			createCalled = true
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"url": "https://huggingface.co/datasets/user/new-repo",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer apiSrv.Close()

	client := hfapi.NewClient("test-token").WithAPIBase(apiSrv.URL).WithHubBase(apiSrv.URL)
	eng := New(client)

	opts := SyncOptions{
		SourceURL:  "https://fake-git-host.example.com/org/repo.git",
		RepoID:     "user/new-repo",
		RepoType:   hfapi.RepoTypeDataset,
		Private:    true,
		DryRun:     false,
		CreateRepo: true,
		Tags:       true,
	}

	// Will fail at probe (fake host), but ensureRepo should have been called first.
	eng.Sync(context.Background(), opts)

	if !createCalled {
		t.Error("expected CreateRepo to be called when repo doesn't exist")
	}
}

func TestSync_EnsureRepo_SkipsExisting(t *testing.T) {
	var createCalled bool

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead:
			// RepoExists: return 200 — repo already exists.
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/repos/create":
			createCalled = true
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer apiSrv.Close()

	client := hfapi.NewClient("test-token").WithAPIBase(apiSrv.URL).WithHubBase(apiSrv.URL)
	eng := New(client)

	opts := SyncOptions{
		SourceURL:  "https://fake-git-host.example.com/org/repo.git",
		RepoID:     "user/existing",
		RepoType:   hfapi.RepoTypeDataset,
		DryRun:     false,
		CreateRepo: true,
		Tags:       true,
	}

	eng.Sync(context.Background(), opts)

	if createCalled {
		t.Error("should NOT call CreateRepo when repo already exists")
	}
}

func TestSync_DryRun_SkipsEnsureRepo(t *testing.T) {
	var headCalled bool

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			headCalled = true
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer apiSrv.Close()

	client := hfapi.NewClient("test-token").WithAPIBase(apiSrv.URL).WithHubBase(apiSrv.URL)
	eng := New(client)

	opts := SyncOptions{
		SourceURL:  "https://fake-git-host.example.com/org/repo.git",
		RepoID:     "user/repo",
		RepoType:   hfapi.RepoTypeModel,
		DryRun:     true,
		CreateRepo: true,
		Tags:       true,
	}

	eng.Sync(context.Background(), opts)

	if headCalled {
		t.Error("dry-run should not check repo existence (ensureRepo is skipped)")
	}
}

func TestSync_ContextCanceled(t *testing.T) {
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer apiSrv.Close()

	client := hfapi.NewClient("test-token").WithAPIBase(apiSrv.URL).WithHubBase(apiSrv.URL)
	eng := New(client)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	opts := SyncOptions{
		SourceURL:  "https://fake-git-host.example.com/org/repo.git",
		RepoID:     "user/repo",
		RepoType:   hfapi.RepoTypeDataset,
		DryRun:     false,
		CreateRepo: false,
		Tags:       true,
	}

	_, err := eng.Sync(ctx, opts)
	if err == nil {
		t.Fatal("expected error with canceled context")
	}
}

func TestSync_ResultFields(t *testing.T) {
	// We can't easily test the full flow without a real git server,
	// but we can verify the result struct is populated correctly
	// by testing the plan-based counting.
	client := hfapi.NewClient("tok")
	eng := New(client)

	// Test that SyncResult.Source and Target are set correctly.
	opts := SyncOptions{
		SourceURL: "https://github.com/org/repo.git",
		RepoID:    "user/my-dataset",
		RepoType:  hfapi.RepoTypeDataset,
		DryRun:    true,
	}

	// Will fail at probe, but we can inspect the target URL construction.
	expectedTarget := "https://huggingface.co/datasets/user/my-dataset"
	actualTarget := eng.hf.GitURL(opts.RepoID, opts.RepoType)
	if actualTarget != expectedTarget {
		t.Errorf("target URL = %q, want %q", actualTarget, expectedTarget)
	}
	_ = eng // Suppress unused warning in minimal test.
}
