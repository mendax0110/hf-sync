// Package hfapi provides a client for the HuggingFace Hub HTTP API.
//
// This client handles repository CRUD operations, authentication, and
// git endpoint URL construction. It does NOT handle git protocol operations
// directly — those are handled by the sync engine using the native git binary.
//
// Authentication uses Bearer tokens passed via the HF_TOKEN environment
// variable or --hf-token flag.
//
// API reference: https://huggingface.co/docs/hub/api
package hfapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// DefaultAPIBase is the HuggingFace Hub API endpoint.
	DefaultAPIBase = "https://huggingface.co/api"

	// DefaultHubBase is the HuggingFace Hub base URL for git operations.
	DefaultHubBase = "https://huggingface.co"
)

// RepoType represents the type of HuggingFace repository.
type RepoType string

const (
	RepoTypeModel   RepoType = "model"
	RepoTypeDataset RepoType = "dataset"
	RepoTypeSpace   RepoType = "space"
)

// Client is the HuggingFace Hub API client.
type Client struct {
	token      string
	apiBase    string
	hubBase    string
	httpClient *http.Client
	retries    int
}

// NewClient creates a new HuggingFace API client with the given token.
func NewClient(token string) *Client {
	return &Client{
		token:   token,
		apiBase: DefaultAPIBase,
		hubBase: DefaultHubBase,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		retries: 3,
	}
}

// WithAPIBase sets a custom API base URL (useful for testing).
func (c *Client) WithAPIBase(base string) *Client {
	c.apiBase = strings.TrimRight(base, "/")
	return c
}

// WithHubBase sets a custom Hub base URL.
func (c *Client) WithHubBase(base string) *Client {
	c.hubBase = strings.TrimRight(base, "/")
	return c
}

// CreateRepoRequest contains parameters for creating a repository.
type CreateRepoRequest struct {
	RepoID  string   // e.g. "username/repo-name"
	Type    RepoType // model, dataset, or space
	Private bool
}

// RepoInfo contains information about a HuggingFace repository.
type RepoInfo struct {
	ID      string   `json:"id"`
	URL     string   `json:"url"`
	GitURL  string   `json:"git_url"`
	Type    RepoType `json:"type"`
	Private bool     `json:"private"`
}

// CreateRepo creates a new repository on HuggingFace Hub.
// Returns the repo info if successful, or an error if creation fails.
func (c *Client) CreateRepo(ctx context.Context, req CreateRepoRequest) (*RepoInfo, error) {
	body := map[string]interface{}{
		"name":    repoName(req.RepoID),
		"private": req.Private,
	}

	// Set organization if the repo ID contains a namespace.
	if org := repoOrg(req.RepoID); org != "" {
		body["organization"] = org
	}

	// Set repo type in the request body.
	if req.Type != "" && req.Type != RepoTypeModel {
		body["type"] = string(req.Type)
	}

	endpoint := fmt.Sprintf("%s/repos/create", c.apiBase)

	var result struct {
		URL string `json:"url"`
	}
	if err := c.doJSON(ctx, http.MethodPost, endpoint, body, &result); err != nil {
		return nil, err
	}

	gitURL := c.GitURL(req.RepoID, req.Type)

	return &RepoInfo{
		ID:      req.RepoID,
		URL:     result.URL,
		GitURL:  gitURL,
		Type:    req.Type,
		Private: req.Private,
	}, nil
}

// RepoExists checks if a repository exists on HuggingFace Hub.
func (c *Client) RepoExists(ctx context.Context, repoID string, repoType RepoType) (bool, error) {
	endpoint := c.repoAPIEndpoint(repoID, repoType)

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, endpoint, nil)
	if err != nil {
		return false, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("checking repo existence: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound, http.StatusUnauthorized:
		return false, nil
	default:
		return false, fmt.Errorf("unexpected status %d checking repo %s", resp.StatusCode, repoID)
	}
}

// GitURL returns the git clone URL for a HuggingFace repository.
// Format depends on repo type:
//   - model:   https://huggingface.co/{repo_id}
//   - dataset: https://huggingface.co/datasets/{repo_id}
//   - space:   https://huggingface.co/spaces/{repo_id}
func (c *Client) GitURL(repoID string, repoType RepoType) string {
	switch repoType {
	case RepoTypeDataset:
		return fmt.Sprintf("%s/datasets/%s", c.hubBase, repoID)
	case RepoTypeSpace:
		return fmt.Sprintf("%s/spaces/%s", c.hubBase, repoID)
	default:
		return fmt.Sprintf("%s/%s", c.hubBase, repoID)
	}
}

// Token returns the client's auth token (used by sync engine for git auth).
func (c *Client) Token() string {
	return c.token
}

// repoAPIEndpoint returns the API endpoint for a specific repository.
func (c *Client) repoAPIEndpoint(repoID string, repoType RepoType) string {
	switch repoType {
	case RepoTypeDataset:
		return fmt.Sprintf("%s/datasets/%s", c.apiBase, repoID)
	case RepoTypeSpace:
		return fmt.Sprintf("%s/spaces/%s", c.apiBase, repoID)
	default:
		return fmt.Sprintf("%s/models/%s", c.apiBase, repoID)
	}
}

// doJSON performs an authenticated JSON request with retry on transient failures.
func (c *Client) doJSON(ctx context.Context, method, url string, body interface{}, result interface{}) error {
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshaling request body: %w", err)
		}
	}

	var lastErr error
	for attempt := 0; attempt <= c.retries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		var reqBody io.Reader
		if bodyBytes != nil {
			reqBody = bytes.NewReader(bodyBytes)
		}

		req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
		if err != nil {
			return fmt.Errorf("creating request: %w", err)
		}

		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("executing request: %w", err)
			continue // network error, retry
		}

		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
			continue // transient server error, retry
		}

		defer resp.Body.Close()

		if resp.StatusCode >= 400 {
			respBody, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
		}

		if result != nil {
			if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
				return fmt.Errorf("decoding response: %w", err)
			}
		}

		return nil
	}
	return lastErr
}

// SetDefaultBranch changes the default branch of a HuggingFace repository.
// This is needed before deleting the current default branch (e.g. HF auto-created
// "main" when the source repo uses "master").
func (c *Client) SetDefaultBranch(ctx context.Context, repoID string, repoType RepoType, branch string) error {
	endpoint := fmt.Sprintf("%s/settings", c.repoAPIEndpoint(repoID, repoType))
	body := map[string]interface{}{
		"defaultBranch": branch,
	}
	return c.doJSON(ctx, http.MethodPut, endpoint, body, nil)
}

// DeleteRepo permanently deletes a repository from HuggingFace Hub.
// This is irreversible.
func (c *Client) DeleteRepo(ctx context.Context, repoID string, repoType RepoType) error {
	endpoint := fmt.Sprintf("%s/repos/delete", c.apiBase)
	body := map[string]interface{}{
		"name": repoName(repoID),
	}
	if org := repoOrg(repoID); org != "" {
		body["organization"] = org
	}
	if repoType != "" && repoType != RepoTypeModel {
		body["type"] = string(repoType)
	}
	return c.doJSON(ctx, http.MethodDelete, endpoint, body, nil)
}

// repoOrg extracts the organization/username from a repo ID like "org/name".
func repoOrg(repoID string) string {
	parts := strings.SplitN(repoID, "/", 2)
	if len(parts) == 2 {
		return parts[0]
	}
	return ""
}

// repoName extracts the repository name from a repo ID like "org/name".
func repoName(repoID string) string {
	parts := strings.SplitN(repoID, "/", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return repoID
}
