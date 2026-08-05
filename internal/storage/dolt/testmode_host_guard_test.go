package dolt

import "testing"

// The case the pre-existing guard MISSES (aegis-4mzlq).
//
// `applyConfigDefaults` refused test-mode connections when the resolved port was
// DefaultSQLPort — a denylist of exactly one port. Measured on this fleet: the
// real server listens on a DIFFERENT port, so that check never fires here, and a
// test run reached the production HOST on a scratch port with the guard in
// place and silent.
//
// Port is the wrong question. A host is either this machine or it is somebody
// else's data.
func TestTestModeRefusesANonLocalHostWhateverThePort(t *testing.T) {
	// 192.0.2.0/24 is RFC 5737 TEST-NET-1 — documentation space, so no real
	// host is named here.
	const remote = "192.0.2.10"

	t.Run("the port the old guard watched", func(t *testing.T) {
		t.Setenv("BEADS_TEST_MODE", "1")
		cfg := &Config{Path: "/tmp/x", Database: "d", ServerHost: remote, ServerPort: DefaultSQLPort}
		applyConfigDefaults(cfg)
		if cfg.ServerPort != 1 {
			t.Fatalf("CONTROL FAILED: the pre-existing port guard must still fire, got %d", cfg.ServerPort)
		}
	})

	t.Run("a port it never watched", func(t *testing.T) {
		t.Setenv("BEADS_TEST_MODE", "1")
		// A port that is neither 0 nor DefaultSQLPort: the old guard is blind.
		cfg := &Config{Path: "/tmp/x", Database: "d", ServerHost: remote, ServerPort: 3399}
		applyConfigDefaults(cfg)
		if cfg.ServerPort != 1 {
			t.Fatalf("a test run was allowed to reach %s:3399 — the guard only "+
				"defended one port and production is not on it", remote)
		}
	})

	t.Run("CONTROL: a local host on an odd port is allowed", func(t *testing.T) {
		t.Setenv("BEADS_TEST_MODE", "1")
		cfg := &Config{Path: "/tmp/x", Database: "d", ServerHost: "127.0.0.1", ServerPort: 3399}
		applyConfigDefaults(cfg)
		if cfg.ServerPort != 3399 {
			t.Fatalf("a LOCAL test server must be reachable, got port %d — a guard "+
				"that refuses everything is not a guard", cfg.ServerPort)
		}
	})
}

func TestIsLocalDoltHostFailsClosed(t *testing.T) {
	for _, h := range []string{"", "127.0.0.1", "localhost", "::1", "127.0.0.1:3399"} {
		if !isLocalDoltHost(h) {
			t.Errorf("CONTROL FAILED: %q is local and must be allowed", h)
		}
	}
	for _, h := range []string{"192.0.2.10", "192.0.2.10:3306", "db.example.invalid"} {
		if isLocalDoltHost(h) {
			t.Errorf("%q is not demonstrably local and must fail closed", h)
		}
	}
}
