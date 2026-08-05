package dolt

import "testing"

// An explicitly-set port must win over ambient environment (aegis-4mzlq).
//
// `applyConfigDefaults` assigned the env port unconditionally, so a caller that
// had deliberately chosen a port was silently redirected by whatever the shell
// happened to export. `ServerHost` immediately above never had this defect — it
// fills only when empty — and the test harness's own comment already described
// the intended contract as "read when ServerPort is 0". The code was the odd one
// out, not the expectation.
//
// Loopback ports throughout: the test-mode guard refuses a non-local host
// whatever the port, so a remote address here would be forced to the sentinel
// and the precedence question would never be reached.
func TestExplicitPortWinsOverAmbientEnv(t *testing.T) {
	t.Run("explicit port survives BEADS_DOLT_SERVER_PORT", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_SERVER_PORT", "43999")
		cfg := &Config{Path: "/tmp/x", Database: "d", ServerHost: "127.0.0.1", ServerPort: 43211}
		applyConfigDefaults(cfg)
		if cfg.ServerPort != 43211 {
			t.Fatalf("ambient env overrode a deliberately chosen port: got %d, want 43211", cfg.ServerPort)
		}
	})

	t.Run("explicit port survives the legacy BEADS_DOLT_PORT", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_SERVER_PORT", "")
		t.Setenv("BEADS_DOLT_PORT", "43999")
		cfg := &Config{Path: "/tmp/x", Database: "d", ServerHost: "127.0.0.1", ServerPort: 43211}
		applyConfigDefaults(cfg)
		if cfg.ServerPort != 43211 {
			t.Fatalf("the legacy env var overrode a deliberately chosen port: got %d, want 43211", cfg.ServerPort)
		}
	})

	// CONTROL. Without this the test above passes just as well against a
	// function that ignores the environment entirely, which would break every
	// caller that legitimately relies on it — including the test harnesses that
	// point the suite at an ephemeral container.
	t.Run("CONTROL: env still fills an unset port", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_SERVER_PORT", "43999")
		cfg := &Config{Path: "/tmp/x", Database: "d", ServerHost: "127.0.0.1"}
		applyConfigDefaults(cfg)
		if cfg.ServerPort != 43999 {
			t.Fatalf("env no longer fills an unset port: got %d, want 43999", cfg.ServerPort)
		}
	})

	t.Run("CONTROL: the legacy var still fills an unset port", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_SERVER_PORT", "")
		t.Setenv("BEADS_DOLT_PORT", "43999")
		cfg := &Config{Path: "/tmp/x", Database: "d", ServerHost: "127.0.0.1"}
		applyConfigDefaults(cfg)
		if cfg.ServerPort != 43999 {
			t.Fatalf("the legacy fallback stopped filling an unset port: got %d, want 43999", cfg.ServerPort)
		}
	})
}

// The precedence change must not open a hole in the leak guards, which are the
// whole reason this function is under scrutiny. An explicit port now survives
// the environment — so prove it does NOT survive test mode's refusals.
func TestExplicitPortDoesNotBypassTestModeGuards(t *testing.T) {
	t.Run("explicit production port is still refused", func(t *testing.T) {
		t.Setenv("BEADS_TEST_MODE", "1")
		cfg := &Config{Path: "/tmp/x", Database: "d", ServerHost: "127.0.0.1", ServerPort: DefaultSQLPort}
		applyConfigDefaults(cfg)
		if cfg.ServerPort != 1 {
			t.Fatalf("an explicitly-set production port bypassed the test-mode guard: got %d", cfg.ServerPort)
		}
	})

	t.Run("explicit port on a non-local host is still refused", func(t *testing.T) {
		t.Setenv("BEADS_TEST_MODE", "1")
		// RFC 5737 TEST-NET-1 — documentation space, names no real host.
		cfg := &Config{Path: "/tmp/x", Database: "d", ServerHost: "192.0.2.10", ServerPort: 43211}
		applyConfigDefaults(cfg)
		if cfg.ServerPort != 1 {
			t.Fatalf("an explicitly-set port reached a non-local host in test mode: got %d", cfg.ServerPort)
		}
	})
}

// The hq-27t protection must survive the precedence change (aegis-4mzlq).
//
// `TestApplyConfigDefaults_EnvOverridesConfig` was written for hq-27t: an
// orchestrator sets BEADS_DOLT_PORT to steer bd to a test server when
// metadata.json names the production port. That protection is real and must not
// regress. But it is delivered by the RESOLVERS, not by the unconditional
// assignment that was here — `doltserver.DefaultConfig`, `bootstrap.go`'s port
// resolution, `open.go` and `cmd/bd/main.go` all consult
// BEADS_DOLT_SERVER_PORT/BEADS_DOLT_PORT first and return early. A
// metadata-derived port therefore reaches this function only as ServerPort == 0,
// already env-resolved.
//
// So the old test constructed a state no live caller produces (a config-file
// value arriving as an explicit field) and read it as a deliberate choice. This
// test covers the scenario it was actually protecting, through the shape real
// callers use.
func TestOrchestratorCanStillSteerBdAwayFromProduction(t *testing.T) {
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")
	t.Setenv("BEADS_DOLT_PORT", "19999")

	// Real callers arrive with ServerPort unresolved; the port a config file
	// would have supplied has not been applied yet.
	cfg := &Config{Path: "/tmp/x", Database: "d", ServerHost: "127.0.0.1"}
	applyConfigDefaults(cfg)

	if cfg.ServerPort != 19999 {
		t.Fatalf("an orchestrator can no longer steer bd to its test server: got %d, want 19999", cfg.ServerPort)
	}
	if cfg.ServerPort == DefaultSQLPort {
		t.Fatalf("bd resolved to the production port with a test port exported")
	}
}
