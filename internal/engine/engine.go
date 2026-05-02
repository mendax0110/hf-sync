// Package engine implements the core sync logic for hf-sync.
//
// The engine follows a plan-then-execute model:
//  1. Probe: discover refs on both source and target remotes (concurrently)
//  2. Plan: compute the set of ref actions (create, update, delete, skip)
//  3. Execute: perform the git push to the HuggingFace target via native git binary
//
// The native git binary is used for the push step (instead of go-git) because
// go-git has no Git LFS support. HuggingFace's pre-receive hook rejects pushes
// of raw blobs above its size threshold — those files must arrive as LFS pointers.
// Shelling out to git+git-lfs handles this transparently.
package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/mendax0110/hf-sync/internal/hfapi"
	"golang.org/x/sync/errgroup"
)

// cacheLocks prevents concurrent clone/update operations on the same mirror path.
// Multiple repos sharing the same source URL would otherwise race.
var cacheLocks sync.Map // map[string]*sync.Mutex

// lfsBinaryPatterns is a comma-separated list of binary file extensions that
// HuggingFace requires to be stored via LFS (or Xet). Without this, HF's
// pre-receive hook rejects pushes containing these files as raw blobs,
// regardless of file size.
const lfsBinaryPatterns = "*.xlsx,*.xls,*.pdf,*.png,*.jpg,*.jpeg,*.gif,*.bmp,*.tiff,*.ico,*.psd," +
	"*.doc,*.docx,*.ppt,*.pptx,*.odt,*.ods,*.odp," +
	"*.zip,*.tar,*.gz,*.bz2,*.7z,*.rar,*.xz,*.zst," +
	"*.bin,*.exe,*.dll,*.so,*.dylib,*.a,*.lib,*.o,*.obj," +
	"*.jar,*.war,*.class,*.pyc," +
	"*.mp3,*.mp4,*.avi,*.mov,*.wav,*.flac,*.ogg,*.mkv,*.webm," +
	"*.ttf,*.otf,*.woff,*.woff2,*.eot," +
	"*.RData,*.rds,*.dta,*.sav,*.sas7bdat,*.feather,*.parquet,*.arrow," +
	"*.h5,*.hdf5,*.nc,*.npy,*.npz,*.pkl,*.pickle," +
	"*.sqlite,*.db,*.mdb,*.accdb," +
	"*.svg,*.eps,*.ai,*.wmf,*.emf"

// Engine orchestrates the sync between a source git remote and HuggingFace.
type Engine struct {
	hf         *hfapi.Client
	progress   ProgressFunc
	retries    int           // max retry attempts for transient failures
	gitTimeout time.Duration // timeout per git command (0 = no limit)
	cacheDir   string        // persistent mirror cache directory (empty = use temp)
}

// New creates a new sync engine with the given HuggingFace client.
func New(hf *hfapi.Client) *Engine {
	return &Engine{
		hf:         hf,
		progress:   NopProgress,
		retries:    3,
		gitTimeout: 10 * time.Minute,
		cacheDir:   defaultCacheDir(),
	}
}

// WithProgress sets a progress callback for status updates during sync.
func (e *Engine) WithProgress(fn ProgressFunc) *Engine {
	e.progress = fn
	return e
}

// WithRetries sets the maximum number of retry attempts for transient failures.
func (e *Engine) WithRetries(n int) *Engine {
	e.retries = n
	return e
}

// WithGitTimeout sets the timeout for individual git commands.
func (e *Engine) WithGitTimeout(d time.Duration) *Engine {
	e.gitTimeout = d
	return e
}

// WithCacheDir sets a custom cache directory for mirror clones.
// Pass empty string to disable caching (use temp dirs).
func (e *Engine) WithCacheDir(dir string) *Engine {
	e.cacheDir = dir
	return e
}

// defaultCacheDir returns the platform-appropriate cache directory.
func defaultCacheDir() string {
	if dir := os.Getenv("HF_SYNC_CACHE_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cache", "hf-sync", "mirrors")
}

// mirrorPath returns the on-disk path for a cached mirror of the given URL.
func mirrorPath(cacheDir, repoURL string) string {
	h := sha256.Sum256([]byte(repoURL))
	return filepath.Join(cacheDir, hex.EncodeToString(h[:12]))
}

// SyncOptions configures a sync operation.
type SyncOptions struct {
	SourceURL   string         // Git clone URL for the source repository
	SourceToken string         // Auth token for the source remote (optional)
	RepoID      string         // HuggingFace repo ID (e.g. "user/dataset-name")
	RepoType    hfapi.RepoType // model, dataset, or space
	Private     bool           // Create as private if repo doesn't exist
	DryRun      bool           // Plan only, don't execute
	Branches    []string       // Filter: only sync these branches (nil = all)
	Tags        bool           // Whether to sync tags
	Force       bool           // Allow non-fast-forward updates
	Prune       bool           // Delete refs on target not present on source
	CreateRepo  bool           // Auto-create HF repo if missing
}

// SyncResult contains the outcome of a sync operation.
type SyncResult struct {
	Source  string      `json:"source"`
	Target  string      `json:"target"`
	Actions []RefAction `json:"actions"`
	Created int         `json:"created"`
	Updated int         `json:"updated"`
	Deleted int         `json:"deleted"`
	Skipped int         `json:"skipped"`
	DryRun  bool        `json:"dry_run"`
}

// RefAction describes a single ref operation planned or executed.
type RefAction struct {
	Ref     string `json:"ref"`
	Type    string `json:"type"`   // "create", "update", "delete", "skip"
	Reason  string `json:"reason"` // human-readable explanation
	OldHash string `json:"old_hash,omitempty"`
	NewHash string `json:"new_hash,omitempty"`
}

// Sync performs the full sync pipeline: probe → plan → execute.
func (e *Engine) Sync(ctx context.Context, opts SyncOptions) (*SyncResult, error) {
	targetURL := e.hf.GitURL(opts.RepoID, opts.RepoType)

	result := &SyncResult{
		Source: opts.SourceURL,
		Target: targetURL,
		DryRun: opts.DryRun,
	}

	// Step 1: Ensure target repository exists.
	// Track whether we just created it — HuggingFace auto-commits a .gitattributes
	// file to "main" on creation, which we need to clean up if the source uses a
	// different default branch (e.g. "master").
	var freshlyCreated bool
	if opts.CreateRepo && !opts.DryRun {
		var err error
		freshlyCreated, err = e.ensureRepo(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("ensuring target repo: %w", err)
		}
	}

	// Step 2: Probe source and target refs concurrently.
	var sourceRefs, targetRefs map[string]string

	e.progress(opts.RepoID, PhaseProbe, "discovering refs on source and target")

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		sourceRefs, err = e.probeRefs(gctx, opts.SourceURL, opts.SourceToken)
		return err
	})
	g.Go(func() error {
		var err error
		targetRefs, err = e.probeRefs(gctx, targetURL, e.hf.Token())
		if err != nil {
			// Target may be brand-new or empty — that is fine.
			targetRefs = make(map[string]string)
		}
		return nil
	})
	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("probing source: %w", err)
	}

	// Step 3: Plan actions from source vs target diff.
	e.progress(opts.RepoID, PhasePlan, fmt.Sprintf("planning actions for %d source refs, %d target refs", len(sourceRefs), len(targetRefs)))
	actions := e.plan(sourceRefs, targetRefs, opts)

	// Step 4: If we just created the HF repo, delete any HF auto-init refs
	// (typically "refs/heads/main" with just a .gitattributes commit) that
	// do not exist on the source. Without this the HF UI shows a stale "main"
	// branch alongside the real source branches.
	if freshlyCreated {
		for ref := range targetRefs {
			if ref == "HEAD" {
				continue
			}
			if _, inSource := sourceRefs[ref]; !inSource {
				actions = append(actions, RefAction{
					Ref:    ref,
					Type:   "delete",
					Reason: "HF auto-init ref not present in source",
				})
			}
		}
	}

	result.Actions = actions

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

	// Step 5: Execute (unless dry-run).
	if !opts.DryRun && (result.Created > 0 || result.Updated > 0 || result.Deleted > 0) {
		e.progress(opts.RepoID, PhaseClone, "starting mirror clone and push")
		if err := e.execute(ctx, opts, targetURL, actions); err != nil {
			return nil, fmt.Errorf("executing sync: %w", err)
		}
	}

	return result, nil
}

// ensureRepo creates the HuggingFace repository if it does not exist.
// Returns (true, nil) when a new repo was just created so the caller can
// schedule cleanup of HF's auto-init commit.
func (e *Engine) ensureRepo(ctx context.Context, opts SyncOptions) (bool, error) {
	exists, err := e.hf.RepoExists(ctx, opts.RepoID, opts.RepoType)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}

	_, err = e.hf.CreateRepo(ctx, hfapi.CreateRepoRequest{
		RepoID:  opts.RepoID,
		Type:    opts.RepoType,
		Private: opts.Private,
	})
	return err == nil, err
}

// probeRefs discovers refs on a remote and returns a map of ref name → hash.
// go-git is used here only for the lightweight ls-remote — no object transfer.
func (e *Engine) probeRefs(ctx context.Context, rawURL, token string) (map[string]string, error) {
	remote := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{
		Name: "probe",
		URLs: []string{rawURL},
	})

	listOpts := &git.ListOptions{}
	if token != "" {
		listOpts.Auth = &http.BasicAuth{
			Username: "x-token-auth",
			Password: token,
		}
	}

	refs, err := remote.ListContext(ctx, listOpts)
	if err != nil {
		return nil, fmt.Errorf("listing refs at %s: %w", rawURL, err)
	}

	result := make(map[string]string, len(refs))
	for _, ref := range refs {
		result[ref.Name().String()] = ref.Hash().String()
	}
	return result, nil
}

// plan computes the set of ref actions needed to bring the target in sync with source.
func (e *Engine) plan(sourceRefs, targetRefs map[string]string, opts SyncOptions) []RefAction {
	var actions []RefAction

	branchFilter := make(map[string]bool)
	for _, b := range opts.Branches {
		branchFilter["refs/heads/"+b] = true
	}

	for ref, srcHash := range sourceRefs {
		if !e.shouldSyncRef(ref, opts, branchFilter) {
			continue
		}

		tgtHash, exists := targetRefs[ref]
		if !exists {
			actions = append(actions, RefAction{
				Ref:     ref,
				Type:    "create",
				Reason:  "ref does not exist on target",
				NewHash: srcHash,
			})
		} else if srcHash != tgtHash {
			reason := "ref updated on source"
			if opts.Force {
				reason = "force update"
			}
			actions = append(actions, RefAction{
				Ref:     ref,
				Type:    "update",
				Reason:  reason,
				OldHash: tgtHash,
				NewHash: srcHash,
			})
		}
	}

	if opts.Prune {
		for ref := range targetRefs {
			if !e.shouldSyncRef(ref, opts, branchFilter) {
				continue
			}
			if _, exists := sourceRefs[ref]; !exists {
				actions = append(actions, RefAction{
					Ref:    ref,
					Type:   "delete",
					Reason: "ref not present on source",
				})
			}
		}
	}

	return actions
}

// shouldSyncRef returns true if the given ref should be included in the sync.
func (e *Engine) shouldSyncRef(ref string, opts SyncOptions, branchFilter map[string]bool) bool {
	if ref == "HEAD" {
		return false
	}
	if strings.HasPrefix(ref, "refs/heads/") {
		if len(branchFilter) > 0 {
			return branchFilter[ref]
		}
		return true
	}
	if strings.HasPrefix(ref, "refs/tags/") {
		return opts.Tags
	}
	return false
}

// execute performs the actual git push to the HuggingFace target using the
// native git binary. This replaces the previous go-git in-memory push because:
//
//  1. go-git has no Git LFS support. HuggingFace's pre-receive hook rejects
//     raw blobs above ~10 MB with "pre-receive hook declined"; those objects
//     must be uploaded as LFS pointers with the content stored in LFS storage.
//
//  2. The native git client + git-lfs filter handles this automatically during
//     clone and push, so no special-casing is needed per repository.
func (e *Engine) execute(ctx context.Context, opts SyncOptions, targetURL string, actions []RefAction) error {
	sourceURL := injectToken(opts.SourceURL, opts.SourceToken)
	authedTarget := injectToken(targetURL, e.hf.Token())

	// Obtain a mirror working directory — either from cache or a fresh temp clone.
	mirrorDir, cleanup, _, err := e.obtainMirror(ctx, opts, sourceURL)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}

	if hasLFS() {
		e.progress(opts.RepoID, PhaseLFS, "fetching LFS objects from source")
		e.runGitRetry(ctx, mirrorDir, "lfs", "fetch", "--all", sourceURL) //nolint:errcheck

		// Only run full LFS migrate on first clone. For incremental updates,
		// the existing refs are already migrated — only new content arrives.
		lfsMigrated := filepath.Join(mirrorDir, ".hf-sync-lfs-migrated")
		markerContent, markerErr := os.ReadFile(lfsMigrated)
		needsBinaryMigration := markerErr != nil || string(markerContent) < "2"

		if markerErr != nil {
			// First time — migrate everything.
			e.progress(opts.RepoID, PhaseLFS, "migrating large files to LFS pointers (first time)")
			if out, err := e.runGitRetry(ctx, mirrorDir, "lfs", "migrate", "import", "--everything", "--above=10mb"); err != nil {
				_ = out
			}
		}

		if needsBinaryMigration {
			// Migrate known binary file types regardless of size.
			// HuggingFace rejects ALL binary files that aren't LFS pointers.
			e.progress(opts.RepoID, PhaseLFS, "migrating binary file types to LFS pointers")
			if out, err := e.runGitRetry(ctx, mirrorDir, "lfs", "migrate", "import", "--everything", "--include="+lfsBinaryPatterns); err != nil {
				_ = out
			} else {
				if err := os.WriteFile(lfsMigrated, []byte("2"), 0o644); err != nil {
					return fmt.Errorf("write LFS migration marker: %w", err)
				}
			}
		} else {
			// Incremental update on cached mirror — only migrate refs that changed.
			var changedRefs []string
			for _, a := range actions {
				if a.Type == "create" || a.Type == "update" {
					changedRefs = append(changedRefs, a.Ref)
				}
			}
			if len(changedRefs) > 0 {
				e.progress(opts.RepoID, PhaseLFS, fmt.Sprintf("migrating LFS for %d updated refs", len(changedRefs)))
				includeRef := "--include-ref=" + strings.Join(changedRefs, ",")
				migrateArgs := []string{"lfs", "migrate", "import", "--above=10mb", includeRef}
				if out, err := e.runGitRetry(ctx, mirrorDir, migrateArgs...); err != nil {
					_ = out
				}
				// Also migrate binary file types for changed refs.
				migrateArgs = []string{"lfs", "migrate", "import", "--include=" + lfsBinaryPatterns, includeRef}
				e.runGitRetry(ctx, mirrorDir, migrateArgs...) //nolint:errcheck
			}
		}
	}

	// Build refspecs from planned actions.
	var createUpdateSpecs, deleteSpecs []string
	for _, action := range actions {
		switch action.Type {
		case "create", "update":
			prefix := ""
			if opts.Force {
				prefix = "+"
			}
			createUpdateSpecs = append(createUpdateSpecs, prefix+action.Ref+":"+action.Ref)
		case "delete":
			deleteSpecs = append(deleteSpecs, ":"+action.Ref)
		}
	}
	if len(createUpdateSpecs) == 0 && len(deleteSpecs) == 0 {
		return nil
	}

	// Upload LFS objects to target BEFORE pushing refs.
	// In a bare/mirror repo, git's pre-push hook doesn't fire, so LFS objects
	// must be uploaded explicitly. HuggingFace's pre-receive hook rejects pushes
	// that reference LFS objects not yet in their store.
	if hasLFS() {
		e.progress(opts.RepoID, PhaseLFS, "pushing LFS objects to target")
		e.runGitRetry(ctx, mirrorDir, "lfs", "push", "--all", authedTarget) //nolint:errcheck
	}

	// Push create/update refs first so branches exist before we try to delete others.
	if len(createUpdateSpecs) > 0 {
		e.progress(opts.RepoID, PhasePush, fmt.Sprintf("pushing %d refs to target", len(createUpdateSpecs)))
		pushArgs := append([]string{"push", authedTarget}, createUpdateSpecs...)
		if out, err := e.runGitRetry(ctx, mirrorDir, pushArgs...); err != nil {
			outStr := scrubToken(string(out), e.hf.Token())
			if isLFSRejection(outStr) && !hasLFS() {
				return fmt.Errorf("pushing to target: %w\n%s\n\nhf-sync: git-lfs is not installed. Install it to auto-convert large files:\n  apt-get install git-lfs && git lfs install", err, outStr)
			}
			return fmt.Errorf("pushing to target: %w\n%s", err, outStr)
		}
	}

	// After pushing content, ensure HF default branch points to a real source branch.
	// HuggingFace auto-creates "main" on repo creation, but if the source uses
	// "master" (or another branch), the HF UI shows the empty "main" by default.
	if len(createUpdateSpecs) > 0 {
		for _, action := range actions {
			if (action.Type == "create" || action.Type == "update") && strings.HasPrefix(action.Ref, "refs/heads/") {
				branch := strings.TrimPrefix(action.Ref, "refs/heads/")
				_ = e.hf.SetDefaultBranch(ctx, opts.RepoID, opts.RepoType, branch)
				break
			}
		}
	}

	// If we're deleting refs/heads/main (HF's default branch), try to remove it.
	if len(deleteSpecs) > 0 {
		// Filter out main if we just set the default to another branch —
		// HF may still reject the delete but that's non-fatal now.
		e.progress(opts.RepoID, PhasePush, fmt.Sprintf("deleting %d refs from target", len(deleteSpecs)))
		pushArgs := append([]string{"push", authedTarget}, deleteSpecs...)
		if out, err := e.runGitRetry(ctx, mirrorDir, pushArgs...); err != nil {
			outStr := string(out)
			if strings.Contains(outStr, "deletion of the current branch prohibited") {
				// HuggingFace's git server didn't propagate the default branch
				// change in time. The actual content sync succeeded — treat this
				// as non-fatal. The stale auto-init branch will remain.
				e.progress(opts.RepoID, PhasePush, "could not delete HF auto-init branch (non-fatal, content synced OK)")
			} else {
				return fmt.Errorf("pushing to target: %w\n%s", err, scrubToken(outStr, e.hf.Token()))
			}
		}
	}

	e.progress(opts.RepoID, PhaseCleanup, "done")
	return nil
}

// obtainMirror returns a mirror directory ready for push. It uses a persistent
// cache when configured: first call does a full clone, subsequent calls do an
// incremental `git remote update` which fetches only new objects.
// Returns (dir, cleanupFunc, isCached, error). cleanupFunc is non-nil only for temp dirs.
func (e *Engine) obtainMirror(ctx context.Context, opts SyncOptions, sourceURL string) (string, func(), bool, error) {
	if e.cacheDir == "" {
		// No cache — fall back to temp dir with full clone.
		dir, cleanup, err := e.freshMirror(ctx, opts, sourceURL)
		return dir, cleanup, false, err
	}

	if err := os.MkdirAll(e.cacheDir, 0o755); err != nil {
		// Can't create cache dir — fall back to temp.
		dir, cleanup, err := e.freshMirror(ctx, opts, sourceURL)
		return dir, cleanup, false, err
	}

	dir := mirrorPath(e.cacheDir, opts.SourceURL)

	// Acquire a per-path lock so multiple repos sharing the same source URL
	// don't race into the same cache directory concurrently.
	lockVal, _ := cacheLocks.LoadOrStore(dir, &sync.Mutex{})
	mu := lockVal.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	// Check if we have an existing cached mirror.
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err == nil {
		// Existing mirror — just update it (fetches only delta).
		e.progress(opts.RepoID, PhaseClone, "updating cached mirror (incremental fetch)")
		if out, err := e.runGitRetry(ctx, dir, "remote", "update", "--prune"); err != nil {
			// If update fails (e.g. corrupted cache), nuke and re-clone.
			e.progress(opts.RepoID, PhaseClone, "cache corrupted, re-cloning")
			os.RemoveAll(dir)
			dir2, _, err := e.cachedClone(ctx, opts, sourceURL, dir)
			return dir2, nil, false, err
		} else {
			_ = out
		}
		return dir, nil, true, nil
	}

	// No existing cache — do initial clone into cache dir.
	dir2, _, err := e.cachedClone(ctx, opts, sourceURL, dir)
	return dir2, nil, false, err
}

func (e *Engine) cachedClone(ctx context.Context, opts SyncOptions, sourceURL, dir string) (string, func(), error) {
	e.progress(opts.RepoID, PhaseClone, "cloning "+opts.SourceURL+" (first time, will be cached)")
	if out, err := e.runGitRetry(ctx, "", "clone", "--mirror", sourceURL, dir); err != nil {
		os.RemoveAll(dir) // Clean up partial clone.
		return "", nil, fmt.Errorf("git clone --mirror: %w\n%s", err, scrubToken(string(out), opts.SourceToken))
	}
	return dir, nil, nil
}

func (e *Engine) freshMirror(ctx context.Context, opts SyncOptions, sourceURL string) (string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "hf-sync-*")
	if err != nil {
		return "", nil, fmt.Errorf("creating temp dir: %w", err)
	}
	cleanup := func() { os.RemoveAll(tmpDir) }

	e.progress(opts.RepoID, PhaseClone, "cloning "+opts.SourceURL)
	if out, err := e.runGitRetry(ctx, "", "clone", "--mirror", sourceURL, tmpDir); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("git clone --mirror: %w\n%s", err, scrubToken(string(out), opts.SourceToken))
	}
	return tmpDir, cleanup, nil
}

// runGit executes git with the given arguments. If dir is non-empty the command
// runs inside that directory. Returns combined stdout+stderr and any exit error.
func runGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	return cmd.CombinedOutput()
}

// runGitRetry executes a git command with timeout and retry on transient failures.
// Retries use exponential backoff (1s, 2s, 4s, ...).
func (e *Engine) runGitRetry(ctx context.Context, dir string, args ...string) ([]byte, error) {
	var out []byte
	var err error

	for attempt := 0; attempt <= e.retries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				return out, ctx.Err()
			case <-time.After(backoff):
			}
		}

		var cmdCtx context.Context
		var cancel context.CancelFunc
		if e.gitTimeout > 0 {
			cmdCtx, cancel = context.WithTimeout(ctx, e.gitTimeout)
		} else {
			cmdCtx, cancel = context.WithCancel(ctx)
		}

		out, err = runGit(cmdCtx, dir, args...)
		cancel()

		if err == nil {
			return out, nil
		}

		// Only retry on transient errors (network, timeout). Don't retry on
		// permission errors, pre-receive hook rejections, or other permanent failures.
		if !isTransientGitError(string(out), err) {
			return out, err
		}
	}
	return out, err
}

// isTransientGitError returns true if the git error looks like a temporary
// network issue that might succeed on retry.
func isTransientGitError(output string, err error) bool {
	transientPatterns := []string{
		"Connection reset by peer",
		"Connection timed out",
		"Could not resolve host",
		"SSL_ERROR_SYSCALL",
		"The remote end hung up unexpectedly",
		"early EOF",
		"failed to connect",
		"unable to access",
		"Connection refused",
		"HTTP 429",
		"HTTP 502",
		"HTTP 503",
		"HTTP 504",
	}
	combined := output + " " + err.Error()
	for _, p := range transientPatterns {
		if strings.Contains(combined, p) {
			return true
		}
	}
	return false
}

// hasLFS reports whether git-lfs is available on PATH.
func hasLFS() bool {
	_, err := exec.LookPath("git-lfs")
	return err == nil
}

// injectToken embeds a token as the password component of an HTTPS git URL so
// the native git binary authenticates without needing a credential helper.
func injectToken(rawURL, token string) string {
	if token == "" {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.User = url.UserPassword("x-token-auth", token)
	return u.String()
}

// scrubToken replaces a raw token with *** to avoid leaking credentials
// in error messages shown to the user or written to logs.
func scrubToken(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "***")
}

// isLFSError checks if an error is related to LFS file size limits.
func isLFSError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "lfs") || strings.Contains(msg, "file is too large")
}

// isLFSRejection checks if push output indicates HuggingFace rejected due to large files or binary files.
func isLFSRejection(output string) bool {
	return strings.Contains(output, "files larger than 10 MiB") ||
		strings.Contains(output, "git-lfs.github.com") ||
		strings.Contains(output, "contains binary files") ||
		strings.Contains(output, "hub/xet")
}
