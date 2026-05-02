// Package main is the entry point for the hf-sync CLI.
//
// hf-sync mirrors git repositories to HuggingFace Hub as datasets, models,
// or spaces. It leverages HuggingFace's native git+LFS protocol to stream
// repository content directly without requiring a full local checkout.
package main

import (
	"os"

	"github.com/mendax0110/hf-sync/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
