package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/mendax0110/hf-sync/internal/hfapi"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var initCmd = &cobra.Command{
	Use:   "init <hf-repo-id>",
	Short: "Create a new HuggingFace repository",
	Long: `Init creates a new repository on HuggingFace Hub.

This is useful for pre-creating repositories before running sync,
or for setting up repositories with specific configurations.

Examples:
  # Create a public dataset repo
  hf-sync init myuser/my-dataset --repo-type dataset

  # Create a private model repo
  hf-sync init myorg/my-model --repo-type model --private`,
	Args: cobra.ExactArgs(1),
	RunE: runInit,
}

func init() {
	rootCmd.AddCommand(initCmd)

	initCmd.Flags().String("repo-type", "dataset", "HuggingFace repo type: model, dataset, or space")
	initCmd.Flags().Bool("private", false, "create as private repository")
}

func runInit(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	repoID := args[0]
	hfToken := viper.GetString("hf_token")
	if hfToken == "" {
		return fmt.Errorf("HuggingFace token required: set --hf-token or HF_TOKEN env var")
	}

	repoType, _ := cmd.Flags().GetString("repo-type")
	private, _ := cmd.Flags().GetBool("private")
	outputJSON, _ := cmd.Root().PersistentFlags().GetBool("json")

	client := hfapi.NewClient(hfToken)

	repo, err := client.CreateRepo(ctx, hfapi.CreateRepoRequest{
		RepoID:  repoID,
		Type:    hfapi.RepoType(repoType),
		Private: private,
	})
	if err != nil {
		return fmt.Errorf("failed to create repository: %w", err)
	}

	if outputJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(repo)
	}

	fmt.Printf("Created %s repository: %s\n", repoType, repo.URL)
	return nil
}
