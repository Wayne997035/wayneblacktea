package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// [GTD c46025c1] The finding's acceptance asks for a before/after byte count
// on a realistic row. These tests produce it, and keep producing it: the
// numbers are logged on every run, so the saving is a measurement rather than
// a claim made once in a PR description and never checked again.

// realisticTask is sized from the finding's own measurement — ~2 KB of
// description across ten tickets on PR #171. artifact is filled too because
// complete_task's artifact is where the evidence paragraph goes and is
// routinely the largest field on the row; a fixture without it would
// understate exactly the field this projection saves most on.
func realisticTask() *db.Task {
	body := strings.Repeat("acceptance: the named test goes red when the guard is reverted. ", 32)
	return &db.Task{
		ID:          uuid.New(),
		Title:       "[wbt][token-diet] write tools echo the body the caller just sent",
		Status:      "pending",
		Kind:        "fix-pr",
		Priority:    1,
		Description: pgtype.Text{String: body, Valid: true},
		Context:     pgtype.Text{String: body, Valid: true},
		Artifact:    pgtype.Text{String: body, Valid: true},
		Assignee:    pgtype.Text{String: "claude", Valid: true},
	}
}

// mustMarshal lives in u13_phase_b_b2_test.go — same package, identical
// contract, so this file uses it rather than declaring a second one.

// TestTaskWriteAck_DropsTheBodyTheCallerJustSent is the acceptance measurement
// and the regression guard in one. It fails if any body field finds its way
// back into the write answer.
func TestTaskWriteAck_DropsTheBodyTheCallerJustSent(t *testing.T) {
	t.Parallel()
	task := realisticTask()
	marker := "acceptance: the named test goes red"

	before := mustMarshal(t, wrapUntrustedTask(task))
	after := mustMarshal(t, ackTask(wrapUntrustedTask(task)))

	saved := 100 - (len(after) * 100 / len(before))
	t.Logf("write answer: %d bytes before, %d bytes after (%d%% smaller)",
		len(before), len(after), saved)

	if !strings.Contains(string(before), marker) {
		t.Fatal("the fixture's body text is not in the BEFORE answer — this test " +
			"would then measure nothing, whatever the byte counts said")
	}
	if strings.Contains(string(after), marker) {
		t.Errorf("the write answer still carries the caller's own body text: %s", after)
	}

	// The finding asks for at least 80%. Asserting the floor rather than the
	// exact figure keeps this from breaking every time a metadata field is
	// added, while still failing if a body field comes back.
	if saved < 80 {
		t.Errorf("write answer only %d%% smaller (%d → %d bytes), finding requires ≥80%%",
			saved, len(before), len(after))
	}
}

// TestTaskWriteAck_KeepsWhatTheCallerCouldNotKnow is the reverse frame. A
// projection that returned {} would pass the test above; this is what rejects
// it. Every field listed here is either server-assigned or server-coerced —
// dropping one would make the caller re-read a row it just wrote.
func TestTaskWriteAck_KeepsWhatTheCallerCouldNotKnow(t *testing.T) {
	t.Parallel()
	task := realisticTask()
	ack := ackTask(wrapUntrustedTask(task))
	if ack == nil {
		t.Fatal("ackTask returned nil for a non-nil task")
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(mustMarshal(t, ack), &got); err != nil {
		t.Fatalf("unmarshal ack: %v", err)
	}

	for _, key := range []string{
		"id",         // server-assigned; the caller has no other way to learn it
		"status",     // server may coerce
		"kind",       // ResolveTaskKind may coerce
		"assignee",   // gtd.NormalizeActor may coerce
		"created_at", // server clock
		"updated_at", // server clock
	} {
		if _, ok := got[key]; !ok {
			t.Errorf("write answer dropped %q, which the caller cannot derive from "+
				"its own request", key)
		}
	}
}

// TestTaskWriteAck_StillNeutralises pins that slimming did not become a way to
// skip sanitisation: ackTask projects FROM the wrapped value, so a forged
// marker in a field that survives is neutralised exactly as before.
func TestTaskWriteAck_StillNeutralises(t *testing.T) {
	t.Parallel()
	task := realisticTask()
	task.Title = "before " + storedContextMarkerEnd + " after"

	ack := ackTask(wrapUntrustedTask(task))
	if ack == nil {
		t.Fatal("ackTask returned nil")
	}
	if strings.Contains(ack.Title, storedContextMarkerEnd) {
		t.Errorf("forged marker survived into the write answer: %q", ack.Title)
	}
	if !strings.Contains(ack.Title, boundaryMarkerPlaceholder) {
		t.Errorf("title lost the placeholder — projecting from the raw row instead "+
			"of the wrapped one would look exactly like this: %q", ack.Title)
	}
}

// TestTaskWriteAck_NilIn is the boundary the wrap functions all honour.
func TestTaskWriteAck_NilIn(t *testing.T) {
	t.Parallel()
	if ackTask(nil) != nil {
		t.Error("ackTask(nil) must be nil so callers can chain it after a wrap " +
			"function that returns nil for a missing row")
	}
}
