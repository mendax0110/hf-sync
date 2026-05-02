package hfapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewClient(t *testing.T) {
	c := NewClient("hf_test_token")

	if c.token != "hf_test_token" {
		t.Errorf("expected token 'hf_test_token', got %q", c.token)
	}
	if c.apiBase != DefaultAPIBase {
		t.Errorf("expected apiBase %q, got %q", DefaultAPIBase, c.apiBase)
	}
	if c.hubBase != DefaultHubBase {
		t.Errorf("expected hubBase %q, got %q", DefaultHubBase, c.hubBase)
	}
	if c.httpClient == nil {
		t.Fatal("expected non-nil httpClient")
	}
	if c.httpClient.Timeout != 30*time.Second {
		t.Errorf("expected 30s timeout, got %v", c.httpClient.Timeout)
	}
	if c.retries != 3 {
		t.Errorf("expected retries=3, got %d", c.retries)
	}
}

func TestWithAPIBase(t *testing.T) {
	c := NewClient("tok").WithAPIBase("http://localhost:8080/api/")

	// Should trim trailing slash.
	if c.apiBase != "http://localhost:8080/api" {
		t.Errorf("expected trimmed base, got %q", c.apiBase)
	}
}

func TestWithHubBase(t *testing.T) {
	c := NewClient("tok").WithHubBase("http://localhost:9090/")

	if c.hubBase != "http://localhost:9090" {
		t.Errorf("expected trimmed base, got %q", c.hubBase)
	}
}

func TestToken(t *testing.T) {
	c := NewClient("secret-token")
	if c.Token() != "secret-token" {
		t.Errorf("Token() = %q, want %q", c.Token(), "secret-token")
	}
}

func TestGitURL_AllTypes(t *testing.T) {
	c := NewClient("tok")

	tests := []struct {
		name     string
		repoID   string
		repoType RepoType
		want     string
	}{
		{"model default", "user/my-model", RepoTypeModel, "https://huggingface.co/user/my-model"},
		{"dataset", "org/data-repo", RepoTypeDataset, "https://huggingface.co/datasets/org/data-repo"},
		{"space", "user/my-app", RepoTypeSpace, "https://huggingface.co/spaces/user/my-app"},
		{"empty type defaults to model", "user/repo", "", "https://huggingface.co/user/repo"},
		{"nested org path", "org-name/sub-repo", RepoTypeDataset, "https://huggingface.co/datasets/org-name/sub-repo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.GitURL(tt.repoID, tt.repoType)
			if got != tt.want {
				t.Errorf("GitURL(%q, %q) = %q, want %q", tt.repoID, tt.repoType, got, tt.want)
			}
		})
	}
}

func TestGitURL_CustomHubBase(t *testing.T) {
	c := NewClient("tok").WithHubBase("https://hf-mirror.example.com")

	got := c.GitURL("user/repo", RepoTypeDataset)
	want := "https://hf-mirror.example.com/datasets/user/repo"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRepoExists_Found(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-tok" {
			t.Errorf("expected auth 'Bearer test-tok', got %q", auth)
		}
		if r.Method != http.MethodHead {
			t.Errorf("expected HEAD, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient("test-tok").WithAPIBase(srv.URL)

	exists, err := c.RepoExists(context.Background(), "user/exists", RepoTypeModel)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !exists {
		t.Error("expected true, got false")
	}
}

func TestRepoExists_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient("tok").WithAPIBase(srv.URL)

	exists, err := c.RepoExists(context.Background(), "user/nope", RepoTypeDataset)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exists {
		t.Error("expected false, got true")
	}
}

func TestRepoExists_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := NewClient("bad-tok").WithAPIBase(srv.URL)

	exists, err := c.RepoExists(context.Background(), "user/private", RepoTypeModel)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exists {
		t.Error("expected false for unauthorized")
	}
}

func TestRepoExists_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewClient("tok").WithAPIBase(srv.URL)

	_, err := c.RepoExists(context.Background(), "user/broken", RepoTypeModel)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestRepoExists_ContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient("tok").WithAPIBase(srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	_, err := c.RepoExists(ctx, "user/repo", RepoTypeModel)
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}

func TestRepoExists_EndpointRouting(t *testing.T) {
	tests := []struct {
		repoType RepoType
		wantPath string
	}{
		{RepoTypeModel, "/models/user/repo"},
		{RepoTypeDataset, "/datasets/user/repo"},
		{RepoTypeSpace, "/spaces/user/repo"},
	}

	for _, tt := range tests {
		t.Run(string(tt.repoType), func(t *testing.T) {
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			c := NewClient("tok").WithAPIBase(srv.URL)
			c.RepoExists(context.Background(), "user/repo", tt.repoType)

			if gotPath != tt.wantPath {
				t.Errorf("expected path %q, got %q", tt.wantPath, gotPath)
			}
		})
	}
}

func TestCreateRepo_Success(t *testing.T) {
	var receivedBody map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/create" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected content-type application/json, got %q", ct)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer my-token" {
			t.Errorf("expected 'Bearer my-token', got %q", auth)
		}

		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"url": "https://huggingface.co/datasets/myorg/data",
		})
	}))
	defer srv.Close()

	c := NewClient("my-token").WithAPIBase(srv.URL)

	repo, err := c.CreateRepo(context.Background(), CreateRepoRequest{
		RepoID:  "myorg/data",
		Type:    RepoTypeDataset,
		Private: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedBody["name"] != "data" {
		t.Errorf("expected name 'data', got %v", receivedBody["name"])
	}
	if receivedBody["organization"] != "myorg" {
		t.Errorf("expected organization 'myorg', got %v", receivedBody["organization"])
	}
	if receivedBody["type"] != "dataset" {
		t.Errorf("expected type 'dataset', got %v", receivedBody["type"])
	}
	if receivedBody["private"] != true {
		t.Errorf("expected private true, got %v", receivedBody["private"])
	}

	if repo.ID != "myorg/data" {
		t.Errorf("expected ID 'myorg/data', got %q", repo.ID)
	}
	if repo.Type != RepoTypeDataset {
		t.Errorf("expected type dataset, got %q", repo.Type)
	}
	if !repo.Private {
		t.Error("expected private true")
	}
	if repo.URL != "https://huggingface.co/datasets/myorg/data" {
		t.Errorf("unexpected URL: %q", repo.URL)
	}
}

func TestCreateRepo_ModelTypeOmitsType(t *testing.T) {
	var receivedBody map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"url": "https://huggingface.co/user/model"})
	}))
	defer srv.Close()

	c := NewClient("tok").WithAPIBase(srv.URL)
	c.CreateRepo(context.Background(), CreateRepoRequest{
		RepoID: "user/model",
		Type:   RepoTypeModel,
	})

	if _, exists := receivedBody["type"]; exists {
		t.Error("model type should not include 'type' in request body")
	}
}

func TestCreateRepo_NoOrg(t *testing.T) {
	var receivedBody map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"url": "https://huggingface.co/solo-repo"})
	}))
	defer srv.Close()

	c := NewClient("tok").WithAPIBase(srv.URL)
	c.CreateRepo(context.Background(), CreateRepoRequest{
		RepoID: "solo-repo",
		Type:   RepoTypeModel,
	})

	if _, exists := receivedBody["organization"]; exists {
		t.Error("should not set organization for repo without namespace")
	}
	if receivedBody["name"] != "solo-repo" {
		t.Errorf("expected name 'solo-repo', got %v", receivedBody["name"])
	}
}

func TestCreateRepo_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"error": "You already created this dataset repo"}`))
	}))
	defer srv.Close()

	c := NewClient("tok").WithAPIBase(srv.URL)

	_, err := c.CreateRepo(context.Background(), CreateRepoRequest{
		RepoID: "user/exists",
		Type:   RepoTypeDataset,
	})
	if err == nil {
		t.Fatal("expected error for 409 response")
	}
	if got := err.Error(); got == "" {
		t.Error("error message should not be empty")
	}
}

func TestCreateRepo_NetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()

	c := NewClient("tok").WithAPIBase(srv.URL)

	_, err := c.CreateRepo(context.Background(), CreateRepoRequest{
		RepoID: "user/repo",
		Type:   RepoTypeModel,
	})
	if err == nil {
		t.Fatal("expected error for closed server")
	}
}

func TestRepoOrg(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"org/repo", "org"},
		{"user/dataset-name", "user"},
		{"solo-repo", ""},
		{"deep/nested/path", "deep"},
	}

	for _, tt := range tests {
		got := repoOrg(tt.input)
		if got != tt.want {
			t.Errorf("repoOrg(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestRepoName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"org/repo", "repo"},
		{"user/dataset-name", "dataset-name"},
		{"solo-repo", "solo-repo"},
		{"deep/nested/path", "nested/path"},
	}

	for _, tt := range tests {
		got := repoName(tt.input)
		if got != tt.want {
			t.Errorf("repoName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// --- Retry behavior tests ---

func TestDoJSON_RetriesOn503(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"error": "temporarily unavailable"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer srv.Close()

	c := NewClient("tok").WithAPIBase(srv.URL)
	c.retries = 3

	var result map[string]string
	err := c.doJSON(context.Background(), http.MethodGet, srv.URL+"/test", nil, &result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
	if result["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", result)
	}
}

func TestDoJSON_RetriesOn429(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error": "rate limited"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"done": "true"})
	}))
	defer srv.Close()

	c := NewClient("tok").WithAPIBase(srv.URL)
	c.retries = 2

	var result map[string]string
	err := c.doJSON(context.Background(), http.MethodGet, srv.URL+"/test", nil, &result)
	if err != nil {
		t.Fatalf("unexpected error after retry: %v", err)
	}
	if attempts != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts)
	}
}

func TestDoJSON_DoesNotRetryOn4xx(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error": "forbidden"}`))
	}))
	defer srv.Close()

	c := NewClient("tok").WithAPIBase(srv.URL)
	c.retries = 3

	err := c.doJSON(context.Background(), http.MethodGet, srv.URL+"/test", nil, nil)
	if err == nil {
		t.Fatal("expected error for 403")
	}
	if attempts != 1 {
		t.Errorf("should not retry on 403, got %d attempts", attempts)
	}
}

func TestDoJSON_ExhaustsRetries(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`bad gateway`))
	}))
	defer srv.Close()

	c := NewClient("tok").WithAPIBase(srv.URL)
	c.retries = 2

	err := c.doJSON(context.Background(), http.MethodGet, srv.URL+"/test", nil, nil)
	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	// Initial attempt + 2 retries = 3 total.
	if attempts != 3 {
		t.Errorf("expected 3 attempts (1 + 2 retries), got %d", attempts)
	}
}

func TestDoJSON_RespectsContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := NewClient("tok").WithAPIBase(srv.URL)
	c.retries = 10

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := c.doJSON(ctx, http.MethodGet, srv.URL+"/test", nil, nil)
	if err == nil {
		t.Fatal("expected error with canceled context")
	}
}

func TestDoJSON_SendsBodyOnRetry(t *testing.T) {
	attempts := 0
	var lastBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		lastBody, _ = io.ReadAll(r.Body)
		if attempts < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	}))
	defer srv.Close()

	c := NewClient("tok").WithAPIBase(srv.URL)
	c.retries = 2

	body := map[string]string{"key": "value"}
	err := c.doJSON(context.Background(), http.MethodPost, srv.URL+"/test", body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if attempts != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts)
	}
	// Body should have been re-sent on retry.
	if len(lastBody) == 0 {
		t.Error("expected body to be sent on retry attempt")
	}
}

func TestSetDefaultBranch(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient("tok").WithAPIBase(srv.URL)
	err := c.SetDefaultBranch(context.Background(), "user/repo", RepoTypeDataset, "master")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotMethod != http.MethodPut {
		t.Errorf("expected PUT, got %s", gotMethod)
	}
	if gotPath != "/datasets/user/repo/settings" {
		t.Errorf("expected /datasets/user/repo/settings, got %q", gotPath)
	}
	if gotBody["defaultBranch"] != "master" {
		t.Errorf("expected defaultBranch=master, got %v", gotBody["defaultBranch"])
	}
}
