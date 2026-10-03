package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/google/uuid"
)

// fakeDecisionHandlerStore records the LogParams passed to Log so tests can
// assert provenance binding without a real DB. [F1003-11] All dropped in
// favor of List — mirrors handler.decisionStore's interface change
// (decision_handler.go's doc comment: the no-filter branch now needs
// offset/has_more paging, which All's frozen 2-arg signature cannot grow to
// support).
type fakeDecisionHandlerStore struct {
	logged  []decision.LogParams
	logErr  error
	list    []db.Decision
	listErr error
	byRepo  []db.Decision
	byProj  []db.Decision

	// probe is returned by List instead of list whenever the handler asks
	// for exactly 1 row — that is listAllWithHasMore's boundary-case
	// existence probe (decision_handler.go), fired only when limit ==
	// maxLimit and limit+1 would exceed decision.ListParams.Validate's own
	// ceiling.
	probe []db.Decision

	gotListParams                   []decision.ListParams
	gotByRepoLimit, gotByRepoOffset int32
	gotByProjLimit, gotByProjOffset int32
}

func (f *fakeDecisionHandlerStore) List(_ context.Context, p decision.ListParams) ([]db.Decision, error) {
	f.gotListParams = append(f.gotListParams, p)
	if p.Limit == 1 {
		return f.probe, f.listErr
	}
	return f.list, f.listErr
}

func (f *fakeDecisionHandlerStore) ByRepo(_ context.Context, _ string, limit, offset int32) ([]db.Decision, error) {
	f.gotByRepoLimit, f.gotByRepoOffset = limit, offset
	return f.byRepo, f.listErr
}

func (f *fakeDecisionHandlerStore) ByProject(_ context.Context, _ uuid.UUID, limit, offset int32) ([]db.Decision, error) {
	f.gotByProjLimit, f.gotByProjOffset = limit, offset
	return f.byProj, f.listErr
}

func (f *fakeDecisionHandlerStore) Log(_ context.Context, p decision.LogParams) (*db.Decision, error) {
	if f.logErr != nil {
		return nil, f.logErr
	}
	f.logged = append(f.logged, p)
	return &db.Decision{ID: uuid.New(), Title: p.Title}, nil
}

// TestListDecisions_NilStoreResultReturnsEmptyArrayNotNull verifies the
// nil-slice-vs-JSON-null gap across all three ListDecisions return points
// (List / ByRepo / ByProject). json.Unmarshal([]byte("null"), &slice) leaves
// slice nil with len==0 — the exact same shape as unmarshaling "[]" — so a
// test that unmarshals the response and checks len() cannot distinguish the
// two. This asserts the raw response body string instead. [F1003-11] the
// response became an object envelope, so this now asserts the `"decisions":
// []` substring rather than the old bare `"[]"` body.
func TestListDecisions_NilStoreResultReturnsEmptyArrayNotNull(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{name: "no filter (List)", query: ""},
		{name: "ByRepo", query: "?repo_name=wayneblacktea"},
		{name: "ByProject", query: "?project_id=" + uuid.New().String()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeDecisionHandlerStore{} // all list fields left nil
			e := newEcho()
			h := handler.NewDecisionHandler(store)
			e.GET("/api/decisions", h.ListDecisions)
			rec := performRequest(e, http.MethodGet, "/api/decisions"+tc.query, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
			}
			got := strings.TrimSpace(rec.Body.String())
			if !strings.Contains(got, `"decisions":[]`) {
				t.Errorf("body = %q, want substring %q (nil slice must not serialize to JSON null)", got, `"decisions":[]`)
			}
		})
	}
}

// TestLogDecision_HTTP is a table-driven pass over POST /api/decisions
// covering the happy path, a required-field error, and the P3.0a
// forged-payload regression: an extra "source" key in the request body must
// never override the server-owned decision.SourceManual constant that
// LogDecision always binds (producer #4 — no `source` request field exists
// on logDecisionRequest, so c.Bind silently drops it).
func TestLogDecision_HTTP(t *testing.T) {
	cases := []struct {
		name         string
		body         string
		wantCode     int
		wantLogCount int
		wantSource   decision.Source
	}{
		{
			name: "happy path binds SourceManual",
			body: `{"title":"ADR: use Echo","context":"need HTTP framework",` +
				`"decision":"use echo/v4","rationale":"minimal, fast"}`,
			wantCode:     http.StatusCreated,
			wantLogCount: 1,
			wantSource:   decision.SourceManual,
		},
		{
			name:         "missing required field → 400, no log",
			body:         `{"title":"","context":"c","decision":"d","rationale":"r"}`,
			wantCode:     http.StatusBadRequest,
			wantLogCount: 0,
		},
		{
			name: "forged source field is ignored, still SourceManual",
			body: `{"title":"ADR: forged source","context":"ctx","decision":"dec",` +
				`"rationale":"rat","source":"auto"}`,
			wantCode:     http.StatusCreated,
			wantLogCount: 1,
			wantSource:   decision.SourceManual,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeDecisionHandlerStore{}
			e := newEcho()
			h := handler.NewDecisionHandler(store)
			e.POST("/api/decisions", h.LogDecision)
			rec := performRequest(e, http.MethodPost, "/api/decisions", tc.body)
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if len(store.logged) != tc.wantLogCount {
				t.Fatalf("Log calls = %d, want %d", len(store.logged), tc.wantLogCount)
			}
			if tc.wantLogCount > 0 && store.logged[0].Source != tc.wantSource {
				t.Errorf("Source = %q, want %q", store.logged[0].Source, tc.wantSource)
			}
		})
	}
}

// decisionsPageBody mirrors decision_handler.go's unexported decisionsPage
// response envelope, decoded for assertions.
type decisionsPageBody struct {
	Decisions []db.Decision `json:"decisions"`
	Offset    int32         `json:"offset"`
	Limit     int32         `json:"limit"`
	HasMore   bool          `json:"has_more"`
}

func decodeDecisionsPage(t *testing.T, rawBody string) decisionsPageBody {
	t.Helper()
	var body decisionsPageBody
	if err := json.Unmarshal([]byte(rawBody), &body); err != nil {
		t.Fatalf("decode response body: %v (body: %s)", err, rawBody)
	}
	return body
}

// TestListDecisions_OffsetAndHasMore covers [F1003-11]'s offset/has_more
// contract across all three filter branches: the handler must forward
// limit+1 and offset to the store, echo offset/limit in the response, trim
// the extra probe row before returning it, and normalize a negative offset
// to 0 — same invalid-input fallback convention as limit.
func TestListDecisions_OffsetAndHasMore(t *testing.T) {
	someRow := func() db.Decision { return db.Decision{ID: uuid.New(), Title: "t"} }
	threeRows := []db.Decision{someRow(), someRow(), someRow()}
	twoRows := []db.Decision{someRow(), someRow()}

	cases := []struct {
		name          string
		query         string
		wire          func(store *fakeDecisionHandlerStore)
		wantOffset    int32
		wantLimit     int32
		wantHasMore   bool
		wantRows      int
		checkForwards func(t *testing.T, store *fakeDecisionHandlerStore)
	}{
		{
			name:        "no filter, offset omitted defaults to 0",
			query:       "?limit=2",
			wire:        func(s *fakeDecisionHandlerStore) { s.list = threeRows },
			wantOffset:  0,
			wantLimit:   2,
			wantHasMore: true, // 3 rows returned for limit+1=3 -> len(rows) > limit
			wantRows:    2,
			checkForwards: func(t *testing.T, s *fakeDecisionHandlerStore) {
				last := s.gotListParams[len(s.gotListParams)-1]
				if last.Limit != 3 || last.Offset != 0 || !last.IncludeAuto {
					t.Errorf("List params = %+v, want Limit=3 Offset=0 IncludeAuto=true", last)
				}
			},
		},
		{
			name:        "no filter, offset=20 forwarded and echoed",
			query:       "?limit=2&offset=20",
			wire:        func(s *fakeDecisionHandlerStore) { s.list = twoRows },
			wantOffset:  20,
			wantLimit:   2,
			wantHasMore: false, // 2 rows for limit+1=3 -> not more than limit
			wantRows:    2,
			checkForwards: func(t *testing.T, s *fakeDecisionHandlerStore) {
				last := s.gotListParams[len(s.gotListParams)-1]
				if last.Offset != 20 {
					t.Errorf("List Offset = %d, want 20", last.Offset)
				}
			},
		},
		{
			name:        "no filter, negative offset normalizes to 0",
			query:       "?limit=2&offset=-5",
			wire:        func(s *fakeDecisionHandlerStore) { s.list = twoRows },
			wantOffset:  0,
			wantLimit:   2,
			wantHasMore: false,
			wantRows:    2,
			checkForwards: func(t *testing.T, s *fakeDecisionHandlerStore) {
				last := s.gotListParams[len(s.gotListParams)-1]
				if last.Offset != 0 {
					t.Errorf("List Offset = %d, want 0 (negative must normalize)", last.Offset)
				}
			},
		},
		{
			name:        "repo_name branch forwards limit+1/offset and trims",
			query:       "?repo_name=wayneblacktea&limit=2&offset=10",
			wire:        func(s *fakeDecisionHandlerStore) { s.byRepo = threeRows },
			wantOffset:  10,
			wantLimit:   2,
			wantHasMore: true,
			wantRows:    2,
			checkForwards: func(t *testing.T, s *fakeDecisionHandlerStore) {
				if s.gotByRepoLimit != 3 || s.gotByRepoOffset != 10 {
					t.Errorf("ByRepo(limit=%d, offset=%d), want (3, 10)", s.gotByRepoLimit, s.gotByRepoOffset)
				}
			},
		},
		{
			// Mirrors spec Acceptance row: project with 12 decisions,
			// offset=10 limit=5 -> 2 rows (11th-12th), has_more=false.
			name:        "project_id branch, exhausted last page",
			query:       "?project_id=" + uuid.New().String() + "&limit=5&offset=10",
			wire:        func(s *fakeDecisionHandlerStore) { s.byProj = twoRows },
			wantOffset:  10,
			wantLimit:   5,
			wantHasMore: false,
			wantRows:    2,
			checkForwards: func(t *testing.T, s *fakeDecisionHandlerStore) {
				if s.gotByProjLimit != 6 || s.gotByProjOffset != 10 {
					t.Errorf("ByProject(limit=%d, offset=%d), want (6, 10)", s.gotByProjLimit, s.gotByProjOffset)
				}
			},
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
			body := decodeDecisionsPage(t, rec.Body.String())
			if body.Offset != tc.wantOffset || body.Limit != tc.wantLimit || body.HasMore != tc.wantHasMore || len(body.Decisions) != tc.wantRows {
				t.Errorf("got {offset:%d limit:%d has_more:%v rows:%d}, want {offset:%d limit:%d has_more:%v rows:%d}",
					body.Offset, body.Limit, body.HasMore, len(body.Decisions),
					tc.wantOffset, tc.wantLimit, tc.wantHasMore, tc.wantRows)
			}
			tc.checkForwards(t, store)
		})
	}
}

// TestListDecisions_NoFilterLimitBoundary covers the no-filter branch's
// limit == maxLimit edge case (decision_handler.go's listAllWithHasMore):
// limit+1 (101) would exceed decision.ListParams.Validate's own 100-row
// ceiling, so at this boundary the handler fetches exactly limit rows and
// runs a 1-row existence probe at offset+limit instead. Without this
// special case, ?limit=100 (or any larger value, clamped to 100) on the
// no-filter branch would 500 instead of paging.
func TestListDecisions_NoFilterLimitBoundary(t *testing.T) {
	hundred := make([]db.Decision, 100)
	for i := range hundred {
		hundred[i] = db.Decision{ID: uuid.New(), Title: "t"}
	}

	cases := []struct {
		name        string
		probe       []db.Decision
		wantHasMore bool
	}{
		{name: "probe finds a next row", probe: []db.Decision{{ID: uuid.New(), Title: "next"}}, wantHasMore: true},
		{name: "probe finds nothing, exactly 100 total", probe: nil, wantHasMore: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// query limit exceeds maxLimit (100) and must clamp to it,
			// exercising the same boundary as an exact ?limit=100 request.
			store := &fakeDecisionHandlerStore{list: hundred, probe: tc.probe}
			e := newEcho()
			h := handler.NewDecisionHandler(store)
			e.GET("/api/decisions", h.ListDecisions)
			rec := performRequest(e, http.MethodGet, "/api/decisions?limit=9999", "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
			}
			body := decodeDecisionsPage(t, rec.Body.String())
			if body.Limit != 100 || len(body.Decisions) != 100 {
				t.Fatalf("got {limit:%d rows:%d}, want {limit:100 rows:100}", body.Limit, len(body.Decisions))
			}
			if body.HasMore != tc.wantHasMore {
				t.Errorf("has_more = %v, want %v", body.HasMore, tc.wantHasMore)
			}
			// Exactly 2 List calls: the main limit=100 fetch, then the
			// limit=1 probe — confirms the probe fires instead of a
			// rejected limit=101 request.
			if len(store.gotListParams) != 2 {
				t.Fatalf("List called %d times, want 2 (main fetch + probe)", len(store.gotListParams))
			}
			if store.gotListParams[0].Limit != 100 || store.gotListParams[1].Limit != 1 {
				t.Errorf("List calls = %+v, want [Limit=100, Limit=1]", store.gotListParams)
			}
		})
	}
}
