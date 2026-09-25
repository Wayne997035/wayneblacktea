package sqlite_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

// [GTD 1134a216] These pin the WIRING, not the classification — the
// classification has its own tests next to BatchItemErrMsg. What they catch is
// BatchConfirm going back to putting err.Error() in the field, which is a
// change the classification tests would not notice.
//
// The finding was filed about confirm_proposals' reject path: it stays on
// BatchConfirm (no materialiser to run), so F170-08's fix to the sibling
// accept path never covered it.

func openBatchProposalStore(t *testing.T) (*sqlite.DB, *sqlite.ProposalStore) {
	t.Helper()
	d, err := sqlite.OpenTemplated(t, context.Background(), ":memory:", "") // [F0925-09] semantics-preserving template helper
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d, sqlite.NewProposalStore(d)
}

// TestBatchConfirm_UnknownIDReportsTheSentinel is the reverse frame: the one
// error a caller can act on must survive. An implementation that replaced
// every message with the generic text would pass the leak test below.
func TestBatchConfirm_UnknownIDReportsTheSentinel(t *testing.T) {
	t.Parallel() // [F0925-10]
	_, s := openBatchProposalStore(t)
	id := uuid.New()

	res, err := s.BatchConfirm(context.Background(), []uuid.UUID{id}, proposal.StatusRejected)
	if err != nil {
		t.Fatalf("BatchConfirm returned a batch-level error: %v", err)
	}
	if len(res.Results) != 1 {
		t.Fatalf("want 1 result, got %d", len(res.Results))
	}
	if res.Results[0].OK {
		t.Fatal("rejecting an unknown id must not report OK")
	}
	if got := res.Results[0].ErrMsg; got != proposal.ErrNotFound.Error() {
		t.Errorf("ErrMsg = %q, want the sentinel %q — an unknown id is the "+
			"caller's own answer, not an internal failure",
			got, proposal.ErrNotFound.Error())
	}
}

// TestBatchConfirm_StoreFailureDoesNotShipDriverText is the negative frame.
// Closing the DB underneath the store is the cheapest way to make Resolve fail
// with real driver text from outside the package; the finding's own measured
// case ("no such table: pending_proposals") needs the unexported handle to
// reproduce, and this exercises the same code path.
func TestBatchConfirm_StoreFailureDoesNotShipDriverText(t *testing.T) {
	t.Parallel() // [F0925-10]
	d, s := openBatchProposalStore(t)
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	id := uuid.New()
	res, err := s.BatchConfirm(context.Background(), []uuid.UUID{id}, proposal.StatusRejected)
	if err != nil {
		// A batch-level error is also an acceptable shape here — what must not
		// happen is driver text reaching the caller, so check that instead of
		// insisting on one of the two shapes.
		if strings.Contains(err.Error(), "database is closed") {
			t.Fatalf("batch-level error ships driver text: %v", err)
		}
		return
	}
	if len(res.Results) != 1 {
		t.Fatalf("want 1 result, got %d", len(res.Results))
	}
	got := res.Results[0].ErrMsg
	if got == "" {
		t.Fatal("a failed item must still say something; an empty ErrMsg makes " +
			"the failure indistinguishable from success in the JSON")
	}
	for _, leak := range []string{"database is closed", "sql:", "sqlite", "SQL logic error"} {
		if strings.Contains(got, leak) {
			t.Errorf("ErrMsg leaks driver text %q: %q", leak, got)
		}
	}
	// Which item failed is answered by Results[i].ID, not by the message — so
	// assert the id is on the field that owns it and absent from the string
	// every failed item pays for.
	if res.Results[0].ID != id.String() {
		t.Errorf("Results[0].ID = %q, want %q — the caller cannot tell which "+
			"batch item failed", res.Results[0].ID, id.String())
	}
	if strings.Contains(got, id.String()) {
		t.Errorf("ErrMsg repeats the id that Results[0].ID already carries: %q", got)
	}
}
