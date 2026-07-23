package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Regression tests for the silent-no-op parent-command defect: a bare parent
// like `bd label <id> <label>` (missing the `add` subcommand) used to print
// help and EXIT 0 — cobra returns ErrHelp for non-runnable commands before
// ValidateArgs ever runs, so scripts read the no-op as success
// (`bd label X Y >/dev/null && echo ok` reported labels that were never
// applied). enforceParentArgs makes every bare parent runnable and rejecting.

// buildTestTree mirrors the real shape: parent with subcommands, no Run.
func buildTestTree() (*cobra.Command, *cobra.Command) {
	root := &cobra.Command{Use: "bd-test", Run: func(*cobra.Command, []string) {}}
	parent := &cobra.Command{Use: "label", Short: "Manage issue labels"}
	parent.AddCommand(&cobra.Command{
		Use:  "add",
		Args: cobra.MinimumNArgs(2),
		Run:  func(*cobra.Command, []string) {},
	})
	root.AddCommand(parent)
	return root, parent
}

func TestEnforceParentArgs_RejectsStrayArgsNonZero(t *testing.T) {
	root, parent := buildTestTree()
	enforceParentArgs(root)

	if !parent.Runnable() {
		t.Fatal("enforceParentArgs must make bare parents Runnable — cobra skips ValidateArgs for non-runnable commands, so a bare-parent Args validator alone is dead code")
	}
	if parent.Annotations[bareParentHelpAnnotation] != "true" {
		t.Error("shimmed parent must carry the bareParentHelpAnnotation so PersistentPreRun skips store init for bare help invocations")
	}

	root.SetArgs([]string{"label", "some-id", "some-label"})
	root.SetOut(&strings.Builder{})
	root.SetErr(&strings.Builder{})
	if err := root.Execute(); err == nil {
		t.Fatal("`label <id> <label>` (missing subcommand) must return an error, not silently print help — this exact shape published false success reports")
	}
}

func TestEnforceParentArgs_BareParentStillShowsHelp(t *testing.T) {
	root, _ := buildTestTree()
	enforceParentArgs(root)

	out := &strings.Builder{}
	root.SetArgs([]string{"label"})
	root.SetOut(out)
	root.SetErr(&strings.Builder{})
	if err := root.Execute(); err != nil {
		t.Fatalf("bare `label` should show help and succeed, got error: %v", err)
	}
	if !strings.Contains(out.String(), "Manage issue labels") {
		t.Errorf("bare parent should print its help text, got: %q", out.String())
	}
}

func TestEnforceParentArgs_LeavesExistingArgsAndRunAlone(t *testing.T) {
	root, _ := buildTestTree()
	custom := &cobra.Command{
		Use:  "custom",
		Args: cobra.ExactArgs(1),
		Run:  func(*cobra.Command, []string) {},
	}
	root.AddCommand(custom)
	runnableParent := &cobra.Command{Use: "runnable", Run: func(*cobra.Command, []string) {}}
	runnableParent.AddCommand(&cobra.Command{Use: "sub", Run: func(*cobra.Command, []string) {}})
	root.AddCommand(runnableParent)

	enforceParentArgs(root)

	if custom.Annotations[bareParentHelpAnnotation] == "true" {
		t.Error("commands with their own Run must not be shimmed")
	}
	if runnableParent.Annotations[bareParentHelpAnnotation] == "true" {
		t.Error("already-runnable parents must keep their own Run, not the help shim")
	}
}

func TestLabelCmd_BareArgsSuggestAdd(t *testing.T) {
	// The real labelCmd carries a custom validator with a did-you-mean hint;
	// call it directly (hermetic — no store, no Execute).
	err := labelCmd.Args(labelCmd, []string{"bd-testid", "some-label"})
	if err == nil {
		t.Fatal("labelCmd must reject `label <id> <label>` with an error")
	}
	if !strings.Contains(err.Error(), "label add bd-testid some-label") {
		t.Errorf("error should suggest the intended `label add` invocation, got: %v", err)
	}
	if err := labelCmd.Args(labelCmd, nil); err != nil {
		t.Errorf("bare `label` must not error from Args, got: %v", err)
	}
}
