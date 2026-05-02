// Package cmd implements the CLI commands for hf-sync.
//
// Commands follow a plan-then-execute model inspired by git-sync:
//   - sync: mirror a source repo to HuggingFace Hub
//   - plan: preview what sync would do without pushing
//   - init: create a new HuggingFace repository
//   - status: show sync state between source and target
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string

// rootCmd is the base command for hf-sync.
var rootCmd = &cobra.Command{
	Use:   "hf-sync",
	Short: "Mirror git repositories to HuggingFace Hub",
	Long: `hf-sync mirrors git repositories to HuggingFace Hub as datasets, models, or spaces.

It leverages HuggingFace's native git+LFS protocol to stream repository content
directly from a source remote to HuggingFace without requiring a full local checkout.

Features:
  - Remote-to-remote sync via smart HTTP
  - Automatic HuggingFace repository creation
  - LFS-aware transfers for large files
  - Dataset card generation
  - JSON output for automation
  - Config file for multi-repo batch sync`,
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is ./hf-sync.yaml)")
	rootCmd.PersistentFlags().String("hf-token", "", "HuggingFace API token (or set HF_TOKEN env var)")
	rootCmd.PersistentFlags().String("source-token", "", "source remote token for authentication")
	rootCmd.PersistentFlags().String("log-level", "info", "log level (debug, info, warn, error)")
	rootCmd.PersistentFlags().Bool("json", false, "output results as JSON")

	// Bind environment variables.
	_ = viper.BindPFlag("hf_token", rootCmd.PersistentFlags().Lookup("hf-token"))
	_ = viper.BindPFlag("source_token", rootCmd.PersistentFlags().Lookup("source-token"))
	_ = viper.BindEnv("hf_token", "HF_TOKEN")
	_ = viper.BindEnv("source_token", "GITSYNC_SOURCE_TOKEN")
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		viper.SetConfigName("hf-sync")
		viper.SetConfigType("yaml")
		viper.AddConfigPath(".")
		viper.AddConfigPath("$HOME/.config/hf-sync")
	}

	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err == nil {
		if viper.GetBool("json") {
			return
		}
		fmt.Fprintln(os.Stderr, "Using config file:", viper.ConfigFileUsed())
	}
}
