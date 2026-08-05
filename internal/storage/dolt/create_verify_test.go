package dolt

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// The acceptance test for aegis-nl5hc, in the shape the bead demands: a create
// whose write is made NOT to land must REPORT FAILURE. A create that succeeds
// proves nothing here — that is the path that already worked, and the whole
// defect is that the failing path was indistinguishable from it.
//
// The loss is injected by deleting the row after the create, which is the
// observable end state of the measured bug (aegis-364b: the id was minted, the
// event write failed, and afterwards the issue did not exist). Injecting it this
// way rather than by faking an error keeps the test honest about WHAT IS BEING
// CHECKED: not "did an error occur", but "does the store hold the row".
func TestCreateReportsFailureWhenTheStoreDoesNotHoldTheIssue(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	issue := &types.Issue{
		ID:        "verify-lost-write",
		Title:     "a write that does not land",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
	}
	if err := store.CreateIssue(ctx, issue, "tester"); err != nil {
		t.Fatalf("CONTROL FAILED: the ordinary create path must succeed, got: %v", err)
	}

	// CONTROL: verification passes while the row is present. Without this, a
	// verifier that always errored would pass the assertion below.
	if err := store.verifyCreated(ctx, issue); err != nil {
		t.Fatalf("CONTROL FAILED: verifyCreated must accept a row that IS present: %v", err)
	}

	// Now make the write not have landed.
	if _, err := store.db.ExecContext(ctx, "DELETE FROM issues WHERE id = ?", issue.ID); err != nil {
		t.Fatalf("could not inject the lost write: %v", err)
	}

	err := store.verifyCreated(ctx, issue)
	if err == nil {
		t.Fatal("create verified a write the store does not hold — this is the aegis-nl5hc defect: " +
			"a failed write reporting success")
	}
	// The message has to be actionable by whoever reads it at 3am: it must name
	// the id and say plainly that nothing was created, because the caller's next
	// move (retry? treat as filed?) depends on that distinction.
	for _, want := range []string{issue.ID, "STORE DOES NOT HOLD IT", "do not treat this id as filed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must contain %q, got: %v", want, err)
		}
	}
}

// A batch create must not be able to report success while one of its ids is
// missing. The single-issue path is the one that was measured, but `bd create`
// reaches the batch path too, and a check that covers only the measured path is
// the guard-on-one-door failure this fleet keeps finding.
func TestBatchCreateVerifiesEveryID(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	issues := []*types.Issue{
		{ID: "test-verify-batch-1", Title: "one", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask},
		{ID: "test-verify-batch-2", Title: "two", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask},
	}
	if err := store.CreateIssues(ctx, issues, "tester"); err != nil {
		t.Fatalf("CONTROL FAILED: the ordinary batch create must succeed, got: %v", err)
	}
	if err := store.verifyCreated(ctx, issues...); err != nil {
		t.Fatalf("CONTROL FAILED: verifyCreated must accept rows that ARE present: %v", err)
	}

	// Lose only the SECOND one — a verifier that checked just the first id, or
	// just "any row exists", would pass.
	if _, err := store.db.ExecContext(ctx, "DELETE FROM issues WHERE id = ?", issues[1].ID); err != nil {
		t.Fatalf("could not inject the lost write: %v", err)
	}
	err := store.verifyCreated(ctx, issues...)
	if err == nil {
		t.Fatal("batch create verified a set with a missing id")
	}
	if !strings.Contains(err.Error(), issues[1].ID) {
		t.Errorf("error must name the MISSING id %q, got: %v", issues[1].ID, err)
	}
	if strings.Contains(err.Error(), issues[0].ID) {
		t.Errorf("error must not blame the id that DID land (%q): %v", issues[0].ID, err)
	}
}
