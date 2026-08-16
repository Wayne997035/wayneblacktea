package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

// httpEmptyListContractCase is one row of TestEmptyListContract_HTTP: one HTTP
// list-returning endpoint, exercised with zero underlying rows, checked for
// the "0 rows -> JSON [] never null" contract.
//
// Exactly one of wantBareArray / wantFields is set per row: wantBareArray
// asserts the entire trimmed response body equals "[]"; wantFields asserts
// each named field is present as `"<field>": []` and never `"<field>": null`
// inside a JSON object response (e.g. GetSuggestions' {"knowledge_items":[],
// "decisions":[]}). Both check styles operate on the raw response TEXT, never
// a decoded slice's len() — see empty_list_contract_test.go's MCP sibling
// (internal/mcp/empty_list_contract_test.go) for why len() cannot distinguish
// "null" from "[]" on the wire.
type httpEmptyListContractCase struct {
	name          string
	run           func(t *testing.T) *httptest.ResponseRecorder
	wantBareArray bool
	wantFields    []string
}

// TestEmptyListContract_HTTP is the HTTP-side counterpart of
// TestEmptyListContract_MCP_SQLite (internal/mcp/empty_list_contract_test.go)
// — one table scanning every HTTP list-returning endpoint this repo currently
// exposes for the "0 rows -> JSON [] never null" contract.
//
// Most rows use fakes (fakeGTDStore{}, fakeDecisionStore{}, fakeProposalStore{})
// because the nil-guard for those endpoints lives IN the handler code itself
// (gtd_handler.go's ListGoals/ListProjects/ListTasks, decision_handler.go's
// ListDecisions, proposal_handler.go's ListPendingProposals/ListProposals via
// make([]pendingProposalResponse, 0, len(rows)), learning_handler.go's
// GetSuggestions) — a fake with a nil field genuinely exercises that Go code.
//
// GetDueReviews is different: learning_handler.go's GetDueReviews has NO
// guard of its own (`return c.JSON(http.StatusOK, reviews)` — reviews passed
// straight through), so it relies entirely on the store layer. A fake would
// either mask the real SQLite bug (if pre-seeded []DueReview{}) or fail for
// the wrong reason (bare nil field, unrelated to the actual production code
// path). Those two rows wire the handler to REAL stores instead — SQLite
// :memory: (proves the fix in this PR) and Postgres via the package's shared
// goalProjectPgPool testcontainer (regression lock — PG's DueReviews was
// already correct).
func TestEmptyListContract_HTTP(t *testing.T) {
	cases := []httpEmptyListContractCase{
		{
			name: "ListDecisions",
			run: func(t *testing.T) *httptest.ResponseRecorder {
				e := newEcho()
				h := handler.NewDecisionHandler(&fakeDecisionStore{})
				e.GET("/api/decisions", h.ListDecisions)
				return performRequest(e, http.MethodGet, "/api/decisions", "")
			},
			wantBareArray: true,
		},
		{
			name: "ListGoals",
			run: func(t *testing.T) *httptest.ResponseRecorder {
				e := newEcho()
				h := handler.NewGTDHandler(&fakeGTDStore{})
				e.GET("/api/goals", h.ListGoals)
				return performRequest(e, http.MethodGet, "/api/goals", "")
			},
			wantBareArray: true,
		},
		{
			name: "ListProjects",
			run: func(t *testing.T) *httptest.ResponseRecorder {
				e := newEcho()
				h := handler.NewGTDHandler(&fakeGTDStore{})
				e.GET("/api/projects", h.ListProjects)
				return performRequest(e, http.MethodGet, "/api/projects", "")
			},
			wantBareArray: true,
		},
		{
			name: "ListTasks",
			run: func(t *testing.T) *httptest.ResponseRecorder {
				e := newEcho()
				h := handler.NewGTDHandler(&fakeGTDStore{})
				e.GET("/api/tasks", h.ListTasks)
				return performRequest(e, http.MethodGet, "/api/tasks", "")
			},
			wantBareArray: true,
		},
		{
			// GTD-fix gtd-list-api-filters: ListProjects now routes through
			// ProjectsFiltered when a status query param is present (see
			// TestGTDHandler_ListProjects in handler_test.go for the full
			// filter-forwarding coverage). Sibling row to
			// ListTasks_status_all_filter below, proving the same nil-guard
			// holds on the filtered branch for the projects endpoint too.
			name: "ListProjects_status_all_filter",
			run: func(t *testing.T) *httptest.ResponseRecorder {
				e := newEcho()
				h := handler.NewGTDHandler(&fakeGTDStore{})
				e.GET("/api/projects", h.ListProjects)
				return performRequest(e, http.MethodGet, "/api/projects?status=all", "")
			},
			wantBareArray: true,
		},
		{
			// GTD-fix gtd-list-api-filters: ListTasks now routes through
			// TasksFiltered when a status query param is present (see
			// TestGTDHandler_ListTasks in handler_test.go for the full
			// filter-forwarding coverage). This row exercises that second
			// code path specifically for the empty-list contract — a nil
			// store result on the *filtered* branch (?status=all) must still
			// serialize as [] the same way the unfiltered ListTasks row
			// above does, not regress to null just because a query param was
			// present.
			name: "ListTasks_status_all_filter",
			run: func(t *testing.T) *httptest.ResponseRecorder {
				e := newEcho()
				h := handler.NewGTDHandler(&fakeGTDStore{})
				e.GET("/api/tasks", h.ListTasks)
				return performRequest(e, http.MethodGet, "/api/tasks?status=all", "")
			},
			wantBareArray: true,
		},
		{
			name: "ListPendingProposals",
			run: func(t *testing.T) *httptest.ResponseRecorder {
				e := newEcho()
				h := handler.NewProposalHandler(newFakeProposalStore(), &fakeProposalLearningStore{})
				e.GET("/api/proposals/pending", h.ListPendingProposals)
				return performRequest(e, http.MethodGet, "/api/proposals/pending", "")
			},
			wantBareArray: true,
		},
		{
			name: "ListProposals",
			run: func(t *testing.T) *httptest.ResponseRecorder {
				e := newEcho()
				h := handler.NewProposalHandler(newFakeProposalStore(), &fakeProposalLearningStore{})
				e.GET("/api/proposals", h.ListProposals)
				return performRequest(e, http.MethodGet, "/api/proposals", "")
			},
			wantBareArray: true,
		},
		{
			name: "GetSuggestions",
			run: func(t *testing.T) *httptest.ResponseRecorder {
				e := newEcho()
				h := handler.NewLearningHandler(
					&fakeLearningStore{},
					handler.WithKnowledgeStore(&fakeKnowledgeStore{}),
					handler.WithDecisionStore(&fakeSuggestionDecisionStore{}),
				)
				e.GET("/api/learning/suggestions", h.GetSuggestions)
				return performRequest(e, http.MethodGet, "/api/learning/suggestions", "")
			},
			wantFields: []string{"knowledge_items", "decisions"},
		},
		{
			// THE FIX under test in this PR, HTTP entry point, SQLite backend.
			// learning_handler.go's GetDueReviews has no guard of its own — it
			// relies on internal/storage/sqlite/learning.go's DueReviews, which
			// previously returned an unguarded nil slice (see
			// internal/storage/sqlite/learning_test.go's
			// TestLearningStore_DueReviews_EmptyReturnsEmptyArrayNotNull for the
			// store-level mutation proof).
			name: "GetDueReviews_SQLite",
			run: func(t *testing.T) *httptest.ResponseRecorder {
				d, err := sqlite.Open(context.Background(), ":memory:", "")
				if err != nil {
					t.Fatalf("sqlite.Open: %v", err)
				}
				t.Cleanup(func() { _ = d.Close() })
				e := newEcho()
				h := handler.NewLearningHandler(sqlite.NewLearningStore(d))
				e.GET("/api/learning/reviews", h.GetDueReviews)
				return performRequest(e, http.MethodGet, "/api/learning/reviews", "")
			},
			wantBareArray: true,
		},
		{
			// Postgres regression lock, HTTP entry point: internal/learning/
			// store.go's DueReviews was already correct
			// (make([]DueReview, 0, len(rows))) — this row protects that from
			// regressing at the HTTP boundary specifically, since the handler
			// itself has no guard and would silently start returning null again
			// if a future edit to the PG store dropped the make() call.
			name: "GetDueReviews_Postgres",
			run: func(t *testing.T) *httptest.ResponseRecorder {
				pool := openGoalProjectTestPgPool(t)
				wsID := uuid.New() // fresh, isolated workspace — guaranteed zero rows
				e := newEcho()
				h := handler.NewLearningHandler(learning.NewStore(pool, &wsID))
				e.GET("/api/learning/reviews", h.GetDueReviews)
				return performRequest(e, http.MethodGet, "/api/learning/reviews", "")
			},
			wantBareArray: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := tc.run(t)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: got status %d, want 200 (body: %s)", tc.name, rec.Code, rec.Body.String())
			}
			raw := rec.Body.String()

			if len(tc.wantFields) > 0 {
				for _, field := range tc.wantFields {
					// echo.Context.JSON encodes compactly (no space after ':'),
					// unlike the MCP side's json.MarshalIndent — match that shape.
					wantOK := `"` + field + `":[]`
					wantBad := `"` + field + `":null`
					if !strings.Contains(raw, wantOK) {
						t.Errorf("%s: expected raw JSON to contain %s, got: %s", tc.name, wantOK, raw)
					}
					if strings.Contains(raw, wantBad) {
						t.Errorf("%s: %q must never serialize as null, got: %s", tc.name, field, raw)
					}
				}
				return
			}

			if got := strings.TrimSpace(raw); got != "[]" {
				t.Errorf("%s: raw body = %q, want exactly %q (nil slice must not serialize to JSON null)", tc.name, got, "[]")
			}
		})
	}
}
