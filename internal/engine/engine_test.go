package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/mendax0110/hf-sync/internal/hfapi"
)

// --- Plan Tests ---

func TestPlan_EmptySource(t *testing.T) {
	e := &Engine{}

	sourceRefs := map[string]string{}
	targetRefs := map[string]string{"refs/heads/main": "abc123"}

	opts := SyncOptions{Tags: true}
	actions := e.plan(sourceRefs, targetRefs, opts)

	if len(actions) != 0 {
		t.Errorf("expected 0 actions for empty source, got %d", len(actions))
	}
}

func TestPlan_EmptyTarget(t *testing.T) {
	e := &Engine{}

	sourceRefs := map[string]string{
		"refs/heads/main":    "abc123",
		"refs/heads/develop": "def456",
		"refs/tags/v1.0":     "ghi789",
	}
	targetRefs := map[string]string{}

	opts := SyncOptions{Tags: true}
	actions := e.plan(sourceRefs, targetRefs, opts)

	if len(actions) != 3 {
		t.Fatalf("expected 3 actions, got %d", len(actions))
	}

	for _, a := range actions {
		if a.Type != "create" {
			t.Errorf("expected type 'create', got %q for ref %s", a.Type, a.Ref)
		}
		if a.Reason != "ref does not exist on target" {
			t.Errorf("unexpected reason: %q", a.Reason)
		}
	}
}

func TestPlan_CreateNewRefs(t *testing.T) {
	e := &Engine{}

	sourceRefs := map[string]string{
		"refs/heads/main":    "aaa",
		"refs/heads/feature": "bbb",
	}
	targetRefs := map[string]string{
		"refs/heads/main": "aaa", // Already in sync.
	}

	opts := SyncOptions{Tags: true}
	actions := e.plan(sourceRefs, targetRefs, opts)

	if len(actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(actions))
	}
	if actions[0].Ref != "refs/heads/feature" {
		t.Errorf("expected refs/heads/feature, got %q", actions[0].Ref)
	}
	if actions[0].Type != "create" {
		t.Errorf("expected create, got %q", actions[0].Type)
	}
	if actions[0].NewHash != "bbb" {
		t.Errorf("expected NewHash 'bbb', got %q", actions[0].NewHash)
	}
}

func TestPlan_UpdateRefs(t *testing.T) {
	e := &Engine{}

	sourceRefs := map[string]string{
		"refs/heads/main": "new-hash",
	}
	targetRefs := map[string]string{
		"refs/heads/main": "old-hash",
	}

	opts := SyncOptions{Tags: true}
	actions := e.plan(sourceRefs, targetRefs, opts)

	if len(actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(actions))
	}
	if actions[0].Type != "update" {
		t.Errorf("expected type 'update', got %q", actions[0].Type)
	}
	if actions[0].OldHash != "old-hash" {
		t.Errorf("expected OldHash 'old-hash', got %q", actions[0].OldHash)
	}
	if actions[0].NewHash != "new-hash" {
		t.Errorf("expected NewHash 'new-hash', got %q", actions[0].NewHash)
	}
	if actions[0].Reason != "ref updated on source" {
		t.Errorf("unexpected reason: %q", actions[0].Reason)
	}
}

func TestPlan_UpdateWithForce(t *testing.T) {
	e := &Engine{}

	sourceRefs := map[string]string{
		"refs/heads/main": "rebased-hash",
	}
	targetRefs := map[string]string{
		"refs/heads/main": "old-hash",
	}

	opts := SyncOptions{Tags: true, Force: true}
	actions := e.plan(sourceRefs, targetRefs, opts)

	if len(actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(actions))
	}
	if actions[0].Reason != "force update" {
		t.Errorf("expected 'force update' reason, got %q", actions[0].Reason)
	}
}

func TestPlan_NoActionWhenInSync(t *testing.T) {
	e := &Engine{}

	refs := map[string]string{
		"refs/heads/main":    "aaa",
		"refs/heads/develop": "bbb",
		"refs/tags/v1.0":     "ccc",
	}

	opts := SyncOptions{Tags: true}
	actions := e.plan(refs, refs, opts)

	if len(actions) != 0 {
		t.Errorf("expected 0 actions when in sync, got %d", len(actions))
	}
}

func TestPlan_Prune(t *testing.T) {
	e := &Engine{}

	sourceRefs := map[string]string{
		"refs/heads/main": "abc123",
	}
	targetRefs := map[string]string{
		"refs/heads/main":  "abc123",
		"refs/heads/stale": "old-hash",
		"refs/heads/old":   "another-hash",
	}

	opts := SyncOptions{Tags: true, Prune: true}
	actions := e.plan(sourceRefs, targetRefs, opts)

	if len(actions) != 2 {
		t.Fatalf("expected 2 delete actions, got %d", len(actions))
	}

	deleteRefs := make(map[string]bool)
	for _, a := range actions {
		if a.Type != "delete" {
			t.Errorf("expected type 'delete', got %q for %s", a.Type, a.Ref)
		}
		if a.Reason != "ref not present on source" {
			t.Errorf("unexpected reason: %q", a.Reason)
		}
		deleteRefs[a.Ref] = true
	}
	if !deleteRefs["refs/heads/stale"] {
		t.Error("expected refs/heads/stale in delete actions")
	}
	if !deleteRefs["refs/heads/old"] {
		t.Error("expected refs/heads/old in delete actions")
	}
}

func TestPlan_PruneDisabledByDefault(t *testing.T) {
	e := &Engine{}

	sourceRefs := map[string]string{
		"refs/heads/main": "abc",
	}
	targetRefs := map[string]string{
		"refs/heads/main":  "abc",
		"refs/heads/extra": "def",
	}

	opts := SyncOptions{Tags: true, Prune: false}
	actions := e.plan(sourceRefs, targetRefs, opts)

	if len(actions) != 0 {
		t.Errorf("expected 0 actions with prune=false, got %d", len(actions))
	}
}

func TestPlan_PruneRespectsFilter(t *testing.T) {
	e := &Engine{}

	sourceRefs := map[string]string{
		"refs/heads/main": "abc",
	}
	targetRefs := map[string]string{
		"refs/heads/main":    "abc",
		"refs/heads/feature": "def", // Matches filter, not in source → delete.
		"refs/heads/other":   "ghi", // Does NOT match filter → skip.
	}

	opts := SyncOptions{
		Tags:     true,
		Prune:    true,
		Branches: []string{"main", "feature"},
	}
	actions := e.plan(sourceRefs, targetRefs, opts)

	if len(actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(actions))
	}
	if actions[0].Ref != "refs/heads/feature" {
		t.Errorf("expected refs/heads/feature, got %q", actions[0].Ref)
	}
}

func TestPlan_BranchFilter(t *testing.T) {
	e := &Engine{}

	sourceRefs := map[string]string{
		"refs/heads/main":    "abc123",
		"refs/heads/develop": "def456",
		"refs/heads/feature": "ghi789",
	}
	targetRefs := map[string]string{}

	opts := SyncOptions{
		Tags:     true,
		Branches: []string{"main", "develop"},
	}
	actions := e.plan(sourceRefs, targetRefs, opts)

	if len(actions) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(actions))
	}

	refs := make(map[string]bool)
	for _, a := range actions {
		refs[a.Ref] = true
	}
	if !refs["refs/heads/main"] {
		t.Error("expected refs/heads/main in actions")
	}
	if !refs["refs/heads/develop"] {
		t.Error("expected refs/heads/develop in actions")
	}
	if refs["refs/heads/feature"] {
		t.Error("did not expect refs/heads/feature in actions")
	}
}

func TestPlan_SkipTags(t *testing.T) {
	e := &Engine{}

	sourceRefs := map[string]string{
		"refs/heads/main": "abc123",
		"refs/tags/v1.0":  "def456",
		"refs/tags/v2.0":  "ghi789",
	}
	targetRefs := map[string]string{}

	opts := SyncOptions{Tags: false}
	actions := e.plan(sourceRefs, targetRefs, opts)

	if len(actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(actions))
	}
	if actions[0].Ref != "refs/heads/main" {
		t.Errorf("expected refs/heads/main, got %q", actions[0].Ref)
	}
}

func TestPlan_TagsIncludedByDefault(t *testing.T) {
	e := &Engine{}

	sourceRefs := map[string]string{
		"refs/heads/main": "aaa",
		"refs/tags/v1.0":  "bbb",
	}
	targetRefs := map[string]string{}

	opts := SyncOptions{Tags: true}
	actions := e.plan(sourceRefs, targetRefs, opts)

	if len(actions) != 2 {
		t.Fatalf("expected 2 actions (branch + tag), got %d", len(actions))
	}
}

func TestPlan_MixedOperations(t *testing.T) {
	e := &Engine{}

	sourceRefs := map[string]string{
		"refs/heads/main":    "new-main",    // Updated
		"refs/heads/feature": "new-feature", // New
		"refs/tags/v2.0":     "tag-v2",      // New tag
	}
	targetRefs := map[string]string{
		"refs/heads/main":  "old-main",   // Will be updated
		"refs/heads/stale": "stale-hash", // Will be pruned
		"refs/tags/v1.0":   "tag-v1",     // Will be pruned
	}

	opts := SyncOptions{Tags: true, Prune: true}
	actions := e.plan(sourceRefs, targetRefs, opts)

	counts := map[string]int{}
	for _, a := range actions {
		counts[a.Type]++
	}

	if counts["create"] != 2 {
		t.Errorf("expected 2 creates, got %d", counts["create"])
	}
	if counts["update"] != 1 {
		t.Errorf("expected 1 update, got %d", counts["update"])
	}
	if counts["delete"] != 2 {
		t.Errorf("expected 2 deletes, got %d", counts["delete"])
	}
}

// --- shouldSyncRef Tests ---

func TestShouldSyncRef_HEAD(t *testing.T) {
	e := &Engine{}
	opts := SyncOptions{Tags: true}

	if e.shouldSyncRef("HEAD", opts, nil) {
		t.Error("HEAD should not be synced")
	}
}

func TestShouldSyncRef_Branches(t *testing.T) {
	e := &Engine{}
	opts := SyncOptions{Tags: true}

	tests := []struct {
		ref  string
		want bool
	}{
		{"refs/heads/main", true},
		{"refs/heads/feature/xyz", true},
		{"refs/heads/release-1.0", true},
	}

	for _, tt := range tests {
		if got := e.shouldSyncRef(tt.ref, opts, nil); got != tt.want {
			t.Errorf("shouldSyncRef(%q) = %v, want %v", tt.ref, got, tt.want)
		}
	}
}

func TestShouldSyncRef_BranchFilter(t *testing.T) {
	e := &Engine{}
	opts := SyncOptions{Tags: true}
	filter := map[string]bool{
		"refs/heads/main":    true,
		"refs/heads/release": true,
	}

	tests := []struct {
		ref  string
		want bool
	}{
		{"refs/heads/main", true},
		{"refs/heads/release", true},
		{"refs/heads/develop", false},
		{"refs/heads/feature", false},
	}

	for _, tt := range tests {
		if got := e.shouldSyncRef(tt.ref, opts, filter); got != tt.want {
			t.Errorf("shouldSyncRef(%q) with filter = %v, want %v", tt.ref, got, tt.want)
		}
	}
}

func TestShouldSyncRef_Tags(t *testing.T) {
	e := &Engine{}

	if !e.shouldSyncRef("refs/tags/v1.0", SyncOptions{Tags: true}, nil) {
		t.Error("expected tags to be included when Tags=true")
	}
	if e.shouldSyncRef("refs/tags/v1.0", SyncOptions{Tags: false}, nil) {
		t.Error("expected tags to be excluded when Tags=false")
	}
}

func TestShouldSyncRef_SkipOtherRefs(t *testing.T) {
	e := &Engine{}
	opts := SyncOptions{Tags: true}

	excluded := []string{
		"refs/pull/1/head",
		"refs/pull/42/merge",
		"refs/notes/commits",
		"refs/stash",
		"refs/remotes/origin/main",
	}

	for _, ref := range excluded {
		if e.shouldSyncRef(ref, opts, nil) {
			t.Errorf("shouldSyncRef(%q) = true, want false", ref)
		}
	}
}

// --- SyncResult counting tests ---

func TestSyncResult_Counting(t *testing.T) {
	// Simulate what Sync() does when counting actions.
	actions := []RefAction{
		{Type: "create"},
		{Type: "create"},
		{Type: "update"},
		{Type: "delete"},
		{Type: "skip"},
		{Type: "skip"},
		{Type: "skip"},
	}

	result := &SyncResult{}
	for _, a := range actions {
		switch a.Type {
		case "create":
			result.Created++
		case "update":
			result.Updated++
		case "delete":
			result.Deleted++
		case "skip":
			result.Skipped++
		}
	}

	if result.Created != 2 {
		t.Errorf("Created = %d, want 2", result.Created)
	}
	if result.Updated != 1 {
		t.Errorf("Updated = %d, want 1", result.Updated)
	}
	if result.Deleted != 1 {
		t.Errorf("Deleted = %d, want 1", result.Deleted)
	}
	if result.Skipped != 3 {
		t.Errorf("Skipped = %d, want 3", result.Skipped)
	}
}

// --- Engine creation tests ---

func TestNew(t *testing.T) {
	client := hfapi.NewClient("tok")
	eng := New(client)

	if eng == nil {
		t.Fatal("expected non-nil engine")
	}
	if eng.hf != client {
		t.Error("engine should store the provided client")
	}
	if eng.retries != 3 {
		t.Errorf("expected default retries=3, got %d", eng.retries)
	}
	if eng.gitTimeout != 10*time.Minute {
		t.Errorf("expected default gitTimeout=10m, got %v", eng.gitTimeout)
	}
	if eng.progress == nil {
		t.Error("expected non-nil default progress func")
	}
	// cacheDir should be set to default (non-empty when HOME is available)
	// We just verify it doesn't panic.
	_ = eng.cacheDir
}

func TestWithCacheDir(t *testing.T) {
	client := hfapi.NewClient("tok")
	eng := New(client).WithCacheDir("/tmp/custom-cache")

	if eng.cacheDir != "/tmp/custom-cache" {
		t.Errorf("expected cacheDir='/tmp/custom-cache', got %q", eng.cacheDir)
	}
}

func TestWithCacheDir_Empty_DisablesCache(t *testing.T) {
	client := hfapi.NewClient("tok")
	eng := New(client).WithCacheDir("")

	if eng.cacheDir != "" {
		t.Errorf("expected empty cacheDir, got %q", eng.cacheDir)
	}
}

func TestMirrorPath_Deterministic(t *testing.T) {
	path1 := mirrorPath("/cache", "https://github.com/org/repo.git")
	path2 := mirrorPath("/cache", "https://github.com/org/repo.git")

	if path1 != path2 {
		t.Error("mirrorPath should be deterministic for same URL")
	}

	path3 := mirrorPath("/cache", "https://github.com/org/other-repo.git")
	if path1 == path3 {
		t.Error("mirrorPath should differ for different URLs")
	}
}

func TestMirrorPath_ContainsCacheDir(t *testing.T) {
	path := mirrorPath("/my/cache", "https://example.com/repo.git")
	if !strings.HasPrefix(path, "/my/cache/") {
		t.Errorf("expected path under /my/cache/, got %q", path)
	}
}

func TestWithProgress(t *testing.T) {
	client := hfapi.NewClient("tok")
	var called bool
	fn := func(repoID string, phase Phase, msg string) {
		called = true
	}

	eng := New(client).WithProgress(fn)
	eng.progress("test", PhaseProbe, "hello")

	if !called {
		t.Error("expected custom progress func to be called")
	}
}

func TestWithRetries(t *testing.T) {
	client := hfapi.NewClient("tok")
	eng := New(client).WithRetries(5)

	if eng.retries != 5 {
		t.Errorf("expected retries=5, got %d", eng.retries)
	}
}

func TestWithGitTimeout(t *testing.T) {
	client := hfapi.NewClient("tok")
	eng := New(client).WithGitTimeout(5 * time.Minute)

	if eng.gitTimeout != 5*time.Minute {
		t.Errorf("expected gitTimeout=5m, got %v", eng.gitTimeout)
	}
}

// --- isLFSError tests ---

func TestIsLFSError(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		{"git lfs: file too large", true},
		{"file is too large to push", true},
		{"authentication failed", false},
		{"connection refused", false},
		{"LFS objects missing", true},
	}

	for _, tt := range tests {
		err := &testError{msg: tt.msg}
		got := isLFSError(err)
		if got != tt.want {
			t.Errorf("isLFSError(%q) = %v, want %v", tt.msg, got, tt.want)
		}
	}
}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

// --- Edge case tests ---

func TestPlan_SourceHasHEADOnly(t *testing.T) {
	e := &Engine{}

	sourceRefs := map[string]string{
		"HEAD": "abc123",
	}
	targetRefs := map[string]string{}

	opts := SyncOptions{Tags: true}
	actions := e.plan(sourceRefs, targetRefs, opts)

	if len(actions) != 0 {
		t.Errorf("HEAD-only source should produce 0 actions, got %d", len(actions))
	}
}

func TestPlan_LargeRefSet(t *testing.T) {
	e := &Engine{}

	sourceRefs := make(map[string]string)
	for i := 0; i < 100; i++ {
		ref := "refs/heads/branch-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		sourceRefs[ref] = "hash-" + ref
	}
	targetRefs := map[string]string{}

	opts := SyncOptions{Tags: true}
	actions := e.plan(sourceRefs, targetRefs, opts)

	if len(actions) != 100 {
		t.Errorf("expected 100 create actions, got %d", len(actions))
	}

	for _, a := range actions {
		if a.Type != "create" {
			t.Errorf("expected all creates, got %q for %s", a.Type, a.Ref)
		}
	}
}

func TestPlan_BothEmpty(t *testing.T) {
	e := &Engine{}

	actions := e.plan(map[string]string{}, map[string]string{}, SyncOptions{Tags: true})

	if len(actions) != 0 {
		t.Errorf("expected 0 actions for both empty, got %d", len(actions))
	}
}

func TestPlan_PruneWithBothEmpty(t *testing.T) {
	e := &Engine{}

	opts := SyncOptions{Tags: true, Prune: true}
	actions := e.plan(map[string]string{}, map[string]string{}, opts)

	if len(actions) != 0 {
		t.Errorf("expected 0 actions, got %d", len(actions))
	}
}

func TestPlan_TagUpdate(t *testing.T) {
	e := &Engine{}

	sourceRefs := map[string]string{
		"refs/tags/v1.0": "new-tag-hash",
	}
	targetRefs := map[string]string{
		"refs/tags/v1.0": "old-tag-hash",
	}

	// Without force, should still produce an update action.
	opts := SyncOptions{Tags: true}
	actions := e.plan(sourceRefs, targetRefs, opts)

	if len(actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(actions))
	}
	if actions[0].Type != "update" {
		t.Errorf("expected 'update', got %q", actions[0].Type)
	}
}
