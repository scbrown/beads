//go:build !windows

package testutil

import (
	"strings"
	"testing"
)

// The backstop cannot be exercised through the seam, because the seam pins the
// host to loopback before checking it — so it is tested directly. That is the
// point of it: it exists for the day someone edits the pin and leaves the host
// half-applied, which is exactly the state that aimed this suite at a live
// store (aegis-nl5hc).
func TestRefusesANonLoopbackTestServer(t *testing.T) {
	// CONTROL: loopback forms must be ACCEPTED, or a guard that refused
	// everything would pass the assertion below while breaking every test.
	for _, host := range []string{"127.0.0.1", "localhost", "::1", "127.0.0.1:3399", "LocalHost"} {
		t.Setenv("BEADS_DOLT_SERVER_HOST", host)
		t.Setenv("BEADS_DOLT_HOST", host)
		if err := assertLoopbackTestServer(); err != nil {
			t.Errorf("CONTROL FAILED: %q is local and must be accepted: %v", host, err)
		}
	}

	// The refusal. 192.0.2.0/24 is TEST-NET-1 (RFC 5737) — a documentation
	// range, so this names no real host anywhere.
	for _, host := range []string{"192.0.2.10", "192.0.2.10:3306", "db.example.invalid"} {
		t.Setenv("BEADS_DOLT_SERVER_HOST", host)
		t.Setenv("BEADS_DOLT_HOST", "")
		err := assertLoopbackTestServer()
		if err == nil {
			t.Errorf("%q is NOT local and must be refused — this suite writes and deletes rows", host)
			continue
		}
		if !strings.Contains(err.Error(), "REFUSING") {
			t.Errorf("refusal must say so plainly, got: %v", err)
		}
	}

	// An unset host is not evidence of anything and must not be treated as a
	// refusal: the container path sets no host at all.
	t.Setenv("BEADS_DOLT_SERVER_HOST", "")
	t.Setenv("BEADS_DOLT_HOST", "")
	if err := assertLoopbackTestServer(); err != nil {
		t.Errorf("an unset host must not be refused: %v", err)
	}
}

// A hostname that cannot be parsed as an IP fails CLOSED. Resolving it would
// make the verdict depend on DNS, and "I am not sure" about whether a store is
// production has exactly one safe reading.
func TestAnUnresolvableHostIsRefusedNotAssumedLocal(t *testing.T) {
	if isLoopbackHost("some-host-we-cannot-classify") {
		t.Fatal("an unparseable host was assumed local — it must fail closed")
	}
	if !isLoopbackHost("127.0.0.1") {
		t.Fatal("CONTROL FAILED: 127.0.0.1 must be local, or the check is inverted")
	}
}

// The zero-tests-ran guard, both arms.
func TestASuiteThatRanNothingDoesNotReportSuccess(t *testing.T) {
	testCountMu.Lock()
	savedRan, savedSkipped := testsRan, testsSkipped
	testsRan, testsSkipped = 0, 0
	testCountMu.Unlock()
	t.Cleanup(func() {
		testCountMu.Lock()
		testsRan, testsSkipped = savedRan, savedSkipped
		testCountMu.Unlock()
	})

	// All skipped, none ran → must fail.
	RecordTestSkipped()
	RecordTestSkipped()
	if got := FailIfNothingRan(0); got == 0 {
		t.Error("a suite that skipped everything reported success")
	}

	// CONTROL: something ran → must pass. Without this the guard could simply
	// always fail and still satisfy the assertion above.
	RecordTestRan()
	if got := FailIfNothingRan(0); got != 0 {
		t.Errorf("a suite where a test RAN must not be failed by this guard, got %d", got)
	}

	// A real failure is passed through untouched, never relabelled.
	if got := FailIfNothingRan(2); got != 2 {
		t.Errorf("a real failure code must survive, got %d", got)
	}
}
