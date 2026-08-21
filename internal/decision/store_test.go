package decision_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/sanitize"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// fakeDBTX is a minimal db.DBTX stand-in whose QueryRow always returns a
// distinguishable sentinel error — enough for TestLog_CleanRationale below
// to prove clean input reaches the DB call at all (instead of getting stuck
// at ValidateNoTagNoise), without needing a real connection. Exec/Query are
// never called by decision.Store.Log (see internal/db/decision.sql.go —
// CreateDecision only calls QueryRow) so they're stubbed to satisfy db.DBTX.
type fakeDBTX struct{}

func (fakeDBTX) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errFakeDBTXUnused
}

func (fakeDBTX) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errFakeDBTXUnused
}

func (fakeDBTX) QueryRow(context.Context, string, ...any) pgx.Row {
	return fakeErrRow{}
}

var errFakeDBTXUnused = errors.New("fakeDBTX: Exec/Query not expected to be called by decision.Store.Log")

// fakeErrRow is a pgx.Row whose Scan always fails with a sentinel error
// distinguishable from sanitize.ErrTagNoise.
type fakeErrRow struct{}

func (fakeErrRow) Scan(...any) error { return errFakeRowScan }

var errFakeRowScan = errors.New("fakeErrRow: no real row")

// TestLog_TagNoiseRejection_ReportsFieldAndExcerpt is U2's "decision store
// test" (see the mcp-surface spec's Lane file-ownership check — Lane C owns
// internal/sanitize/, so it also covers this one caller-side test).
// decision.Store.Log validates every free-text field with
// sanitize.ValidateNoTagNoise BEFORE issuing any query (store.go:43-60), so
// this exercises the real fmt.Errorf("log_decision: rationale %w", err) wrap
// with no DB connection needed — a nil DBTX never gets called.
func TestLog_TagNoiseRejection_ReportsFieldAndExcerpt(t *testing.T) {
	store := decision.NewStore(nil, nil)

	_, err := store.Log(context.Background(), decision.LogParams{
		Title:     "ok",
		Rationale: "see </invoke> tag",
		Source:    decision.SourceManual,
	})
	if err == nil {
		t.Fatal("expected tag-noise error, got nil")
	}
	if !errors.Is(err, sanitize.ErrTagNoise) {
		t.Errorf("errors.Is(err, sanitize.ErrTagNoise) = false, want true (err: %v)", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "rationale") {
		t.Errorf("error message %q does not contain field name %q", msg, "rationale")
	}
	if !strings.Contains(msg, "</invoke>") {
		t.Errorf("error message %q does not contain the matched fragment %q", msg, "</invoke>")
	}
}

// TestLog_CleanRationale_NoTagNoiseError is the positive control for the
// test above (backend-security-design.md checker discipline — a checker
// must be proven to NOT fire on good input, not just to fire on bad input):
// clean text in every field must pass ValidateNoTagNoise and reach the DB
// call. It still fails there (fakeDBTX has no real row), but that failure
// must be errFakeRowScan, never sanitize.ErrTagNoise.
func TestLog_CleanRationale_NoTagNoiseError(t *testing.T) {
	store := decision.NewStore(fakeDBTX{}, nil)

	_, err := store.Log(context.Background(), decision.LogParams{
		Title:     "ok",
		Rationale: "a perfectly normal rationale",
		Source:    decision.SourceManual,
	})
	if errors.Is(err, sanitize.ErrTagNoise) {
		t.Errorf("clean rationale must not trip ErrTagNoise, got: %v", err)
	}
	if !errors.Is(err, errFakeRowScan) {
		t.Errorf("expected the call to reach the DB layer (errFakeRowScan), got: %v", err)
	}
}
