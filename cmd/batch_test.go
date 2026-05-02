package cmd

import (
	"testing"

	"github.com/mendax0110/hf-sync/internal/hfapi"
)

func TestBuildSyncOpts_DefaultsOnly(t *testing.T) {
	defaults := batchDefaults{
		RepoType: "dataset",
		Private:  false,
		Tags:     true,
		Force:    false,
		Prune:    false,
	}

	repo := repoConfig{
		Source: "https://github.com/org/repo.git",
		Target: "user/repo",
	}

	opts := buildSyncOpts(repo, defaults, "src-token", false)

	if opts.SourceURL != "https://github.com/org/repo.git" {
		t.Errorf("SourceURL = %q", opts.SourceURL)
	}
	if opts.SourceToken != "src-token" {
		t.Errorf("SourceToken = %q", opts.SourceToken)
	}
	if opts.RepoID != "user/repo" {
		t.Errorf("RepoID = %q", opts.RepoID)
	}
	if opts.RepoType != hfapi.RepoType("dataset") {
		t.Errorf("RepoType = %q", opts.RepoType)
	}
	if opts.Private != false {
		t.Error("expected Private=false")
	}
	if opts.Tags != true {
		t.Error("expected Tags=true")
	}
	if opts.Force != false {
		t.Error("expected Force=false")
	}
	if opts.Prune != false {
		t.Error("expected Prune=false")
	}
	if opts.DryRun != false {
		t.Error("expected DryRun=false")
	}
	if !opts.CreateRepo {
		t.Error("expected CreateRepo=true")
	}
}

func TestBuildSyncOpts_RepoOverridesDefaults(t *testing.T) {
	defaults := batchDefaults{
		RepoType: "dataset",
		Private:  false,
		Tags:     true,
		Force:    false,
		Prune:    false,
	}

	trueVal := true
	falseVal := false

	repo := repoConfig{
		Source:   "https://github.com/org/model.git",
		Target:   "user/model",
		RepoType: "model",
		Private:  &trueVal,
		Tags:     &falseVal,
		Force:    &trueVal,
		Prune:    &trueVal,
		Branches: []string{"main", "release"},
	}

	opts := buildSyncOpts(repo, defaults, "", false)

	if opts.RepoType != hfapi.RepoType("model") {
		t.Errorf("RepoType = %q, want 'model'", opts.RepoType)
	}
	if !opts.Private {
		t.Error("expected Private=true (overridden)")
	}
	if opts.Tags {
		t.Error("expected Tags=false (overridden)")
	}
	if !opts.Force {
		t.Error("expected Force=true (overridden)")
	}
	if !opts.Prune {
		t.Error("expected Prune=true (overridden)")
	}
	if len(opts.Branches) != 2 {
		t.Errorf("expected 2 branches, got %d", len(opts.Branches))
	}
	if opts.Branches[0] != "main" || opts.Branches[1] != "release" {
		t.Errorf("branches = %v", opts.Branches)
	}
}

func TestBuildSyncOpts_DryRun(t *testing.T) {
	defaults := batchDefaults{RepoType: "dataset", Tags: true}
	repo := repoConfig{
		Source: "https://example.com/repo.git",
		Target: "user/repo",
	}

	opts := buildSyncOpts(repo, defaults, "", true)

	if !opts.DryRun {
		t.Error("expected DryRun=true when passed")
	}
}

func TestBuildSyncOpts_PartialOverrides(t *testing.T) {
	defaults := batchDefaults{
		RepoType: "dataset",
		Private:  true,
		Tags:     true,
		Force:    true,
		Prune:    true,
	}

	// Only override Private, leave others as defaults.
	falseVal := false
	repo := repoConfig{
		Source:  "https://example.com/repo.git",
		Target:  "user/repo",
		Private: &falseVal,
	}

	opts := buildSyncOpts(repo, defaults, "", false)

	if opts.Private {
		t.Error("expected Private=false (overridden)")
	}
	// These should still be from defaults.
	if !opts.Tags {
		t.Error("expected Tags=true (from default)")
	}
	if !opts.Force {
		t.Error("expected Force=true (from default)")
	}
	if !opts.Prune {
		t.Error("expected Prune=true (from default)")
	}
}

func TestBuildSyncOpts_EmptyBranches(t *testing.T) {
	defaults := batchDefaults{RepoType: "model", Tags: true}
	repo := repoConfig{
		Source: "https://example.com/repo.git",
		Target: "user/repo",
	}

	opts := buildSyncOpts(repo, defaults, "", false)

	if opts.Branches != nil {
		t.Errorf("expected nil Branches, got %v", opts.Branches)
	}
}
