package cmd

import "github.com/spf13/cobra"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print hf-sync version",
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Println("hf-sync " + version)
	},
}

// version is set at build time via ldflags.
var version = "dev"

func init() {
	rootCmd.AddCommand(versionCmd)
}
