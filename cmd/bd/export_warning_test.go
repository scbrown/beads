package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestShouldWarnTrackedJSONLNotUpdated(t *testing.T) {
	repo := t.TempDir()
	beadsDir := filepath.Join(repo, ".beads")
	if err := os.Mkdir(beadsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"metadata.json": `{"database":"test"}`,
		"issues.jsonl":  "",
	} {
		if err := os.WriteFile(filepath.Join(beadsDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	runGit("init", "-q")
	runGit("add", ".beads/issues.jsonl")

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	if !shouldWarnTrackedJSONLNotUpdated(false) {
		t.Fatal("expected warning for non-TTY export with tracked issues.jsonl")
	}
	if shouldWarnTrackedJSONLNotUpdated(true) {
		t.Fatal("did not expect warning for TTY export")
	}
	runGit("rm", "--cached", "-q", ".beads/issues.jsonl")
	if shouldWarnTrackedJSONLNotUpdated(false) {
		t.Fatal("did not expect warning for untracked issues.jsonl")
	}
}
