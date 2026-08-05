// Package testenv arms the beads test-mode protections in a way that does not
// depend on build tags.
package testenv

import "os"

// ArmTestMode sets BEADS_TEST_MODE=1, which makes applyConfigDefaults refuse a
// non-loopback Dolt host whatever the port (aegis-4mzlq).
//
// Call it from an UNTAGGED `init()` in every test package that can construct a
// dolt.Config. That placement is the whole point and it is not interchangeable
// with calling it from TestMain (aegis-cd7rw):
//
//   - An untagged file is compiled into EVERY configuration of the package's
//     test binary, so a build tag added later inherits the protection instead
//     of having to remember it. Every arming point in beads was `//go:build
//     cgo`, with no `!cgo` counterpart, so `CGO_ENABLED=0 go test` armed
//     nothing and a config with no explicit host resolved to the production
//     server. Measured: dolt.lan:3306 without test mode, :1 with it.
//   - init() runs before TestMain, so it cannot be skipped by a package that
//     has no TestMain at all.
//
// Adding `!cgo` counterparts would have fixed the instance and left the shape:
// the protection would still be opt-in per tag, and its absence still
// invisible. Build tags are an enumeration, and enumerating the unsafe cases
// fails open for every case nobody enumerated.
//
// It does not force the value: a test that deliberately exercises non-test-mode
// behavior overrides it with t.Setenv, which is scoped and restored.
func ArmTestMode() {
	_ = os.Setenv("BEADS_TEST_MODE", "1")
}
