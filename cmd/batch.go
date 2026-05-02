package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/mendax0110/hf-sync/internal/engine"
	"github.com/mendax0110/hf-sync/internal/hfapi"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/sync/errgroup"
)

var batchCmd = &cobra.Command{
	Use:   "batch",
	Short: "Sync all repositories defined in the config file",
	Long: `Batch syncs all repositories defined in the hf-sync.yaml config file.

Each repository entry in the config specifies a source URL and target HuggingFace
repo ID. Repos are processed concurrently (up to --workers at a time).

Examples:
  # Sync all repos in config
  hf-sync batch

  # Sync with explicit config file
  hf-sync batch --config ./my-config.yaml

  # Dry-run all repos
  hf-sync batch --dry-run

  # Control concurrency
  hf-sync batch --workers 2`,
	RunE: runBatch,
}

func init() {
	rootCmd.AddCommand(batchCmd)

	batchCmd.Flags().Bool("dry-run", false, "plan only, don't execute")
	batchCmd.Flags().Int("workers", 8, "number of repos to sync concurrently")
	batchCmd.Flags().Bool("no-cache", false, "disable mirror cache (force full clone each time)")
}

// batchConfig represents the structure of the config file for batch operations.
type batchConfig struct {
	Defaults batchDefaults `mapstructure:"defaults"`
	Repos    []repoConfig  `mapstructure:"repos"`
}

type batchDefaults struct {
	RepoType string `mapstructure:"repo_type"`
	Private  bool   `mapstructure:"private"`
	Tags     bool   `mapstructure:"tags"`
	Force    bool   `mapstructure:"force"`
	Prune    bool   `mapstructure:"prune"`
}

type repoConfig struct {
	Source   string   `mapstructure:"source"`
	Target   string   `mapstructure:"target"`
	RepoType string   `mapstructure:"repo_type"`
	Private  *bool    `mapstructure:"private"`
	Tags     *bool    `mapstructure:"tags"`
	Force    *bool    `mapstructure:"force"`
	Prune    *bool    `mapstructure:"prune"`
	Branches []string `mapstructure:"branches"`
}

type batchResult struct {
	Results []batchRepoResult `json:"results"`
	Total   int               `json:"total"`
	Success int               `json:"success"`
	Failed  int               `json:"failed"`
}

type batchRepoResult struct {
	Source string             `json:"source"`
	Target string             `json:"target"`
	Result *engine.SyncResult `json:"result,omitempty"`
	Error  string             `json:"error,omitempty"`
}

func runBatch(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	hfToken := viper.GetString("hf_token")
	if hfToken == "" {
		return fmt.Errorf("HuggingFace token required: set --hf-token or HF_TOKEN env var")
	}

	sourceToken := viper.GetString("source_token")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	workers, _ := cmd.Flags().GetInt("workers")
	noCache, _ := cmd.Flags().GetBool("no-cache")
	outputJSON, _ := cmd.Root().PersistentFlags().GetBool("json")

	if workers < 1 {
		workers = 1
	}

	var cfg batchConfig
	if err := viper.Unmarshal(&cfg); err != nil {
		return fmt.Errorf("parsing config: %w", err)
	}

	if len(cfg.Repos) == 0 {
		return fmt.Errorf("no repositories defined in config file")
	}

	client := hfapi.NewClient(hfToken)

	total := len(cfg.Repos)
	results := make([]batchRepoResult, total)
	batchStart := time.Now()

	if !outputJSON {
		fmt.Printf("Starting batch sync: %d repos, %d workers\n\n", total, workers)
	}

	// Use errgroup with concurrency limit for cleaner parallel execution.
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(workers)

	var mu sync.Mutex

	for i, repo := range cfg.Repos {
		i, repo := i, repo
		g.Go(func() error {
			repoStart := time.Now()

			if !outputJSON {
				mu.Lock()
				fmt.Printf("[%d/%d] ▶ %s → %s\n", i+1, total, repo.Source, repo.Target)
				mu.Unlock()
			}

			// Each repo gets its own engine instance for independent progress reporting.
			eng := engine.New(client)
			if noCache {
				eng.WithCacheDir("")
			}
			if !outputJSON {
				eng.WithProgress(func(repoID string, phase engine.Phase, msg string) {
					mu.Lock()
					elapsed := time.Since(repoStart).Truncate(time.Millisecond)
					fmt.Printf("  [%d/%d] [%s] %-8s %s\n", i+1, total, elapsed, phase, msg)
					mu.Unlock()
				})
			}

			opts := buildSyncOpts(repo, cfg.Defaults, sourceToken, dryRun)
			syncResult, err := eng.Sync(gctx, opts)

			elapsed := time.Since(repoStart).Truncate(time.Millisecond)

			entry := batchRepoResult{
				Source: repo.Source,
				Target: repo.Target,
			}
			if err != nil {
				entry.Error = err.Error()
				if !outputJSON {
					mu.Lock()
					fmt.Printf("[%d/%d] ✗ FAILED (%s): %v\n", i+1, total, elapsed, err)
					mu.Unlock()
				}
			} else {
				entry.Result = syncResult
				if !outputJSON {
					mu.Lock()
					fmt.Printf("[%d/%d] ✓ OK (%s) created=%d updated=%d deleted=%d skipped=%d\n",
						i+1, total, elapsed,
						syncResult.Created, syncResult.Updated,
						syncResult.Deleted, syncResult.Skipped)
					mu.Unlock()
				}
			}

			mu.Lock()
			results[i] = entry
			mu.Unlock()

			return nil // don't abort other repos on individual failure
		})
	}

	_ = g.Wait()

	// Tally successes and failures from the ordered results slice.
	br := batchResult{
		Total:   total,
		Results: results,
	}
	for _, r := range results {
		if r.Error != "" {
			br.Failed++
		} else {
			br.Success++
		}
	}

	if outputJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(br)
	}

	totalElapsed := time.Since(batchStart).Truncate(time.Millisecond)
	fmt.Printf("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Printf("Batch complete in %s: %d/%d succeeded, %d failed\n", totalElapsed, br.Success, br.Total, br.Failed)
	return nil
}

func buildSyncOpts(repo repoConfig, defaults batchDefaults, sourceToken string, dryRun bool) engine.SyncOptions {
	repoType := defaults.RepoType
	if repo.RepoType != "" {
		repoType = repo.RepoType
	}

	private := defaults.Private
	if repo.Private != nil {
		private = *repo.Private
	}

	tags := defaults.Tags
	if repo.Tags != nil {
		tags = *repo.Tags
	}

	force := defaults.Force
	if repo.Force != nil {
		force = *repo.Force
	}

	prune := defaults.Prune
	if repo.Prune != nil {
		prune = *repo.Prune
	}

	return engine.SyncOptions{
		SourceURL:   repo.Source,
		SourceToken: sourceToken,
		RepoID:      repo.Target,
		RepoType:    hfapi.RepoType(repoType),
		Private:     private,
		DryRun:      dryRun,
		Branches:    repo.Branches,
		Tags:        tags,
		Force:       force,
		Prune:       prune,
		CreateRepo:  true,
	}
}
