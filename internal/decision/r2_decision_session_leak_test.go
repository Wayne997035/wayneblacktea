package decision_test

// PR160 round-2 security review, M-3/M-2: proves the JSON leak fix
// (internal/db/models_custom.go, Decision.MarshalJSON) holds on the real
// Store.Log code path for BOTH backends, not just against a hand-built
// db.Decision literal (internal/db's own test covers that). Every case is
// checked twice, mirroring actor_provenance_contract_test.go's rigor: once
// via json.Marshal(row) (the bad case — must not leak), once via an
// independent raw-SQL read-back bypassing the Go struct entirely (the
// positive control — the audit trail must be untouched).
//
// backend-security-design.md §6.5 makes testcontainers PG coverage
// unconditional for any code path touching PG, not conditional on "does PG
// vs SQLite logic differ here" — this file exists specifically to satisfy
// that even though the actual defect (JSON marshaling) is dialect-agnostic
// Go code.

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
	sqlitestore "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
)

const r2LeakProbeSessionID = "mcp-session-r2-leak-probe-decision-package"

// TestDecisionStore_ActorSessionIDJSONLeak_SQLite is the SQLite half of the
// dual-backend pair.
func TestDecisionStore_ActorSessionIDJSONLeak_SQLite(t *testing.T) {
	ctx := context.Background()
	d, err := sqlitestore.Open(ctx, ":memory:", "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = d.Close() }()

	s := sqlitestore.NewDecisionStore(d)
	row, err := s.Log(ctx, decision.LogParams{
		Title:            "R2 leak probe (SQLite)",
		Context:          "ctx",
		Decision:         "dec",
		Rationale:        "rat",
		Source:           decision.SourceManual,
		ActorSessionID:   r2LeakProbeSessionID,
		ConfirmedByHuman: true,
	})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}

	// Bad case: json.Marshal(row) must not leak.
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(raw), r2LeakProbeSessionID) {
		t.Errorf("json.Marshal(Log() return) leaks actor_session_id: %s", raw)
	}
	if strings.Contains(string(raw), "actor_session_id") || strings.Contains(string(raw), "confirmed_by_human") {
		t.Errorf("json.Marshal(Log() return) contains a dropped-field key: %s", raw)
	}

	// Positive control: raw SQL read-back, bypassing scanDecision entirely —
	// the audit trail is untouched by the JSON-layer fix.
	var dbSession sql.NullString
	var dbConfirmed int64
	err = d.QueryRowContext(
		ctx, `SELECT actor_session_id, confirmed_by_human FROM decisions WHERE id = ?1`, row.ID.String(),
	).Scan(&dbSession, &dbConfirmed)
	if err != nil {
		t.Fatalf("raw DB read-back: %v", err)
	}
	if !dbSession.Valid || dbSession.String != r2LeakProbeSessionID {
		t.Errorf("DB actor_session_id = %+v, want Valid=true String=%q", dbSession, r2LeakProbeSessionID)
	}
	if dbConfirmed != 1 {
		t.Errorf("DB confirmed_by_human = %d, want 1", dbConfirmed)
	}
}

// TestDecisionStore_ActorSessionIDJSONLeak_Postgres is the Postgres half via
// testcontainers (backend-security-design.md §6.5).
func TestDecisionStore_ActorSessionIDJSONLeak_Postgres(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	store := decision.NewStore(pool, nil)

	row, err := store.Log(ctx, decision.LogParams{
		Title:            "R2 leak probe (Postgres)",
		Context:          "ctx",
		Decision:         "dec",
		Rationale:        "rat",
		Source:           decision.SourceManual,
		ActorSessionID:   r2LeakProbeSessionID,
		ConfirmedByHuman: true,
	})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	t.Cleanup(func() {
		if _, cleanErr := pool.Exec(ctx, "DELETE FROM decisions WHERE id = $1", row.ID); cleanErr != nil {
			t.Logf("cleanup decision: %v", cleanErr)
		}
	})

	// Bad case: json.Marshal(row) must not leak.
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(raw), r2LeakProbeSessionID) {
		t.Errorf("json.Marshal(Log() return) leaks actor_session_id: %s", raw)
	}
	if strings.Contains(string(raw), "actor_session_id") || strings.Contains(string(raw), "confirmed_by_human") {
		t.Errorf("json.Marshal(Log() return) contains a dropped-field key: %s", raw)
	}

	// Positive control: raw SQL read-back — the audit trail is untouched.
	var dbSession sql.NullString
	var dbConfirmed bool
	err = pool.QueryRow(
		ctx, "SELECT actor_session_id, confirmed_by_human FROM decisions WHERE id = $1", row.ID,
	).Scan(&dbSession, &dbConfirmed)
	if err != nil {
		t.Fatalf("raw DB read-back: %v", err)
	}
	if !dbSession.Valid || dbSession.String != r2LeakProbeSessionID {
		t.Errorf("DB actor_session_id = %+v, want Valid=true String=%q", dbSession, r2LeakProbeSessionID)
	}
	if !dbConfirmed {
		t.Errorf("DB confirmed_by_human = %v, want true", dbConfirmed)
	}
}
