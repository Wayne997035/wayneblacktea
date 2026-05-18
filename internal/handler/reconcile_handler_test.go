package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/completioncandidate"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/labstack/echo/v4"
)

// openMemForReconcile spins up an in-memory SQLite GTDStore + matching
// completioncandidate.SQLiteStore on the same connection.
func openMemForReconcile(t *testing.T) (*sqlite.GTDStore, completioncandidate.Store) {
	t.Helper()
	d, err := sqlite.Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return sqlite.NewGTDStore(d), completioncandidate.NewSQLiteStore(d.SqlConn(), "")
}

// runReconcileRequest is a thin helper that posts the JSON body and returns
// the Echo response recorder.
func runReconcileRequest(t *testing.T, h *handler.ReconcileHandler, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost, "/api/tasks/reconcile-merged-prs",
		bytes.NewReader(body),
	)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	if err := h.Reconcile(c); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	return rec
}

// TestReconcileMergedPRs_ExactMatch_AutoApplies covers the end-to-end happy path:
// seed task → POST reconcile → task is completed + completion_candidates row
// with status='auto_applied' exists.
func TestReconcileMergedPRs_ExactMatch_AutoApplies(t *testing.T) {
	store, candStore := openMemForReconcile(t)
	h := handler.NewReconcileHandler(store, candStore)

	task, err := store.CreateTask(context.Background(),
		gtd.CreateTaskParams{Title: "do the thing", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	branch := "feature/x"
	if _, err := store.UpdateTask(context.Background(), task.ID,
		gtd.UpdateTaskParams{BranchName: &branch}); err != nil {
		t.Fatalf("seed branch: %v", err)
	}

	prURL := "https://github.com/owner/repo/pull/1"
	body := mustJSON(t, map[string]any{
		"merged_prs": []map[string]any{{
			"url": prURL, "head_ref": branch,
			"merged_at": "2026-05-18T12:00:00Z",
			"title":     "feat: x", "body": "closes #42",
			"repo": "owner/repo",
		}},
	})
	rec := runReconcileRequest(t, h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var resp struct {
		Matches []struct {
			TaskID, Reason, PRUrl, PRHeadRef string
		} `json:"matches"`
		Applied         int `json:"applied"`
		NoMatch         int `json:"no_match"`
		CandidateWrites int `json:"candidate_writes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, rec.Body.String())
	}
	if len(resp.Matches) != 1 || resp.Applied != 1 {
		t.Errorf("matches=%d applied=%d, want 1+1", len(resp.Matches), resp.Applied)
	}
	if resp.CandidateWrites != 1 {
		t.Errorf("candidate_writes = %d, want 1", resp.CandidateWrites)
	}

	// Task must be completed with pr_url set.
	got, err := store.GetTaskByID(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if got.Status != "completed" {
		t.Errorf("status = %q, want completed", got.Status)
	}
	if !got.PRUrl.Valid || got.PRUrl.String != prURL {
		t.Errorf("pr_url persisted = %+v, want %q", got.PRUrl, prURL)
	}
}

// TestReconcileMergedPRs_Idempotent covers idempotency.
func TestReconcileMergedPRs_Idempotent(t *testing.T) {
	store, candStore := openMemForReconcile(t)
	h := handler.NewReconcileHandler(store, candStore)

	task, err := store.CreateTask(context.Background(),
		gtd.CreateTaskParams{Title: "idem", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	branch := "feature/idem"
	if _, err := store.UpdateTask(context.Background(), task.ID,
		gtd.UpdateTaskParams{BranchName: &branch}); err != nil {
		t.Fatalf("seed branch: %v", err)
	}

	body := mustJSON(t, map[string]any{
		"merged_prs": []map[string]any{{
			"url": "https://github.com/owner/repo/pull/2", "head_ref": branch,
		}},
	})

	// 1st call: applied=1
	rec1 := runReconcileRequest(t, h, body)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first status = %d (body: %s)", rec1.Code, rec1.Body.String())
	}
	var first struct{ Applied int }
	_ = json.Unmarshal(rec1.Body.Bytes(), &first)
	if first.Applied != 1 {
		t.Errorf("first applied = %d, want 1", first.Applied)
	}

	// 2nd call: applied=0, matches=[] (task already completed, matcher skips)
	rec2 := runReconcileRequest(t, h, body)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second status = %d", rec2.Code)
	}
	var second struct {
		Applied int             `json:"applied"`
		Matches []any           `json:"matches"`
		NoMatch int             `json:"no_match"`
		Cw      json.RawMessage `json:"candidate_writes"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &second); err != nil {
		t.Fatalf("unmarshal second: %v", err)
	}
	if second.Applied != 0 {
		t.Errorf("second applied = %d, want 0", second.Applied)
	}
	if len(second.Matches) != 0 {
		t.Errorf("second matches = %d, want 0", len(second.Matches))
	}
	if second.NoMatch != 1 {
		t.Errorf("second no_match = %d, want 1 (PR didn't match anything pending)", second.NoMatch)
	}
}

// TestReconcileMergedPRs_NoMatch covers the no-match path.
func TestReconcileMergedPRs_NoMatch(t *testing.T) {
	store, candStore := openMemForReconcile(t)
	h := handler.NewReconcileHandler(store, candStore)

	task, err := store.CreateTask(context.Background(),
		gtd.CreateTaskParams{Title: "untouched", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	mine := "feature/y"
	if _, err := store.UpdateTask(context.Background(), task.ID,
		gtd.UpdateTaskParams{BranchName: &mine}); err != nil {
		t.Fatalf("seed branch: %v", err)
	}

	body := mustJSON(t, map[string]any{
		"merged_prs": []map[string]any{{
			"url": "https://github.com/owner/repo/pull/3", "head_ref": "feature/x",
		}},
	})
	rec := runReconcileRequest(t, h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp struct {
		Matches []any `json:"matches"`
		Applied int   `json:"applied"`
		NoMatch int   `json:"no_match"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Applied != 0 || resp.NoMatch != 1 {
		t.Errorf("applied=%d no_match=%d, want 0/1", resp.Applied, resp.NoMatch)
	}
	got, err := store.GetTaskByID(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if got.Status != "pending" {
		t.Errorf("task status = %q, want pending", got.Status)
	}
}

// TestReconcileMergedPRs_MultipleSameBranch_PicksMostRecent — handler-level
// confirmation of the matcher contract.
func TestReconcileMergedPRs_MultipleSameBranch_PicksMostRecent(t *testing.T) {
	store, candStore := openMemForReconcile(t)
	h := handler.NewReconcileHandler(store, candStore)
	ctx := context.Background()

	branch := "feature/dup-handler"
	older, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "older", Priority: 3})
	if err != nil {
		t.Fatalf("older CreateTask: %v", err)
	}
	if _, err := store.UpdateTask(ctx, older.ID, gtd.UpdateTaskParams{BranchName: &branch}); err != nil {
		t.Fatalf("older branch: %v", err)
	}
	// Time gap so updated_at differs (SQLite has ms resolution).
	sleepShort()
	newer, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "newer", Priority: 3})
	if err != nil {
		t.Fatalf("newer CreateTask: %v", err)
	}
	if _, err := store.UpdateTask(ctx, newer.ID, gtd.UpdateTaskParams{BranchName: &branch}); err != nil {
		t.Fatalf("newer branch: %v", err)
	}

	body := mustJSON(t, map[string]any{
		"merged_prs": []map[string]any{{
			"url": "https://github.com/owner/repo/pull/4", "head_ref": branch,
		}},
	})
	rec := runReconcileRequest(t, h, body)
	var resp struct {
		Matches []struct {
			TaskID string `json:"task_id"`
		} `json:"matches"`
		Ambiguous []struct {
			TaskID string `json:"task_id"`
		} `json:"ambiguous"`
		Applied int `json:"applied"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, rec.Body.String())
	}
	if len(resp.Matches) != 1 {
		t.Fatalf("matches = %d, want 1", len(resp.Matches))
	}
	if resp.Matches[0].TaskID != newer.ID.String() {
		t.Errorf("picked = %s, want newer = %s", resp.Matches[0].TaskID, newer.ID)
	}
	if len(resp.Ambiguous) != 1 || resp.Ambiguous[0].TaskID != older.ID.String() {
		t.Errorf("ambiguous = %+v, want older=%s", resp.Ambiguous, older.ID)
	}
	if resp.Applied != 1 {
		t.Errorf("applied = %d, want 1", resp.Applied)
	}

	// Older task remains pending.
	gotOld, err := store.GetTaskByID(ctx, older.ID)
	if err != nil {
		t.Fatalf("GetTaskByID older: %v", err)
	}
	if gotOld.Status != "pending" {
		t.Errorf("older.status = %q, want pending (ambiguous → unchanged)", gotOld.Status)
	}
}

// TestReconcileMergedPRs_PRURLMatch — pr_url linkage path.
func TestReconcileMergedPRs_PRURLMatch(t *testing.T) {
	store, candStore := openMemForReconcile(t)
	h := handler.NewReconcileHandler(store, candStore)

	task, err := store.CreateTask(context.Background(),
		gtd.CreateTaskParams{Title: "linked-by-url", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	prURL := "https://github.com/owner/repo/pull/55"
	if _, err := store.UpdateTask(context.Background(), task.ID,
		gtd.UpdateTaskParams{PRUrl: &prURL}); err != nil {
		t.Fatalf("seed pr_url: %v", err)
	}

	body := mustJSON(t, map[string]any{
		"merged_prs": []map[string]any{{
			"url": prURL, "head_ref": "irrelevant",
		}},
	})
	rec := runReconcileRequest(t, h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Matches []struct {
			Reason string `json:"reason"`
		} `json:"matches"`
		Applied int `json:"applied"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Applied != 1 || len(resp.Matches) != 1 ||
		resp.Matches[0].Reason != string(gtd.MatchReasonPRURLExact) {
		t.Errorf("response = %+v, want applied=1 matches[0].reason=pr_url_exact", resp)
	}
}

// TestReconcileMergedPRs_DoSGuards covers input limits.
func TestReconcileMergedPRs_DoSGuards(t *testing.T) {
	store, candStore := openMemForReconcile(t)
	h := handler.NewReconcileHandler(store, candStore)

	cases := []struct {
		name     string
		body     []byte
		wantCode int
		wantSub  string
	}{
		{
			name: "201 entries → 413",
			body: func() []byte {
				prs := make([]map[string]any, 201)
				for i := range prs {
					prs[i] = map[string]any{
						"url":      "https://github.com/owner/repo/pull/1",
						"head_ref": "feature/x",
					}
				}
				return mustJSON(t, map[string]any{"merged_prs": prs})
			}(),
			wantCode: http.StatusRequestEntityTooLarge,
			wantSub:  "200 entries",
		},
		{
			name:     "malformed pr_url → 400",
			body:     mustJSON(t, map[string]any{"merged_prs": []map[string]any{{"url": "javascript:alert(1)", "head_ref": "x"}}}),
			wantCode: http.StatusBadRequest,
			wantSub:  "valid GitHub PR URL",
		},
		{
			name:     "missing head_ref → 400",
			body:     mustJSON(t, map[string]any{"merged_prs": []map[string]any{{"url": "https://github.com/o/r/pull/1"}}}),
			wantCode: http.StatusBadRequest,
			wantSub:  "head_ref",
		},
		{
			name: "head_ref with newline → 400",
			body: mustJSON(t, map[string]any{"merged_prs": []map[string]any{{
				"url": "https://github.com/o/r/pull/1", "head_ref": "bad\nbranch",
			}}}),
			wantCode: http.StatusBadRequest,
			wantSub:  "control characters",
		},
		{
			name: "invalid merged_at → 400",
			body: mustJSON(t, map[string]any{"merged_prs": []map[string]any{{
				"url": "https://github.com/o/r/pull/1", "head_ref": "x", "merged_at": "not-a-date",
			}}}),
			wantCode: http.StatusBadRequest,
			wantSub:  "RFC3339",
		},
		{
			name:     "unknown field rejected",
			body:     []byte(`{"merged_prs":[], "garbage_field": true}`),
			wantCode: http.StatusBadRequest,
			wantSub:  "unknown field",
		},
		{
			name:     "empty list → 200",
			body:     mustJSON(t, map[string]any{"merged_prs": []any{}}),
			wantCode: http.StatusOK,
			wantSub:  `"matches":[]`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := runReconcileRequest(t, h, tc.body)
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantSub != "" && !strings.Contains(rec.Body.String(), tc.wantSub) {
				t.Errorf("body missing %q: %s", tc.wantSub, rec.Body.String())
			}
		})
	}
}

// mustJSON marshals v or fails the test.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	return b
}

// sleepShort yields enough time for SQLite's millisecond-resolution updated_at
// to differ between two writes. 50ms is comfortably above SQLite's resolution.
func sleepShort() {
	time.Sleep(50 * time.Millisecond)
}
