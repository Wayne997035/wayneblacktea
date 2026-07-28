package sqlite_test

// Tests for DecisionStore.SearchByCosine's capability-error behaviour on
// SQLite. See decision.ErrCosineUnsupported's doc comment
// (internal/decision/decision.go) and internal/storage/sqlite/decision.go's
// SearchByCosine doc comment for why: the SQLite decisions table has no
// embedding column, so this method must never issue SQL against it.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
)

// TestDecisionStore_SearchByCosine_ReturnsCapabilityError verifies that a
// well-formed call (non-empty embedding, positive limit — the shape that
// used to reach the doomed SQL query) returns decision.ErrCosineUnsupported,
// checkable via errors.Is, and does not panic.
func TestDecisionStore_SearchByCosine_ReturnsCapabilityError(t *testing.T) {
	_, s := openDecisionDB(t, ":memory:", "")

	rows, err := s.SearchByCosine(context.Background(), []float32{0.1, 0.2, 0.3}, 10)
	if err == nil {
		t.Fatal("expected decision.ErrCosineUnsupported, got nil error")
	}
	if !errors.Is(err, decision.ErrCosineUnsupported) {
		t.Fatalf("errors.Is(err, decision.ErrCosineUnsupported) = false; err = %v", err)
	}
	if rows != nil {
		t.Errorf("expected nil rows alongside the capability error, got %+v", rows)
	}
}

// TestDecisionStore_SearchByCosine_EdgeCaseInputs verifies the capability
// error is returned regardless of input shape — including the
// zero-embedding / non-positive-limit inputs that the pre-fix implementation
// used to short-circuit on before ever reaching SQL. The capability gap is
// unconditional, so the error must be too: a caller must not be able to
// dodge the sentinel by passing a degenerate query and get back (nil, nil)
// looking like "searched, found nothing".
func TestDecisionStore_SearchByCosine_EdgeCaseInputs(t *testing.T) {
	_, s := openDecisionDB(t, ":memory:", "")
	ctx := context.Background()

	cases := []struct {
		name  string
		vec   []float32
		limit int
	}{
		{"empty embedding", nil, 10},
		{"zero limit", []float32{0.1}, 0},
		{"negative limit", []float32{0.1}, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := s.SearchByCosine(ctx, tc.vec, tc.limit)
			if !errors.Is(err, decision.ErrCosineUnsupported) {
				t.Fatalf("errors.Is(err, decision.ErrCosineUnsupported) = false; err = %v", err)
			}
			if rows != nil {
				t.Errorf("expected nil rows, got %+v", rows)
			}
		})
	}
}

// TestDecisionStore_SearchByCosine_NoSQLIssued proves SearchByCosine never
// touches the database: dropping the decisions table out from under the
// store and then calling SearchByCosine must still return
// decision.ErrCosineUnsupported, not a "no such table: decisions" SQL error.
// If SearchByCosine were still issuing a SELECT, this test would fail with a
// SQL error instead of the capability sentinel.
func TestDecisionStore_SearchByCosine_NoSQLIssued(t *testing.T) {
	d, s := openDecisionDB(t, ":memory:", "")
	ctx := context.Background()

	if err := d.ExecContext(ctx, `DROP TABLE decisions`); err != nil {
		t.Fatalf("drop decisions table (test setup): %v", err)
	}

	rows, err := s.SearchByCosine(ctx, []float32{0.1, 0.2}, 5)
	if !errors.Is(err, decision.ErrCosineUnsupported) {
		t.Fatalf("errors.Is(err, decision.ErrCosineUnsupported) = false; err = %v (want capability "+
			"sentinel even with the decisions table dropped, proving no SQL is issued)", err)
	}
	if rows != nil {
		t.Errorf("expected nil rows, got %+v", rows)
	}
}

// TestDecisionStore_SearchByCosine_ErrorDoesNotLeakInternals verifies the
// error message stays generic — no DSN, file path, or schema/column detail
// (backend-security-design.md §2 threat surface note in the dispatch: a
// capability error must not leak storage internals).
func TestDecisionStore_SearchByCosine_ErrorDoesNotLeakInternals(t *testing.T) {
	_, s := openDecisionDB(t, ":memory:", "")

	_, err := s.SearchByCosine(context.Background(), []float32{0.1}, 1)
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	forbidden := []string{":memory:", ".db", "embedding IS NOT NULL", "no such column"}
	for _, f := range forbidden {
		if strings.Contains(msg, f) {
			t.Errorf("error message %q leaks internal detail %q", msg, f)
		}
	}
}
