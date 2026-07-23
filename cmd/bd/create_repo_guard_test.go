package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// aegis-efky: an explicit --repo naming a nonexistent repo used to silently
// create a fresh store and write the issue into it while printing success.
func TestValidateExplicitRepoTarget_RefusesMissingRepo(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "not-a-repo")

	err := validateExplicitRepoTarget(target, "not-a-repo")
	if err == nil {
		t.Fatal("expected an error for a target with no .beads/, got nil")
	}
	if !strings.Contains(err.Error(), "not an existing beads repo") {
		t.Fatalf("error should say the repo does not exist, got: %v", err)
	}
	// The refusal must not have created anything.
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("validation must not create the target directory")
	}
}

func TestValidateExplicitRepoTarget_AcceptsExistingRepo(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := validateExplicitRepoTarget(tmp, tmp); err != nil {
		t.Fatalf("existing repo must be accepted, got: %v", err)
	}
}
