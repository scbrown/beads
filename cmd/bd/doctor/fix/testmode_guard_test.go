package fix

import "github.com/steveyegge/beads/internal/testenv"

// Arms test mode for EVERY build of this package's tests (aegis-cd7rw).
//
// Deliberately UNTAGGED and deliberately init(), not TestMain: this package's
// TestMain is `//go:build cgo`, so a CGO_ENABLED=0 run armed nothing and a
// dolt.Config with no explicit host resolved to the production server. An
// untagged file compiles into every configuration, so a tag added later
// inherits this rather than needing to remember it.
func init() { testenv.ArmTestMode() }
