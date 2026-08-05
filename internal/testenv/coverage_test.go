package testenv

import (
	"go/build"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestEveryDoltTouchingPackageArmsTestModeUntagged fails when a test package
// that can construct a dolt.Config has no UNTAGGED file arming test mode.
//
// "Untagged" is the assertion, not "present" (aegis-cd7rw). Every arming point
// in beads was `//go:build cgo` with no `!cgo` counterpart, so `CGO_ENABLED=0
// go test` armed nothing and a config with no explicit host resolved to the
// production Dolt server — measured as dolt.lan:3306 without test mode, :1
// with it. A check that merely asked "does this package arm test mode
// somewhere?" would have passed on every one of those packages while they were
// unprotected, which is the failure it exists to catch.
//
// So the protection cannot be opt-in per build tag. An untagged file is
// compiled into every configuration of the test binary, which means a tag added
// later INHERITS the guard instead of needing to remember it. Enumerating build
// tags is still an enumeration, and it fails open for every tag nobody listed.
func TestEveryDoltTouchingPackageArmsTestModeUntagged(t *testing.T) {
	root := repoRoot(t)

	var unarmed []string
	var checked int

	for _, dir := range testDirsConstructingDoltConfigs(t, root) {
		checked++
		if !hasUntaggedArming(t, dir) {
			rel, _ := filepath.Rel(root, dir)
			unarmed = append(unarmed, rel)
		}
	}

	// CONTROL: an empty walk would report perfect coverage. That is the same
	// vacuous probe this whole chain is about, one level up.
	if checked == 0 {
		t.Fatal("CONTROL FAILED: found no test packages constructing dolt configs — the walk is " +
			"broken, and an empty result here would read as full coverage")
	}

	if len(unarmed) > 0 {
		sort.Strings(unarmed)
		t.Errorf("%d of %d test package(s) that can construct a dolt.Config have no UNTAGGED "+
			"arming of test mode:\n  %s\n\n"+
			"A build-tag-gated TestMain does not count: with that tag off, nothing arms "+
			"BEADS_TEST_MODE and a config with no explicit host resolves to the configured "+
			"server — production on an operator's machine. Add an untagged file:\n\n"+
			"    package <pkg>\n\n"+
			"    import \"github.com/steveyegge/beads/internal/testenv\"\n\n"+
			"    func init() { testenv.ArmTestMode() }",
			len(unarmed), checked, strings.Join(unarmed, "\n  "))
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find go.mod above the working directory")
		}
		dir = parent
	}
}

// testDirsConstructingDoltConfigs returns directories whose _test.go files can
// build a dolt.Config — either by importing the package or, for the dolt
// package itself, by naming Config unqualified.
func testDirsConstructingDoltConfigs(t *testing.T, root string) []string {
	t.Helper()
	seen := map[string]bool{}
	var dirs []string

	doltPkgDir := filepath.Join(root, "internal", "storage", "dolt")

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // unreadable entries are not coverage gaps
		}
		if d.IsDir() {
			// Vendored and VCS trees are not ours to guard.
			if name := d.Name(); name == ".git" || name == "vendor" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		dir := filepath.Dir(path)
		if dir == filepath.Join(root, "internal", "testenv") {
			return nil // holds the helper itself
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		src := string(data)
		// The dolt package's own tests say `&Config{`, with no qualifier — the
		// same unqualified-reference blind spot that made gastown's equivalent
		// check under-count its input by one, and the missed package was the
		// most important one.
		constructs := strings.Contains(src, "dolt.Config{") ||
			strings.Contains(src, `storage/dolt"`) ||
			(dir == doltPkgDir && strings.Contains(src, "Config{"))
		if !constructs {
			return nil
		}
		if !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return dirs
}

// hasUntaggedArming reports whether the toolchain actually COMPILES an arming
// file into this package's tests under BOTH cgo settings.
//
// It asks go/build rather than reading the directory, because "the file is
// there" is not the property that matters and checking it is a vacuous probe.
// Measured while building this: the arming files were originally named
// testmode_arm_test.go, and Go reads a trailing _arm before _test.go as an
// implicit GOARCH=arm constraint — so on amd64 the toolchain silently excluded
// every one of them. The directory-reading version of this check PASSED on all
// ten packages while none of the guards compiled. A filename can be a build
// constraint, and no //go:build line has to be present for a file to be
// constrained out.
func hasUntaggedArming(t *testing.T, dir string) bool {
	t.Helper()
	for _, cgo := range []bool{true, false} {
		ctx := build.Default
		ctx.CgoEnabled = cgo
		pkg, err := ctx.ImportDir(dir, 0)
		if err != nil {
			// A package that cannot be imported in a configuration cannot be
			// shown to be armed in it.
			return false
		}
		if !armingFilePresent(dir, pkg.TestGoFiles) && !armingFilePresent(dir, pkg.XTestGoFiles) {
			return false
		}
	}
	return true
}

// armingFilePresent reports whether any of the named compiled files arms test
// mode without carrying an explicit build constraint.
func armingFilePresent(dir string, files []string) bool {
	for _, name := range files {
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		text := string(src)
		if hasBuildConstraint(text) {
			continue // gated: protects only the builds its tag names
		}
		if strings.Contains(text, "testenv.ArmTestMode()") ||
			strings.Contains(text, `Setenv("BEADS_TEST_MODE"`) {
			return true
		}
	}
	return false
}

// hasBuildConstraint reports whether the file header carries any //go:build
// line. ANY constraint disqualifies it — including `!cgo`, which protects only
// the builds it names and is exactly the per-tag opt-in this check rejects.
func hasBuildConstraint(src string) bool {
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "//go:build") {
			return true
		}
		if line != "" && !strings.HasPrefix(line, "//") {
			return false // past the header
		}
	}
	return false
}
