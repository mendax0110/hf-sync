package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/mendax0110/hf-sync/internal/engine"
	"github.com/mendax0110/hf-sync/internal/hfapi"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var syncCmd = &cobra.Command{
	Use:   "sync <source-url> <hf-repo-id>",
	Short: "Mirror a source git repo to a HuggingFace repository",
	Long: `Sync mirrors refs from a source git remote to a HuggingFace Hub repository.

If the target HuggingFace repository does not exist, it is created automatically.
The sync transfers all branches and tags by default, or a filtered subset via --branches.

Examples:
  # Sync a GitHub repo to a HuggingFace dataset
  hf-sync sync https://github.com/org/repo.git myuser/my-dataset --repo-type dataset

  # Sync with explicit tokens
  hf-sync sync \
    --source-token $GITHUB_TOKEN \
    --hf-token $HF_TOKEN \
    https://github.com/org/repo.git myuser/my-model

  # Dry-run to preview changes
  hf-sync sync --dry-run https://github.com/org/repo.git myuser/my-dataset`,
	Args: cobra.ExactArgs(2),
	RunE: runSync,
}

func init() {
	rootCmd.AddCommand(syncCmd)

	syncCmd.Flags().String("repo-type", "dataset", "HuggingFace repo type: model, dataset, or space")
	syncCmd.Flags().Bool("private", false, "create private repository if it doesn't exist")
	syncCmd.Flags().Bool("dry-run", false, "plan and display actions without executing")
	syncCmd.Flags().StringSlice("branches", nil, "branches to sync (default: all)")
	syncCmd.Flags().Bool("tags", true, "sync tags")
	syncCmd.Flags().Bool("force", false, "allow non-fast-forward updates")
	syncCmd.Flags().Bool("prune", false, "delete refs on target that don't exist on source")
	syncCmd.Flags().Bool("create-repo", true, "auto-create the HuggingFace repo if missing")
	syncCmd.Flags().Bool("no-cache", false, "disable mirror cache (force full clone)")
}

func runSync(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	sourceURL := args[0]
	repoID := args[1]

	hfToken := viper.GetString("hf_token")
	if hfToken == "" {
		return fmt.Errorf("HuggingFace token required: set --hf-token or HF_TOKEN env var")
	}

	sourceToken := viper.GetString("source_token")
	repoType, _ := cmd.Flags().GetString("repo-type")
	private, _ := cmd.Flags().GetBool("private")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	branches, _ := cmd.Flags().GetStringSlice("branches")
	tags, _ := cmd.Flags().GetBool("tags")
	force, _ := cmd.Flags().GetBool("force")
	prune, _ := cmd.Flags().GetBool("prune")
	createRepo, _ := cmd.Flags().GetBool("create-repo")
	noCache, _ := cmd.Flags().GetBool("no-cache")

	client := hfapi.NewClient(hfToken)
	outputJSON, _ := cmd.Flags().GetBool("json")

	opts := engine.SyncOptions{
		SourceURL:   sourceURL,
		SourceToken: sourceToken,
		RepoID:      repoID,
		RepoType:    hfapi.RepoType(repoType),
		Private:     private,
		DryRun:      dryRun,
		Branches:    branches,
		Tags:        tags,
		Force:       force,
		Prune:       prune,
		CreateRepo:  createRepo,
	}

	eng := engine.New(client)
	if noCache {
		eng.WithCacheDir("")
	}
	if !outputJSON {
		eng.WithProgress(engine.TextProgress(os.Stderr))
	}

	start := time.Now()
	result, err := eng.Sync(ctx, opts)
	if err != nil {
		return fmt.Errorf("sync failed: %w", err)
	}

	if outputJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	elapsed := time.Since(start).Truncate(time.Millisecond)
	printSyncResult(result, elapsed)
	return nil
}

func printSyncResult(r *engine.SyncResult, elapsed time.Duration) {
	fmt.Printf("Sync complete in %s: %s → %s\n", elapsed, r.Source, r.Target)
	fmt.Printf("  Refs created:  %d\n", r.Created)
	fmt.Printf("  Refs updated:  %d\n", r.Updated)
	fmt.Printf("  Refs deleted:  %d\n", r.Deleted)
	fmt.Printf("  Refs skipped:  %d\n", r.Skipped)
	if r.DryRun {
		fmt.Println("  (dry-run: no changes applied)")
	}
}
