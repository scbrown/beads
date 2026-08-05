package fixtures

import "github.com/steveyegge/beads/internal/testenv"

// Arms test mode for EVERY build of this package's tests (aegis-cd7rw).
//
// UNTAGGED and init(), not TestMain: a build-tag-gated arming protects only the
// builds its tag names, and with that tag off a dolt.Config with no explicit
// host resolves to the configured server — production on an operator's machine.
func init() { testenv.ArmTestMode() }
