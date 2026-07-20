package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

// openWorkSessionMem opens an in-memory SQLite DB and returns both the raw
// *sqlite.DB handle (for fixture inserts that bypass the store API) and a
// WorkSessionStore wrapping it.
func openWorkSessionMem(t *testing.T, workspaceID string) (*sqlite.WorkSessionStore, *sqlite.DB) {
	t.Helper()
	d, err := sqlite.Open(context.Background(), ":memory:", workspaceID)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return sqlite.NewWorkSessionStore(d), d
}

// insertRawEvidence inserts a work_session_evidence row directly via SQL,
// bypassing WorkSessionStore.AddEvidence's workspace stamping, so tests can
// simulate rows written before the zero-UUID legacy stamp existed (SQL NULL
// workspace_id, passed as workspaceIDArg=nil).
func insertRawEvidence(t *testing.T, d *sqlite.DB, sessionID uuid.UUID, workspaceIDArg any, evidenceType string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	const q = `INSERT INTO work_session_evidence
		(id, workspace_id, session_id, evidence_type, status, command, artifact, output_excerpt, created_at)
		VALUES (?1,?2,?3,?4,?5,?6,?7,?8,?9)`
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00")
	if err := d.ExecContext(context.Background(), q,
		id.String(), workspaceIDArg, sessionID.String(), evidenceType, "passed", nil, nil, nil, now,
	); err != nil {
		t.Fatalf("insertRawEvidence: %v", err)
	}
	return id
}

// TestWorkSessionStore_GetEvidence_LegacyMode_ReadsLegacyNullAndZeroUUIDRows
// verifies wbt-2.0 review round2 F3: in legacy mode (no WORKSPACE_ID
// configured), GetEvidence must return both a pre-fix row stamped with SQL
// NULL workspace_id (what legacy-mode AddEvidence wrote before the zero-UUID
// stamp was introduced) and a post-fix row stamped with the zero-UUID
// sentinel. Before this fix, the strict `workspace_id = ?1` equality filter
// made the NULL row permanently invisible with no error.
func TestWorkSessionStore_GetEvidence_LegacyMode_ReadsLegacyNullAndZeroUUIDRows(t *testing.T) {
	store, d := openWorkSessionMem(t, "")
	sessionID := uuid.New()

	nullRowID := insertRawEvidence(t, d, sessionID, nil, "manual_note")
	zeroUUIDRowID := insertRawEvidence(t, d, sessionID, uuid.Nil.String(), "manual_note")

	evidence, err := store.GetEvidence(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("GetEvidence: %v", err)
	}
	found := make(map[uuid.UUID]bool)
	for _, e := range evidence {
		found[e.ID] = true
	}
	if !found[nullRowID] {
		t.Errorf("legacy NULL-workspace_id row %s must be visible in legacy mode, was not returned", nullRowID)
	}
	if !found[zeroUUIDRowID] {
		t.Errorf("zero-UUID-stamped row %s must be visible in legacy mode, was not returned", zeroUUIDRowID)
	}
	if len(evidence) != 2 {
		t.Errorf("expected exactly 2 evidence rows, got %d", len(evidence))
	}
}

// TestWorkSessionStore_GetEvidence_NonLegacyMode_NullRowStaysInvisible verifies
// the isolation half of the same fix: when a real workspace IS configured, a
// NULL-workspace_id row must NOT leak into that workspace's results, while a
// row correctly stamped with the configured workspace IS returned. Non-legacy
// mode intentionally keeps the strict equality-only predicate.
func TestWorkSessionStore_GetEvidence_NonLegacyMode_NullRowStaysInvisible(t *testing.T) {
	wsID := uuid.New()
	store, d := openWorkSessionMem(t, wsID.String())
	sessionID := uuid.New()

	nullRowID := insertRawEvidence(t, d, sessionID, nil, "manual_note")
	ownRowID := insertRawEvidence(t, d, sessionID, wsID.String(), "manual_note")

	evidence, err := store.GetEvidence(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("GetEvidence: %v", err)
	}
	found := make(map[uuid.UUID]bool)
	for _, e := range evidence {
		found[e.ID] = true
	}
	if found[nullRowID] {
		t.Errorf("NULL-workspace_id row %s must NOT be visible in non-legacy mode (isolation regression)", nullRowID)
	}
	if !found[ownRowID] {
		t.Errorf("own-workspace row %s must be visible, was not returned", ownRowID)
	}
}

// TestWorkSessionStore_GetEvidence_NonLegacyMode_OtherWorkspaceRowInvisible is
// the pre-existing isolation guard (not new behaviour from this fix, but a
// regression guard alongside it): a row stamped with a DIFFERENT workspace's
// UUID must not be visible either.
func TestWorkSessionStore_GetEvidence_NonLegacyMode_OtherWorkspaceRowInvisible(t *testing.T) {
	wsID := uuid.New()
	otherWsID := uuid.New()
	store, d := openWorkSessionMem(t, wsID.String())
	sessionID := uuid.New()

	otherRowID := insertRawEvidence(t, d, sessionID, otherWsID.String(), "manual_note")

	evidence, err := store.GetEvidence(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("GetEvidence: %v", err)
	}
	for _, e := range evidence {
		if e.ID == otherRowID {
			t.Errorf("row %s belonging to a different workspace must NOT be visible", otherRowID)
		}
	}
}
