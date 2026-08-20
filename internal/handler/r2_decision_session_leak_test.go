package handler_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// --- PR160 round-2 security review, M-3/M-2: GET /api/decisions must not
// leak actor_session_id or confirmed_by_human — decision_handler.go's
// ListDecisions returns []db.Decision straight through c.JSON with no
// per-handler wrapper (unlike workspace_overview_handler.go /
// dashboard_handler.go, which already project onto their own response
// types), so this endpoint is the most direct reproduction of the leak. ---

// TestListDecisions_HTTP_DoesNotLeakActorSessionID is the HTTP-side bad case
// from PR160's acceptance criteria: the JSON body from GET /api/decisions
// must not contain the actor_session_id key (in any of the three list
// filters the handler supports), while the underlying store row is
// untouched — proven via the fake store's own recorded value, which stands
// in for "the DB row is unaffected" at the handler-test layer (the DB-level
// proof lives in internal/decision's testcontainers PG + SQLite tests).
func TestListDecisions_HTTP_DoesNotLeakActorSessionID(t *testing.T) {
	const leakedSessionID = "mcp-session-http-leak-probe"

	rowWithSecrets := db.Decision{
		ID:               uuid.New(),
		Title:            "http leak probe",
		Context:          "ctx",
		Decision:         "dec",
		Rationale:        "rat",
		Source:           "manual",
		ActorSessionID:   pgtype.Text{String: leakedSessionID, Valid: true},
		ConfirmedByHuman: true,
	}

	cases := []struct {
		name  string
		query string
		wire  func(store *fakeDecisionHandlerStore)
	}{
		{
			name:  "All (no filter)",
			query: "",
			wire:  func(store *fakeDecisionHandlerStore) { store.all = []db.Decision{rowWithSecrets} },
		},
		{
			name:  "ByRepo",
			query: "?repo_name=wayneblacktea",
			wire:  func(store *fakeDecisionHandlerStore) { store.byRepo = []db.Decision{rowWithSecrets} },
		},
		{
			name:  "ByProject",
			query: "?project_id=" + uuid.New().String(),
			wire:  func(store *fakeDecisionHandlerStore) { store.byProj = []db.Decision{rowWithSecrets} },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeDecisionHandlerStore{}
			tc.wire(store)
			e := newEcho()
			h := handler.NewDecisionHandler(store)
			e.GET("/api/decisions", h.ListDecisions)
			rec := performRequest(e, http.MethodGet, "/api/decisions"+tc.query, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if strings.Contains(body, leakedSessionID) {
				t.Errorf("response leaks the raw actor_session_id value %q: %s", leakedSessionID, body)
			}
			if strings.Contains(body, "actor_session_id") {
				t.Errorf("response contains the actor_session_id key: %s", body)
			}
			if strings.Contains(body, "confirmed_by_human") {
				t.Errorf("response contains the confirmed_by_human key: %s", body)
			}

			// Positive control: the store row itself (standing in for the DB
			// row) still carries the audit identity — only the wire response
			// dropped it.
			var stored []db.Decision
			switch tc.name {
			case "All (no filter)":
				stored = store.all
			case "ByRepo":
				stored = store.byRepo
			case "ByProject":
				stored = store.byProj
			}
			if len(stored) != 1 || !stored[0].ActorSessionID.Valid || stored[0].ActorSessionID.String != leakedSessionID {
				t.Errorf("audit trail broken: store row ActorSessionID = %+v, want Valid=true String=%q", stored[0].ActorSessionID, leakedSessionID)
			}
		})
	}
}
