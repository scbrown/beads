//go:build !windows

package testutil

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql" // required by testcontainers Dolt module
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/dolt"
)

// doltServer represents a running test Dolt container instance.
type doltServer struct {
	container *dolt.DoltContainer
}

// serverStartTimeout is the max time to wait for the test Dolt server to accept connections.
const serverStartTimeout = 60 * time.Second

// Module-level singleton state.
var (
	doltServerOnce    sync.Once
	doltServerErr     error
	doltTestPort      string
	doltSingletonSrv  *doltServer
	doltTerminateOnce sync.Once
	dockerOnce        sync.Once
	dockerAvail       bool
	doltCheckOnce     sync.Once
	doltCached        doltReadiness
)

// doltReadiness describes why Dolt integration tests can or cannot run.
type doltReadiness int

// doltDockerRepo is the repository portion of DoltDockerImage (without the tag).
var doltDockerRepo, _, _ = strings.Cut(DoltDockerImage, ":")

const (
	doltNoDocker     doltReadiness = iota // Docker daemon not reachable
	doltNoImage                           // no Dolt image at all
	doltWrongVersion                      // image exists but wrong tag
	doltSkipped                           // explicit opt-out via BEADS_TEST_SKIP
	doltReady                             // ready to start containers
)

func (d doltReadiness) String() string {
	switch d {
	case doltNoDocker:
		return "Docker not available"
	case doltNoImage:
		return fmt.Sprintf("Docker image %s not cached locally (run 'docker pull %s')", DoltDockerImage, DoltDockerImage)
	case doltWrongVersion:
		return fmt.Sprintf("Docker image %s cached but wrong version (run 'docker pull %s')", doltDockerRepo, DoltDockerImage)
	case doltSkipped:
		return "Dolt tests skipped (BEADS_TEST_SKIP=dolt)"
	case doltReady:
		return "Dolt ready"
	default:
		return fmt.Sprintf("unknown dolt readiness state: %d", int(d))
	}
}

// isDockerAvailable returns true if the Docker daemon is reachable.
// The result is cached after the first call.
func isDockerAvailable() bool {
	dockerOnce.Do(func() {
		dockerAvail = exec.Command("docker", "info").Run() == nil
	})
	return dockerAvail
}

// hasTestSkip returns true if the given service appears in the BEADS_TEST_SKIP
// env var (comma-separated list). Example: BEADS_TEST_SKIP=dolt,slow
func hasTestSkip(service string) bool {
	val := os.Getenv("BEADS_TEST_SKIP")
	if val == "" {
		return false
	}
	for _, s := range strings.Split(val, ",") {
		if strings.TrimSpace(s) == service {
			return true
		}
	}
	return false
}

// checkDolt returns the readiness state for Dolt integration tests.
// It composes hasTestSkip, isDockerAvailable, isDoltImageCached, and
// isDoltRepoImageCached, caching the result.
func checkDolt() doltReadiness {
	doltCheckOnce.Do(func() {
		// Explicit skip checked first to avoid ~1s docker info cost.
		if hasTestSkip("dolt") {
			doltCached = doltSkipped
			return
		}
		if !isDockerAvailable() {
			return // doltCached zero value is doltNoDocker
		}
		if isDoltImageCached() {
			doltCached = doltReady
			return
		}
		if isDoltRepoImageCached() {
			doltCached = doltWrongVersion
			return
		}
		doltCached = doltNoImage
	})
	return doltCached
}

// isDoltImageCached returns true if the exact Dolt Docker image (repo:tag)
// is available locally, avoiding unnecessary network calls to Docker Hub.
func isDoltImageCached() bool {
	return exec.Command("docker", "image", "inspect", DoltDockerImage).Run() == nil
}

// isDoltRepoImageCached returns true if ANY version of the Dolt image repo
// exists locally (e.g. dolthub/dolt-sql-server with a different tag).
func isDoltRepoImageCached() bool {
	out, err := exec.Command("docker", "images", doltDockerRepo, "-q").Output()
	return err == nil && len(strings.TrimSpace(string(out))) > 0
}

// startDoltContainer starts the singleton Dolt container.
func startDoltContainer() error {
	ctx, cancel := context.WithTimeout(context.Background(), serverStartTimeout)
	defer cancel()

	ctr, err := dolt.Run(ctx, DoltDockerImage,
		dolt.WithDatabase("beads_test"),
		// Docker port-forwarding makes connections appear as non-localhost
		// (e.g., 172.17.0.1). The entrypoint defaults DOLT_ROOT_HOST to
		// "localhost", so root@localhost won't match external connections.
		// Set to "%" so root can connect from any host.
		testcontainers.WithEnv(map[string]string{"DOLT_ROOT_HOST": "%"}),
	)
	if err != nil {
		return fmt.Errorf("starting Dolt container: %w", err)
	}

	p, err := ctr.MappedPort(ctx, "3306/tcp")
	if err != nil {
		_ = testcontainers.TerminateContainer(ctr)
		return fmt.Errorf("getting mapped port: %w", err)
	}

	if _, err := strconv.Atoi(p.Port()); err != nil {
		_ = testcontainers.TerminateContainer(ctr)
		return fmt.Errorf("parsing port %q: %w", p.Port(), err)
	}

	doltTestPort = p.Port()
	doltSingletonSrv = &doltServer{
		container: ctr,
	}

	return nil
}

// terminateSharedContainer stops and removes the shared Dolt container.
// Safe to call concurrently or multiple times (sync.Once).
func terminateSharedContainer() {
	doltTerminateOnce.Do(func() {
		if doltSingletonSrv != nil && doltSingletonSrv.container != nil {
			_ = testcontainers.TerminateContainer(doltSingletonSrv.container)
			doltSingletonSrv.container = nil
		}
	})
}

// StartIsolatedDoltContainer starts a per-test Dolt container and returns the
// mapped host port. The container is terminated automatically when the test finishes.
func StartIsolatedDoltContainer(t *testing.T) string {
	t.Helper()
	if state := checkDolt(); state != doltReady {
		t.Skipf("skipping test: %s", state)
	}

	ctx, cancel := context.WithTimeout(context.Background(), serverStartTimeout)
	defer cancel()
	ctr, err := dolt.Run(ctx, DoltDockerImage,
		dolt.WithDatabase("beads_test"),
		testcontainers.WithEnv(map[string]string{"DOLT_ROOT_HOST": "%"}),
	)
	if err != nil {
		t.Fatalf("starting Dolt container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(ctr); err != nil {
			t.Logf("terminating Dolt container: %v", err)
		}
	})

	port, err := ctr.MappedPort(ctx, "3306/tcp")
	if err != nil {
		t.Fatalf("getting mapped port: %v", err)
	}

	portStr := port.Port()
	t.Setenv("BEADS_DOLT_PORT", portStr)
	return portStr
}

// ensureSharedContainer starts the singleton container and sets BEADS_DOLT_PORT.
func ensureSharedContainer() {
	doltServerOnce.Do(func() {
		doltServerErr = startDoltContainer()
		if doltServerErr == nil && doltTestPort != "" {
			if err := os.Setenv("BEADS_DOLT_PORT", doltTestPort); err != nil {
				doltServerErr = fmt.Errorf("set BEADS_DOLT_PORT: %w", err)
			}
		}
	})
}

// EnsureDoltContainerForTestMain starts a shared Dolt container for use in
// TestMain functions. Call TerminateDoltContainer() after m.Run() to clean up.
// Sets BEADS_DOLT_PORT process-wide.
// externalDoltPort reads BEADS_TEST_DOLT_PORT: an ALREADY-RUNNING Dolt
// sql-server to test against instead of starting a container.
//
// WHY THIS SEAM EXISTS (aegis-nl5hc). The Dolt tests are gated on Docker, and
// this fleet's crew host has no Docker. The consequence is not that a few tests
// are missing — it is that `go test ./internal/storage/dolt/` prints **ok** here
// while every storage test SKIPS. A suite that reports success without
// exercising the storage layer is the same defect this bead is about, one level
// up: a report about what was ATTEMPTED rather than what was CHECKED.
//
// The server must be one the caller started for testing. Nothing here can tell
// a scratch server from a production one, so the guard is the same as the
// container path's: tests create their own uniquely-named database and never
// touch an existing one.
func externalDoltPort() string {
	return strings.TrimSpace(os.Getenv("BEADS_TEST_DOLT_PORT"))
}

// assertLoopbackTestServer REFUSES to run the suite unless the Dolt the tests
// will reach is on this machine.
//
// WHY A REFUSAL AND NOT A DOC (aegis-nl5hc). The environment that decides where
// tests connect is inherited: a crew host exports host and port pointing at a
// live shared store, so a harness that pins only ONE of them silently aims a
// test suite — which creates databases, writes rows and DELETEs them — at
// production. Measured: pinning the port alone produced connections to the
// production HOST on the scratch port.
//
// A harness that can be pointed at a live store by omitting one variable has a
// safety catch made of attentiveness. This is the catch made of code: whatever
// the environment says, if the resolved host is not loopback the suite does not
// run.
//
// Loopback rather than a named production host on purpose. Naming the estate's
// hosts in a public repo is its own leak (there is a guard in this tree for
// exactly that), and an allowlist of "known bad" hosts fails open for every
// host nobody thought of. "A test server is local" fails CLOSED.
func assertLoopbackTestServer() error {
	for _, key := range []string{"BEADS_DOLT_SERVER_HOST", "BEADS_DOLT_HOST"} {
		host := strings.TrimSpace(os.Getenv(key))
		if host == "" {
			continue
		}
		if !isLoopbackHost(host) {
			return fmt.Errorf(
				"REFUSING to run tests: %s resolves to %q, which is not loopback. "+
					"This suite creates databases, writes rows and deletes them; against a "+
					"shared store that is data loss. Point it at a local dolt sql-server "+
					"(aegis-nl5hc)", key, host)
		}
	}
	return nil
}

// isLoopbackHost reports whether a host string names this machine.
func isLoopbackHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	// A name we cannot parse as an IP is not demonstrably local. Refuse rather
	// than resolve it: DNS would make the answer depend on the network, and the
	// safe reading of "I am not sure" here is no.
	return false
}

func EnsureDoltContainerForTestMain() error {
	if port := externalDoltPort(); port != "" {
		doltTestPort = port
		// BOTH port variables, and the second one is the one that matters.
		// `applyConfigDefaults` resolves the store's port from
		// BEADS_DOLT_SERVER_PORT; on a crew host that is already set to the
		// fleet's production port, so an explicit cfg.ServerPort is overwritten
		// and the store dials PRODUCTION while the test believes it is talking
		// to a scratch server. It surfaces as "server unreachable at :3306",
		// which reads as "your test server is down" rather than "your test was
		// pointed at prod" — the more dangerous of the two readings, and the
		// reason this sets it rather than documenting it.
		for k, v := range map[string]string{
			"BEADS_DOLT_PORT":        port,
			"BEADS_DOLT_SERVER_PORT": port,
			"BEADS_DOLT_SERVER_HOST": "127.0.0.1",
			"BEADS_DOLT_HOST":        "127.0.0.1",
		} {
			if err := os.Setenv(k, v); err != nil {
				// Refuse rather than run: a partially-pinned environment is the
				// dangerous state, not a safe one — it is what aims tests at
				// production.
				return fmt.Errorf("pinning %s for the external test server: %w", k, err)
			}
		}
		// And the HOST, for the same reason and with a worse failure mode: a
		// crew host has BEADS_DOLT_SERVER_HOST pointing at a live shared Dolt,
		// so pinning only the port aims the tests at PRODUCTION-on-a-scratch-
		// port. Measured while building this seam: the tests dialed the
		// production host on the scratch port, and would have dialed it on the
		// production port had the port not been pinned first. Both variables
		// have to be overridden or neither is safe.
		//
		// Pinning is not enough on its own. It is one edit away from being
		// half-applied again, and the failure is silent in the direction of
		// writing to a live store — so the pin is VERIFIED, not trusted.
		return assertLoopbackTestServer()
	}
	if state := checkDolt(); state != doltReady {
		return fmt.Errorf("%s", state)
	}

	ensureSharedContainer()
	return doltServerErr
}

// RequireDoltContainer ensures a shared Dolt container is running. Skips the
// test if Docker is not available.
func RequireDoltContainer(t *testing.T) {
	t.Helper()
	if state := checkDolt(); state != doltReady {
		t.Skipf("skipping test: %s", state)
	}

	ensureSharedContainer()
	if doltServerErr != nil {
		t.Fatalf("Dolt container setup failed: %v", doltServerErr)
	}
}

// DoltContainerAddr returns the address (host:port) of the Dolt container.
func DoltContainerAddr() string {
	return "127.0.0.1:" + doltTestPort
}

// DoltContainerPort returns the mapped host port of the Dolt container.
func DoltContainerPort() string {
	return doltTestPort
}

// DoltContainerPortInt returns the mapped host port as an int.
func DoltContainerPortInt() int {
	p, _ := strconv.Atoi(doltTestPort)
	return p
}

// TerminateDoltContainer stops and removes the shared Dolt container.
// Called from TestMain after m.Run().
func TerminateDoltContainer() {
	// An externally-supplied server is not ours to stop — the caller started it
	// and may be running several packages against it.
	if externalDoltPort() != "" {
		return
	}
	terminateSharedContainer()
}

// DoltContainerCrashed returns true if the shared container has exited unexpectedly.
// Returns false if no container was started.
func DoltContainerCrashed() bool {
	if doltSingletonSrv == nil || doltSingletonSrv.container == nil {
		return false
	}
	state, err := doltSingletonSrv.container.State(context.Background())
	if err != nil {
		return true // can't check state — assume crashed
	}
	return !state.Running
}

// DoltContainerCrashError returns an error if the shared container has exited
// unexpectedly, nil otherwise.
func DoltContainerCrashError() error {
	if doltSingletonSrv == nil || doltSingletonSrv.container == nil {
		return nil
	}
	state, err := doltSingletonSrv.container.State(context.Background())
	if err != nil {
		return fmt.Errorf("failed to check container state: %w", err)
	}
	if !state.Running {
		return fmt.Errorf("Dolt container exited (status=%s, exit=%d)", state.Status, state.ExitCode)
	}
	return nil
}

// ── A suite that evaluated nothing must not print `ok` (aegis-nl5hc) ────────
//
// `go test` exits 0 when every test SKIPS, and `ok <pkg>` is what a human and a
// CI grep both read as "the storage layer passed". On a Docker-less host that
// is the whole Dolt suite: 100% skipped, exit 0, indistinguishable from 100%
// passed. It is the same defect as the tool this bead is about — a report about
// what was attempted rather than what was checked — and it is worse here,
// because it is the instrument someone would use to VERIFY that tool.
//
// Counters, not heuristics: a test that reaches a store increments Ran, one
// that gives up increments Skipped, and the package's TestMain calls
// FailIfNothingRan before returning.

var (
	testsRan     int
	testsSkipped int
	testCountMu  sync.Mutex
)

// RecordTestRan marks that a test actually reached a live store.
func RecordTestRan() {
	testCountMu.Lock()
	defer testCountMu.Unlock()
	testsRan++
}

// RecordTestSkipped marks that a test gave up before reaching a store.
func RecordTestSkipped() {
	testCountMu.Lock()
	defer testCountMu.Unlock()
	testsSkipped++
}

// FailIfNothingRan converts "everything skipped" into a FAILING exit code, so a
// suite that evaluated nothing cannot print `ok`.
//
// Returns the code TestMain should exit with. A non-zero `code` is passed
// through untouched — a real failure must not be relabelled by this.
func FailIfNothingRan(code int) int {
	testCountMu.Lock()
	ran, skipped := testsRan, testsSkipped
	testCountMu.Unlock()

	if code != 0 {
		return code
	}
	if ran == 0 && skipped > 0 {
		fmt.Fprintf(os.Stderr,
			"\nFAIL: %d test(s) SKIPPED and ZERO ran — this suite evaluated NOTHING.\n"+
				"      `ok` here would be indistinguishable from a passing run (aegis-nl5hc).\n"+
				"      Start a local dolt sql-server and set BEADS_TEST_DOLT_PORT, or set\n"+
				"      BEADS_TEST_SKIP=dolt to declare the skip deliberately.\n", skipped)
		return 1
	}
	if ran > 0 && skipped > 0 {
		// Not a failure, but the count must be visible: a partial skip is how a
		// suite quietly stops covering the thing you are about to trust it for.
		fmt.Fprintf(os.Stderr, "NOTE: %d test(s) ran, %d skipped.\n", ran, skipped)
	}
	return code
}
