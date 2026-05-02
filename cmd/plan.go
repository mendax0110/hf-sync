package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/mendax0110/hf-sync/internal/engine"
	"github.com/mendax0110/hf-sync/internal/hfapi"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var planCmd = &cobra.Command{
	Use:   "plan <source-url> <hf-repo-id>",
	Short: "Preview what sync would do without pushing",
	Long: `Plan compares source and target refs and shows what actions sync would take.

No changes are made. This is equivalent to running sync with --dry-run.

Examples:
  hf-sync plan https://github.com/org/repo.git myuser/my-dataset --repo-type dataset`,
	Args: cobra.ExactArgs(2),
	RunE: runPlan,
}

func init() {
	rootCmd.AddCommand(planCmd)

	planCmd.Flags().String("repo-type", "dataset", "HuggingFace repo type: model, dataset, or space")
	planCmd.Flags().StringSlice("branches", nil, "branches to plan (default: all)")
	planCmd.Flags().Bool("tags", true, "include tags in plan")
	planCmd.Flags().Bool("force", false, "plan non-fast-forward updates")
	planCmd.Flags().Bool("prune", false, "plan deletions for refs not on source")
}

func runPlan(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	sourceURL := args[0]
	repoID := args[1]

	hfToken := viper.GetString("hf_token")
	if hfToken == "" {
		return fmt.Errorf("HuggingFace token required: set --hf-token or HF_TOKEN env var")
	}

	sourceToken := viper.GetString("source_token")
	repoType, _ := cmd.Flags().GetString("repo-type")
	branches, _ := cmd.Flags().GetStringSlice("branches")
	tags, _ := cmd.Flags().GetBool("tags")
	force, _ := cmd.Flags().GetBool("force")
	prune, _ := cmd.Flags().GetBool("prune")

	client := hfapi.NewClient(hfToken)
	outputJSON, _ := cmd.Root().PersistentFlags().GetBool("json")

	opts := engine.SyncOptions{
		SourceURL:   sourceURL,
		SourceToken: sourceToken,
		RepoID:      repoID,
		RepoType:    hfapi.RepoType(repoType),
		DryRun:      true, // plan is always dry-run
		Branches:    branches,
		Tags:        tags,
		Force:       force,
		Prune:       prune,
	}

	eng := engine.New(client)
	result, err := eng.Sync(ctx, opts)
	if err != nil {
		return fmt.Errorf("plan failed: %w", err)
	}

	if outputJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	fmt.Printf("Plan: %s → %s\n", result.Source, result.Target)
	for _, action := range result.Actions {
		fmt.Printf("  %s %s (%s)\n", action.Type, action.Ref, action.Reason)
	}
	if len(result.Actions) == 0 {
		fmt.Println("  (no changes needed)")
	}
	return nil
}
