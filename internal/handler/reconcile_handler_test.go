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
	"github.com/Wayne997035/wayneblacktea/internal/mergedprs"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// statusPending is the literal task status used across multiple test-case
// assertions. Promoted to a constant to keep goconst happy.
const statusPending = "pending"

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

// openMemForReconcileWithMergedPRs returns the existing GTD+candidate stores
// alongside a mergedprs.Store + raw *sql.DB so tests can assert directly on
// the merged_prs_observed table.
func openMemForReconcileWithMergedPRs(t *testing.T) (*sqlite.GTDStore, completioncandidate.Store, mergedprs.Store, *sqlite.DB) {
	t.Helper()
	d, err := sqlite.Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return sqlite.NewGTDStore(d),
		completioncandidate.NewSQLiteStore(d.SqlConn(), ""),
		mergedprs.NewSQLiteStore(d.SqlConn(), ""),
		d
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
			"repo": "owner/repo",
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
			"repo": "owner/repo",
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
	if got.Status != statusPending {
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
			"repo": "owner/repo",
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
	if gotOld.Status != statusPending {
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
			"repo": "owner/repo",
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

// toctouCancelStore wraps a gtd.StoreIface and, when
// BatchCompleteTasksByPRMatch is invoked, first flips cancelTaskID to
// 'cancelled' before delegating to the real store's guarded UPDATE. This
// simulates the TOCTOU race the HTTP handler is exposed to: unlike the MCP
// tool's two-step preview/confirm flow (where the race is a real gap between
// two separate calls, see tools_reconcile_test.go's
// TestMCPReconcileMergedPRs_ConfirmDoesNotWriteFalseAutoAppliedCandidate),
// the HTTP handler computes matches and applies them in a single call — the
// only way to reproduce "task cancelled between the match-read and the
// apply" here is to inject the state change inside the store method the
// handler calls for the apply step.
type toctouCancelStore struct {
	gtd.StoreIface
	t            *testing.T
	cancelTaskID uuid.UUID
}

func (w *toctouCancelStore) BatchCompleteTasksByPRMatch(
	ctx context.Context, matches []gtd.Match,
) (map[uuid.UUID]bool, error) {
	w.t.Helper()
	if _, err := w.UpdateTaskStatus(ctx, w.cancelTaskID, gtd.TaskStatusCancelled); err != nil {
		w.t.Fatalf("toctouCancelStore: cancel task mid-window: %v", err)
	}
	// Explicit .StoreIface qualifier is required here (unlike the
	// UpdateTaskStatus call above): BatchCompleteTasksByPRMatch IS shadowed
	// on *toctouCancelStore, so calling w.BatchCompleteTasksByPRMatch would
	// recurse into this method infinitely instead of delegating to the real
	// store's guarded UPDATE.
	return w.StoreIface.BatchCompleteTasksByPRMatch(ctx, matches)
}

// countAutoAppliedCandidatesSQL queries completion_candidates directly for
// the number of rows with status='auto_applied' for the given task —
// independent of the completioncandidate.Store interface (which exposes no
// "get by task_id" method) and independent of the handler's own JSON
// response (which could misreport under the very bug this test guards
// against).
func countAutoAppliedCandidatesSQL(t *testing.T, d *sqlite.DB, taskID uuid.UUID) int {
	t.Helper()
	var n int
	if err := d.SqlConn().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM completion_candidates WHERE task_id = ? AND status = 'auto_applied'`,
		taskID.String(),
	).Scan(&n); err != nil {
		t.Fatalf("countAutoAppliedCandidatesSQL: %v", err)
	}
	return n
}

// TestReconcileMergedPRs_TOCTOUCancelSkipsFalseAutoAppliedCandidate is the
// HTTP-handler regression test mirroring
// TestMCPReconcileMergedPRs_ConfirmDoesNotWriteFalseAutoAppliedCandidate
// (internal/mcp/tools_reconcile_test.go). Before this fix, the
// WriteAutoApplied loop in reconcile_handler.go's Reconcile ran
// unconditionally over every result.Matches entry regardless of whether
// appliedIDs (the genuinely-affected-rows map from
// BatchCompleteTasksByPRMatch) actually confirmed the task was applied — so
// a task cancelled between the match-read (gtd.MatchMergedPRs) and the apply
// call still got a persisted, FALSE completion_candidates row with
// status='auto_applied'.
//
// toctouCancelStore injects the cancellation exactly in that window: it
// wraps the real store and cancels the target task the instant
// BatchCompleteTasksByPRMatch is called (i.e. after MatchMergedPRs has
// already read the task as pending/matched, but before the guarded UPDATE
// runs) — so the real store's guard (`WHERE status IN
// ('pending','in_progress')`) correctly no-ops for this task, and
// appliedIDs is absent/false for it.
func TestReconcileMergedPRs_TOCTOUCancelSkipsFalseAutoAppliedCandidate(t *testing.T) {
	store, candStore, _, d := openMemForReconcileWithMergedPRs(t)
	ctx := context.Background()

	branch := "feature/toctou-http-cancel"
	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{
		Title: "toctou-http-candidate-task", Priority: 3,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{BranchName: &branch}); err != nil {
		t.Fatalf("seed branch: %v", err)
	}

	wrapped := &toctouCancelStore{StoreIface: store, t: t, cancelTaskID: task.ID}
	h := handler.NewReconcileHandler(wrapped, candStore)

	body := mustJSON(t, map[string]any{
		"merged_prs": []map[string]any{{
			"url": "https://github.com/owner/repo/pull/909", "head_ref": branch,
			"repo": "owner/repo",
		}},
	})
	rec := runReconcileRequest(t, h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	var resp struct {
		Matches []struct {
			TaskID  string `json:"task_id"`
			Applied bool   `json:"applied"`
		} `json:"matches"`
		Applied         int `json:"applied"`
		CandidateWrites int `json:"candidate_writes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, rec.Body.String())
	}
	if resp.Applied != 0 {
		t.Fatalf("applied = %d, want 0 — a task cancelled mid-window must not be silently completed", resp.Applied)
	}
	if len(resp.Matches) != 1 || resp.Matches[0].TaskID != task.ID.String() {
		t.Fatalf("matches = %+v, want exactly one match for task %s", resp.Matches, task.ID)
	}
	if resp.Matches[0].Applied {
		t.Errorf("matches[0].applied = true, want false — this match was skipped by the TOCTOU guard")
	}
	if resp.CandidateWrites != 0 {
		t.Errorf("candidate_writes = %d, want 0 — no audit row should be written for a task the guard skipped",
			resp.CandidateWrites)
	}

	// Hard requirement (independent of the JSON response, which could
	// under-report drift under the very bug this test guards against): no
	// completion_candidates row with status='auto_applied' exists for this
	// task.
	if n := countAutoAppliedCandidatesSQL(t, d, task.ID); n != 0 {
		t.Errorf("completion_candidates has %d auto_applied row(s) for task %s cancelled mid-window, want 0",
			n, task.ID)
	}

	// Parity check: task status is still cancelled, never flipped to
	// completed by the stale apply.
	got, err := store.GetTaskByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if got.Status != string(gtd.TaskStatusCancelled) {
		t.Errorf("task status = %q, want %q", got.Status, gtd.TaskStatusCancelled)
	}
}

// TestReconcileMergedPRs_AppliedFieldTrueForGenuineApply confirms the new
// per-match "applied" response field is true for a task that genuinely
// completed this call (no TOCTOU interference) — the positive-path
// complement to the TOCTOU test above, which only proves the false-negative
// case (skipped match → applied:false).
func TestReconcileMergedPRs_AppliedFieldTrueForGenuineApply(t *testing.T) {
	store, candStore := openMemForReconcile(t)
	h := handler.NewReconcileHandler(store, candStore)

	task, err := store.CreateTask(context.Background(),
		gtd.CreateTaskParams{Title: "genuine-apply", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	branch := "feature/genuine-apply"
	if _, err := store.UpdateTask(context.Background(), task.ID,
		gtd.UpdateTaskParams{BranchName: &branch}); err != nil {
		t.Fatalf("seed branch: %v", err)
	}

	body := mustJSON(t, map[string]any{
		"merged_prs": []map[string]any{{
			"url": "https://github.com/owner/repo/pull/910", "head_ref": branch,
			"repo": "owner/repo",
		}},
	})
	rec := runReconcileRequest(t, h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	var resp struct {
		Matches []struct {
			TaskID  string `json:"task_id"`
			Applied bool   `json:"applied"`
		} `json:"matches"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, rec.Body.String())
	}
	if len(resp.Matches) != 1 || resp.Matches[0].TaskID != task.ID.String() {
		t.Fatalf("matches = %+v, want exactly one match for task %s", resp.Matches, task.ID)
	}
	if !resp.Matches[0].Applied {
		t.Errorf("matches[0].applied = false, want true for a genuinely-applied match")
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
						"repo":     "owner/repo",
					}
				}
				return mustJSON(t, map[string]any{"merged_prs": prs})
			}(),
			wantCode: http.StatusRequestEntityTooLarge,
			wantSub:  "200 entries",
		},
		{
			name: "malformed pr_url → 400",
			body: mustJSON(t, map[string]any{"merged_prs": []map[string]any{{
				"url": "javascript:alert(1)", "head_ref": "x", "repo": "owner/repo",
			}}}),
			wantCode: http.StatusBadRequest,
			wantSub:  "valid GitHub PR URL",
		},
		{
			name:     "missing head_ref → 400",
			body:     mustJSON(t, map[string]any{"merged_prs": []map[string]any{{"url": "https://github.com/o/r/pull/1", "repo": "owner/repo"}}}),
			wantCode: http.StatusBadRequest,
			wantSub:  "head_ref",
		},
		{
			name: "head_ref with newline → 400",
			body: mustJSON(t, map[string]any{"merged_prs": []map[string]any{{
				"url": "https://github.com/o/r/pull/1", "head_ref": "bad\nbranch", "repo": "owner/repo",
			}}}),
			wantCode: http.StatusBadRequest,
			wantSub:  "control characters",
		},
		{
			name: "missing repo → 400",
			body: mustJSON(t, map[string]any{"merged_prs": []map[string]any{{
				"url": "https://github.com/o/r/pull/1", "head_ref": "x",
			}}}),
			wantCode: http.StatusBadRequest,
			wantSub:  "repo is required",
		},
		{
			name: "repo path-traversal → 400",
			body: mustJSON(t, map[string]any{"merged_prs": []map[string]any{{
				"url": "https://github.com/o/r/pull/1", "head_ref": "x",
				"repo": "../etc/passwd",
			}}}),
			wantCode: http.StatusBadRequest,
			wantSub:  "owner/repo slug pattern",
		},
		{
			name: "repo shell-injection → 400",
			body: mustJSON(t, map[string]any{"merged_prs": []map[string]any{{
				"url": "https://github.com/o/r/pull/1", "head_ref": "x",
				"repo": "owner/repo;rm -rf",
			}}}),
			wantCode: http.StatusBadRequest,
			wantSub:  "owner/repo slug pattern",
		},
		{
			name: "repo with space → 400",
			body: mustJSON(t, map[string]any{"merged_prs": []map[string]any{{
				"url": "https://github.com/o/r/pull/1", "head_ref": "x",
				"repo": "owner/repo with space",
			}}}),
			wantCode: http.StatusBadRequest,
			wantSub:  "owner/repo slug pattern",
		},
		{
			name: "invalid merged_at → 400",
			body: mustJSON(t, map[string]any{"merged_prs": []map[string]any{{
				"url": "https://github.com/o/r/pull/1", "head_ref": "x",
				"repo": "owner/repo", "merged_at": "not-a-date",
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

// TestReconcileMergedPRs_PersistsMergedPRsObserved verifies the Phase 2
// audit-trail write (sprint feature/0519-gtd-reconcile-phase2): every
// incoming PR ends up in merged_prs_observed exactly once, idempotent on a
// second POST with the same payload.
func TestReconcileMergedPRs_PersistsMergedPRsObserved(t *testing.T) {
	store, candStore, mpsStore, d := openMemForReconcileWithMergedPRs(t)
	h := handler.NewReconcileHandler(store, candStore).WithMergedPRsStore(mpsStore)

	prURL := "https://github.com/owner/repo/pull/501"
	body := mustJSON(t, map[string]any{
		"merged_prs": []map[string]any{{
			"url": prURL, "head_ref": "feature/persistence",
			"merged_at": "2026-05-19T12:00:00Z",
			"title":     "feat: persistence test",
			"body":      "the body",
			"repo":      "owner/repo",
		}},
	})

	// 1st POST → row inserted
	rec1 := runReconcileRequest(t, h, body)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first POST status = %d (body: %s)", rec1.Code, rec1.Body.String())
	}

	row1 := getMergedPRsObservedRow(t, d, prURL)
	if row1.repo == "" {
		t.Fatalf("row not persisted on first POST")
	}
	if row1.title == "" || !strings.Contains(row1.title, "persistence") {
		t.Errorf("title = %q, want substring 'persistence'", row1.title)
	}
	firstObservedAt := row1.observedAt

	// 2nd POST → idempotent: same row, observed_at refreshed.
	sleepShort()
	rec2 := runReconcileRequest(t, h, body)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second POST status = %d", rec2.Code)
	}

	count := countMergedPRsObservedByURL(t, d, prURL)
	if count != 1 {
		t.Errorf("rows for url = %d, want 1 (idempotent on url UNIQUE)", count)
	}
	row2 := getMergedPRsObservedRow(t, d, prURL)
	if !row2.observedAt.After(firstObservedAt) {
		t.Errorf("observed_at not refreshed: first=%v second=%v", firstObservedAt, row2.observedAt)
	}
}

// TestReconcileMergedPRs_FuzzyCandidateForNullLinkageTask verifies the
// Phase 2 detection rule: a pending task with NULL branch_name AND NULL
// pr_url whose title overlaps the PR title surfaces as a 'medium'-
// confidence completion_candidate (reason='pr_merged_fuzzy'). Crucially the
// task must remain pending — fuzzy candidates NEVER auto-apply.
func TestReconcileMergedPRs_FuzzyCandidateForNullLinkageTask(t *testing.T) {
	store, candStore, mpsStore, _ := openMemForReconcileWithMergedPRs(t)
	h := handler.NewReconcileHandler(store, candStore).WithMergedPRsStore(mpsStore)
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{
		Title:    "fix useCompleteTask onError invalidation regression",
		Priority: 3,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// Branch + pr_url intentionally NOT set — fuzzy eligibility.

	body := mustJSON(t, map[string]any{
		"merged_prs": []map[string]any{{
			"url":      "https://github.com/owner/repo/pull/777",
			"head_ref": "feature/unlinked",
			"title":    "fix: useCompleteTask onError invalidation",
			"body":     "unrelated body",
			"repo":     "owner/repo",
		}},
	})
	rec := runReconcileRequest(t, h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	var resp struct {
		Applied         int `json:"applied"`
		NoMatch         int `json:"no_match"`
		CandidateWrites int `json:"candidate_writes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Applied != 0 {
		t.Errorf("applied = %d, want 0 (no exact-match path; fuzzy never auto-applies)", resp.Applied)
	}
	if resp.CandidateWrites != 1 {
		t.Errorf("candidate_writes = %d, want 1 (one fuzzy candidate written)", resp.CandidateWrites)
	}

	// Task must remain pending.
	got, err := store.GetTaskByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if got.Status != statusPending {
		t.Errorf("status = %q, want pending (fuzzy candidate is manual-accept only)", got.Status)
	}

	// The candidate row must be queryable via ListPendingCandidates.
	pendings, err := candStore.ListPendingCandidates(ctx, nil)
	if err != nil {
		t.Fatalf("ListPendingCandidates: %v", err)
	}
	var found bool
	for _, c := range pendings {
		if c.TaskID == task.ID && c.Reason == completioncandidate.ReasonPRMerged {
			if c.Confidence != completioncandidate.ConfidenceMedium {
				t.Errorf("confidence = %q, want medium", c.Confidence)
			}
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no pending candidate with reason=pr_merged_fuzzy for task %s", task.ID)
	}
}

// TestReconcileMergedPRs_FuzzyCandidateFromCurl (Sprint #3 GTD a2088783) is
// the curl-style smoke test for the Phase 2 fuzzy path. It exercises the
// full HTTP wire: a single pending task with null branch_name + null pr_url
// whose title strongly overlaps an incoming PR title.
//
// Goals:
//   - response.candidate_writes == 1
//   - completion_candidates: one row with reason='pr_merged_fuzzy',
//     confidence='medium', status='pending' (NOT auto-applied), task_id
//     matches the seeded task
//   - merged_prs_observed: one row whose url matches the incoming PR (verifies
//     Task B's repo validation didn't reject a legitimate input)
//
// Scope: single-task scenario only — the multi-task ambiguity logic is
// covered by gtd/fuzzy_reconcile_test.go (Engineer 1's worktree). We MUST
// NOT touch fuzzy logic here.
func TestReconcileMergedPRs_FuzzyCandidateFromCurl(t *testing.T) {
	store, candStore, mpsStore, d := openMemForReconcileWithMergedPRs(t)
	h := handler.NewReconcileHandler(store, candStore).WithMergedPRsStore(mpsStore)
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{
		Title:    "implement OAuth refresh token rotation",
		Priority: 3,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// Branch + pr_url intentionally NOT set — fuzzy eligibility (NULL linkage).

	prURL := "https://github.com/Wayne997035/wayneblacktea/pull/9999"
	body := mustJSON(t, map[string]any{
		"merged_prs": []map[string]any{{
			"url":       prURL,
			"head_ref":  "feat/oauth",
			"merged_at": "2026-05-19T10:00:00Z",
			"title":     "implement OAuth refresh token rotation",
			"body":      "",
			"repo":      "Wayne997035/wayneblacktea",
		}},
	})
	rec := runReconcileRequest(t, h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	assertReconcileFuzzyResponse(t, rec.Body.Bytes())
	assertFuzzyCandidateRow(t, ctx, candStore, task.ID)
	assertObservedRowForURL(t, d, prURL, "Wayne997035/wayneblacktea")
}

// assertReconcileFuzzyResponse parses the reconcile response and asserts the
// fuzzy contract: candidate_writes=1, applied=0 (fuzzy NEVER auto-applies).
func assertReconcileFuzzyResponse(t *testing.T, raw []byte) {
	t.Helper()
	var resp struct {
		Applied         int `json:"applied"`
		NoMatch         int `json:"no_match"`
		CandidateWrites int `json:"candidate_writes"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v (body: %s)", err, string(raw))
	}
	if resp.CandidateWrites != 1 {
		t.Errorf("candidate_writes = %d, want 1", resp.CandidateWrites)
	}
	if resp.Applied != 0 {
		t.Errorf("applied = %d, want 0 (fuzzy NEVER auto-applies)", resp.Applied)
	}
}

// assertFuzzyCandidateRow scans the pending-candidates list and asserts
// exactly one row exists for taskID with the fuzzy reason + medium confidence
// + pending status.
func assertFuzzyCandidateRow(t *testing.T, ctx context.Context, candStore completioncandidate.Store, taskID uuid.UUID) {
	t.Helper()
	pendings, err := candStore.ListPendingCandidates(ctx, nil)
	if err != nil {
		t.Fatalf("ListPendingCandidates: %v", err)
	}
	matched := 0
	for _, c := range pendings {
		if c.TaskID != taskID || c.Reason != completioncandidate.ReasonPRMerged {
			continue
		}
		matched++
		if c.Confidence != completioncandidate.ConfidenceMedium {
			t.Errorf("confidence = %q, want medium", c.Confidence)
		}
		if c.Status != completioncandidate.StatusPending {
			t.Errorf("status = %q, want pending (fuzzy is manual-accept only, NOT auto_applied)", c.Status)
		}
	}
	if matched != 1 {
		t.Errorf("fuzzy candidates for task = %d, want 1", matched)
	}
}

// assertObservedRowForURL asserts exactly one merged_prs_observed row exists
// for prURL with the expected repo (proves Task B's repo validation accepted
// the legitimate slug).
func assertObservedRowForURL(t *testing.T, d *sqlite.DB, prURL, wantRepo string) {
	t.Helper()
	if n := countMergedPRsObservedByURL(t, d, prURL); n != 1 {
		t.Errorf("merged_prs_observed rows for url = %d, want 1", n)
	}
	row := getMergedPRsObservedRow(t, d, prURL)
	if row.url != prURL {
		t.Errorf("observed url = %q, want %q", row.url, prURL)
	}
	if row.repo != wantRepo {
		t.Errorf("observed repo = %q, want %q", row.repo, wantRepo)
	}
}

// observedRow is a tiny test helper for asserting on merged_prs_observed.
type observedRow struct {
	repo       string
	url        string
	title      string
	observedAt time.Time
}

// getMergedPRsObservedRow reads the single merged_prs_observed row matching
// url; fails the test if zero or multiple rows match.
func getMergedPRsObservedRow(t *testing.T, d *sqlite.DB, url string) observedRow {
	t.Helper()
	row := d.SqlConn().QueryRow(
		`SELECT repo, url, COALESCE(title,''), observed_at
		FROM merged_prs_observed WHERE url = ?1`, url)
	var (
		r           observedRow
		observedStr string
	)
	if err := row.Scan(&r.repo, &r.url, &r.title, &observedStr); err != nil {
		t.Fatalf("scan merged_prs_observed url=%s: %v", url, err)
	}
	t2, err := time.Parse(time.RFC3339Nano, observedStr)
	if err != nil {
		t.Fatalf("parse observed_at %q: %v", observedStr, err)
	}
	r.observedAt = t2
	return r
}

// countMergedPRsObservedByURL returns the number of rows matching url.
func countMergedPRsObservedByURL(t *testing.T, d *sqlite.DB, url string) int {
	t.Helper()
	var n int
	if err := d.SqlConn().QueryRow(
		`SELECT COUNT(*) FROM merged_prs_observed WHERE url = ?1`, url,
	).Scan(&n); err != nil {
		t.Fatalf("count merged_prs_observed: %v", err)
	}
	return n
}
