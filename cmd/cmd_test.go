package cmd

import (
	"bytes"
	"testing"
)

func TestRootCommand_HasSubcommands(t *testing.T) {
	cmds := rootCmd.Commands()

	expected := map[string]bool{
		"sync":    false,
		"plan":    false,
		"batch":   false,
		"init":    false,
		"version": false,
	}

	for _, cmd := range cmds {
		if _, ok := expected[cmd.Name()]; ok {
			expected[cmd.Name()] = true
		}
	}

	for name, found := range expected {
		if !found {
			t.Errorf("expected subcommand %q not found", name)
		}
	}
}

func TestRootCommand_PersistentFlags(t *testing.T) {
	flags := []string{"config", "hf-token", "source-token", "log-level", "json"}

	for _, name := range flags {
		if rootCmd.PersistentFlags().Lookup(name) == nil {
			t.Errorf("expected persistent flag %q not found", name)
		}
	}
}

func TestSyncCommand_Flags(t *testing.T) {
	flags := []string{"repo-type", "private", "dry-run", "branches", "tags", "force", "prune", "create-repo"}

	for _, name := range flags {
		if syncCmd.Flags().Lookup(name) == nil {
			t.Errorf("expected flag %q not found on sync command", name)
		}
	}
}

func TestSyncCommand_RequiresArgs(t *testing.T) {
	cmd := syncCmd

	// Should require exactly 2 args.
	if cmd.Args == nil {
		t.Fatal("sync command should have Args validator")
	}

	err := cmd.Args(cmd, []string{})
	if err == nil {
		t.Error("expected error with 0 args")
	}

	err = cmd.Args(cmd, []string{"one"})
	if err == nil {
		t.Error("expected error with 1 arg")
	}

	err = cmd.Args(cmd, []string{"one", "two"})
	if err != nil {
		t.Errorf("unexpected error with 2 args: %v", err)
	}

	err = cmd.Args(cmd, []string{"one", "two", "three"})
	if err == nil {
		t.Error("expected error with 3 args")
	}
}

func TestPlanCommand_Flags(t *testing.T) {
	flags := []string{"repo-type", "branches", "tags", "force", "prune"}

	for _, name := range flags {
		if planCmd.Flags().Lookup(name) == nil {
			t.Errorf("expected flag %q not found on plan command", name)
		}
	}
}

func TestInitCommand_Flags(t *testing.T) {
	flags := []string{"repo-type", "private"}

	for _, name := range flags {
		if initCmd.Flags().Lookup(name) == nil {
			t.Errorf("expected flag %q not found on init command", name)
		}
	}
}

func TestVersionCommand_Output(t *testing.T) {
	buf := new(bytes.Buffer)
	versionCmd.SetOut(buf)
	versionCmd.Run(versionCmd, nil)

	output := buf.String()
	if output == "" {
		t.Error("version command should produce output")
	}
	if !bytes.Contains([]byte(output), []byte("hf-sync")) {
		t.Errorf("version output should contain 'hf-sync', got %q", output)
	}
}

func TestSyncCommand_DefaultFlagValues(t *testing.T) {
	repoType, _ := syncCmd.Flags().GetString("repo-type")
	if repoType != "dataset" {
		t.Errorf("default repo-type = %q, want 'dataset'", repoType)
	}

	private, _ := syncCmd.Flags().GetBool("private")
	if private {
		t.Error("default private should be false")
	}

	dryRun, _ := syncCmd.Flags().GetBool("dry-run")
	if dryRun {
		t.Error("default dry-run should be false")
	}

	tags, _ := syncCmd.Flags().GetBool("tags")
	if !tags {
		t.Error("default tags should be true")
	}

	force, _ := syncCmd.Flags().GetBool("force")
	if force {
		t.Error("default force should be false")
	}

	prune, _ := syncCmd.Flags().GetBool("prune")
	if prune {
		t.Error("default prune should be false")
	}

	createRepo, _ := syncCmd.Flags().GetBool("create-repo")
	if !createRepo {
		t.Error("default create-repo should be true")
	}
}
