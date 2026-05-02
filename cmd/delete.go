package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/mendax0110/hf-sync/internal/hfapi"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/sync/errgroup"
)

var deleteCmd = &cobra.Command{
	Use:   "delete [repo-id...]",
	Short: "Delete HuggingFace repositories",
	Long: `Delete one or more HuggingFace repositories permanently.

Can delete repos specified as arguments, or all repos defined in the config file
using --from-config. This is IRREVERSIBLE.

Examples:
  # Delete a single repo
  hf-sync delete myuser/my-dataset --repo-type dataset

  # Delete multiple repos
  hf-sync delete myuser/repo1 myuser/repo2 --repo-type dataset

  # Delete ALL target repos defined in the config file
  hf-sync delete --from-config --config hf-sync.yaml

  # Dry-run to see what would be deleted
  hf-sync delete --from-config --dry-run`,
	RunE: runDelete,
}

func init() {
	rootCmd.AddCommand(deleteCmd)

	deleteCmd.Flags().String("repo-type", "dataset", "HuggingFace repo type: model, dataset, or space")
	deleteCmd.Flags().Bool("from-config", false, "delete all target repos defined in the config file")
	deleteCmd.Flags().Bool("dry-run", false, "show what would be deleted without actually deleting")
	deleteCmd.Flags().Bool("yes", false, "skip confirmation prompt (use with caution)")
}

func runDelete(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	hfToken := viper.GetString("hf_token")
	if hfToken == "" {
		return fmt.Errorf("HuggingFace token required: set --hf-token or HF_TOKEN env var")
	}

	repoType, _ := cmd.Flags().GetString("repo-type")
	fromConfig, _ := cmd.Flags().GetBool("from-config")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	confirmed, _ := cmd.Flags().GetBool("yes")

	// Collect repos to delete.
	type deleteTarget struct {
		RepoID   string
		RepoType hfapi.RepoType
	}
	var targets []deleteTarget

	if fromConfig {
		var cfg batchConfig
		if err := viper.Unmarshal(&cfg); err != nil {
			return fmt.Errorf("parsing config: %w", err)
		}
		if len(cfg.Repos) == 0 {
			return fmt.Errorf("no repositories defined in config file")
		}
		for _, repo := range cfg.Repos {
			rt := cfg.Defaults.RepoType
			if repo.RepoType != "" {
				rt = repo.RepoType
			}
			targets = append(targets, deleteTarget{
				RepoID:   repo.Target,
				RepoType: hfapi.RepoType(rt),
			})
		}
	} else {
		if len(args) == 0 {
			return fmt.Errorf("provide repo IDs as arguments or use --from-config")
		}
		for _, id := range args {
			targets = append(targets, deleteTarget{
				RepoID:   id,
				RepoType: hfapi.RepoType(repoType),
			})
		}
	}

	// Show what will be deleted.
	fmt.Printf("Repos to delete (%d):\n", len(targets))
	for _, t := range targets {
		fmt.Printf("  - %s (type: %s)\n", t.RepoID, t.RepoType)
	}

	if dryRun {
		fmt.Println("\n(dry-run: no repos were deleted)")
		return nil
	}

	// Confirm unless --yes was passed.
	if !confirmed {
		fmt.Printf("\n⚠️  This will PERMANENTLY delete %d repositories. This cannot be undone.\n", len(targets))
		fmt.Print("Type 'yes' to confirm: ")
		var input string
		if _, err := fmt.Scanln(&input); err != nil {
			if errors.Is(err, io.EOF) {
				fmt.Println("Aborted.")
				return nil
			}
			return fmt.Errorf("read confirmation: %w", err)
		}
		if input != "yes" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	client := hfapi.NewClient(hfToken)

	// Delete concurrently (up to 4 at a time).
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(4)

	var mu sync.Mutex
	var succeeded, failed int

	for _, t := range targets {
		t := t
		g.Go(func() error {
			err := client.DeleteRepo(gctx, t.RepoID, t.RepoType)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed++
				fmt.Fprintf(os.Stderr, "  ✗ %s: %v\n", t.RepoID, err)
			} else {
				succeeded++
				fmt.Printf("  ✓ deleted %s\n", t.RepoID)
			}
			return nil // don't abort other deletions
		})
	}

	_ = g.Wait()

	fmt.Printf("\nDone: %d deleted, %d failed\n", succeeded, failed)
	return nil
}
