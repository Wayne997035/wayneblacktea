package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/completioncandidate"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"

	_ "modernc.org/sqlite" // sqlite driver registration for the test-only candidate DB
)

// withReconcileCandidates wires a SQLite-backed completioncandidate.Store onto
// the test server. Returns s for chaining.
//
// newTestWorkSessionServer doesn't call WithCompletionCandidates by default,
// so reconcile tests that exercise candidate_writes must opt in explicitly.
//
// We construct a fresh in-memory store; the candidate writes only need the
// completion_candidates table — they don't share rows with the GTD DB. The
// MCP handler treats candidate persistence as supplementary audit, not a
// foreign-key relationship.
func withReconcileCandidates(t *testing.T, s *Server) *Server {
	t.Helper()
	s, _ = withReconcileCandidatesDB(t, s)
	return s
}

// withReconcileCandidatesDB is withReconcileCandidates but also returns the
// raw *sql.DB backing the wired completioncandidate.Store. Round-3 Finding 2's
// regression test needs to query completion_candidates rows DIRECTLY (not
// just inspect the tool's response JSON) to prove no false 'auto_applied' row
// was persisted for a task the confirm-gate's TOCTOU guard skipped — the
// completioncandidate.Store interface intentionally exposes no "get by
// task_id" method, so the test needs the underlying connection.
func withReconcileCandidatesDB(t *testing.T, s *Server) (*Server, *sql.DB) {
	t.Helper()
	dbConn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open candidate db: %v", err)
	}
	t.Cleanup(func() { _ = dbConn.Close() })
	const ddl = `CREATE TABLE IF NOT EXISTS completion_candidates (
		id                 TEXT PRIMARY KEY,
		workspace_id       TEXT,
		task_id            TEXT NOT NULL,
		repo_name          TEXT,
		reason             TEXT NOT NULL,
		evidence_refs      TEXT NOT NULL DEFAULT '[]',
		confidence         TEXT NOT NULL CHECK (confidence IN ('high','medium','low')),
		suggested_artifact TEXT,
		status             TEXT NOT NULL DEFAULT 'pending'
		                       CHECK (status IN ('pending','accepted','rejected','auto_applied')),
		detected_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
		resolved_at        TEXT
	);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_completion_candidates_task_reason
		ON completion_candidates(task_id, reason);`
	if _, err := dbConn.ExecContext(context.Background(), ddl); err != nil {
		t.Fatalf("apply candidate schema: %v", err)
	}
	cs := completioncandidate.NewSQLiteStore(dbConn, "")
	s.WithCompletionCandidates(cs)
	return s, dbConn
}

// countAutoAppliedCandidates queries completion_candidates directly for the
// number of rows with status='auto_applied' for the given task. Used by the
// Finding 2 regression test to verify audit-trail integrity independent of
// the tool's JSON response (which could be forged/misreported without this
// direct DB check).
func countAutoAppliedCandidates(t *testing.T, dbConn *sql.DB, taskID uuid.UUID) int {
	t.Helper()
	var count int
	err := dbConn.QueryRowContext(
		context.Background(),
		`SELECT COUNT(*) FROM completion_candidates WHERE task_id = ? AND status = 'auto_applied'`,
		taskID.String(),
	).Scan(&count)
	if err != nil {
		t.Fatalf("countAutoAppliedCandidates: %v", err)
	}
	return count
}

// callReconcile invokes the reconcile_merged_prs handler directly with only
// a payload argument (preview call, confirm absent). Kept unchanged so the
// pre-existing tests (_InvalidPayloads, _RepoValidation, _TooManyEntries)
// keep compiling and passing with zero changes to their call sites.
func callReconcile(t *testing.T, s *Server, payload any) *mcpmsg.CallToolResult {
	t.Helper()
	return callReconcileWithArgs(t, s, payload, nil)
}

// callReconcileWithArgs invokes the reconcile_merged_prs handler with the
// given payload (marshaled to JSON and set as the "payload" argument, unless
// payload is nil in which case "payload" is omitted entirely) plus any extra
// arguments (confirm, reconcile_token, ...) merged in. extra values take
// precedence if they also set "payload".
func callReconcileWithArgs(t *testing.T, s *Server, payload any, extra map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	args := map[string]any{}
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		args["payload"] = string(raw)
	}
	for k, v := range extra {
		args[k] = v
	}
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	res, err := s.handleReconcileMergedPRs(context.Background(), req)
	if err != nil {
		t.Fatalf("handleReconcileMergedPRs error: %v", err)
	}
	return res
}

// reconcilePreviewEnvelope is the JSON shape returned by a preview
// (confirm absent/false) call to reconcile_merged_prs.
type reconcilePreviewEnvelope struct {
	Status          string `json:"status"`
	Applied         int    `json:"applied"`
	NoMatch         int    `json:"no_match"`
	CandidateWrites int    `json:"candidate_writes"`
	ReconcileToken  string `json:"reconcile_token"`
	ExpiresAt       string `json:"expires_at"`
	Matches         []struct {
		TaskID string `json:"task_id"`
		Reason string `json:"reason"`
	} `json:"matches"`
}

// reconcileAppliedEnvelope is the JSON shape returned by a confirm
// (confirm=true) call to reconcile_merged_prs.
type reconcileAppliedEnvelope struct {
	Status          string `json:"status"`
	Applied         int    `json:"applied"`
	NoMatch         int    `json:"no_match"`
	CandidateWrites int    `json:"candidate_writes"`
	Matches         []struct {
		TaskID string `json:"task_id"`
		Reason string `json:"reason"`
	} `json:"matches"`
}

// extractReconcileToken parses a preview-call envelope and returns the
// reconcile_token. Fails the test if the envelope shape is unexpected.
func extractReconcileToken(t *testing.T, r *mcpmsg.CallToolResult) reconcilePreviewEnvelope {
	t.Helper()
	if r.IsError {
		t.Fatalf("expected preview-call success, got error: %s", resultText(r))
	}
	var env reconcilePreviewEnvelope
	if err := json.Unmarshal([]byte(resultText(r)), &env); err != nil {
		t.Fatalf("unmarshal preview envelope: %v\nraw=%s", err, resultText(r))
	}
	if env.Status != "confirmation_required" {
		t.Errorf("status = %q, want confirmation_required", env.Status)
	}
	if env.ReconcileToken == "" {
		t.Error("reconcile_token must be non-empty")
	}
	if env.ExpiresAt == "" {
		t.Error("expires_at must be present")
	}
	if env.Applied != 0 {
		t.Errorf("preview applied = %d, want 0", env.Applied)
	}
	return env
}

// seedBranchedTask creates a task and sets its branch_name in one step, for
// tests that need an exact-match-eligible fixture task. Fails the test on any
// error so call sites don't need their own error-handling branches (keeps
// gocyclo down in tests that seed multiple tasks).
func seedBranchedTask(t *testing.T, s *Server, title, branch string) *db.Task {
	t.Helper()
	ctx := context.Background()
	task, err := s.gtd.CreateTask(ctx, gtd.CreateTaskParams{Title: title})
	if err != nil {
		t.Fatalf("CreateTask %q: %v", title, err)
	}
	if _, err := s.gtd.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{BranchName: &branch}); err != nil {
		t.Fatalf("seed branch for %q: %v", title, err)
	}
	return task
}

// TestMCPReconcileMergedPRs_ExactMatch_AutoApplies covers the MCP-side happy
// path end-to-end across the two-step confirm flow: preview must NOT
// complete the task; confirm (with the token from preview) must.
func TestMCPReconcileMergedPRs_ExactMatch_AutoApplies(t *testing.T) {
	s := withReconcileCandidates(t, newTestWorkSessionServer(t))
	ctx := context.Background()

	branch := "feature/mcp-x"
	task := seedBranchedTask(t, s, "mcp-recon", branch)

	payload := map[string]any{
		"merged_prs": []map[string]any{{
			"url":       "https://github.com/owner/repo/pull/77",
			"head_ref":  branch,
			"merged_at": "2026-05-18T12:00:00Z",
			"repo":      "owner/repo",
		}},
	}

	// Step 1 — preview only. Must NOT complete the task.
	previewRes := callReconcile(t, s, payload)
	preview := extractReconcileToken(t, previewRes)
	if len(preview.Matches) != 1 {
		t.Fatalf("preview matches = %d, want 1", len(preview.Matches))
	}
	if preview.Matches[0].TaskID != task.ID.String() {
		t.Errorf("preview matched task = %s, want %s", preview.Matches[0].TaskID, task.ID)
	}

	got, err := s.gtd.GetTaskByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTaskByID after preview: %v", err)
	}
	if got.Status == taskStatusCompleted {
		t.Fatal("preview must not complete the task")
	}

	// Step 2 — confirm with the token from step 1.
	confirmRes := callReconcileWithArgs(t, s, nil, map[string]any{
		"confirm":         true,
		"reconcile_token": preview.ReconcileToken,
	})
	if confirmRes.IsError {
		t.Fatalf("confirm error: %s", resultText(confirmRes))
	}
	var applied reconcileAppliedEnvelope
	if err := json.Unmarshal([]byte(resultText(confirmRes)), &applied); err != nil {
		t.Fatalf("unmarshal confirm envelope: %v\nbody=%s", err, resultText(confirmRes))
	}
	if applied.Status != "applied" {
		t.Errorf("confirm status = %q, want applied", applied.Status)
	}
	if applied.Applied != 1 || len(applied.Matches) != 1 {
		t.Errorf("applied=%d matches=%d, want 1+1", applied.Applied, len(applied.Matches))
	}
	if applied.CandidateWrites != 1 {
		t.Errorf("candidate_writes = %d, want 1 (SQLite store satisfies WriteAutoApplied)",
			applied.CandidateWrites)
	}

	// Verify task is closed.
	got, err = s.gtd.GetTaskByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if got.Status != taskStatusCompleted {
		t.Errorf("status = %q, want completed", got.Status)
	}
}

// TestMCPReconcileMergedPRs_InvalidPayloads covers MCP-side validation paths.
func TestMCPReconcileMergedPRs_InvalidPayloads(t *testing.T) {
	s := newTestWorkSessionServer(t)

	cases := []struct {
		name      string
		args      map[string]any
		wantInErr string
	}{
		{name: "missing payload", args: map[string]any{}, wantInErr: "payload is required"},
		{name: "bad json", args: map[string]any{"payload": "not-json"}, wantInErr: "invalid payload JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := mcpmsg.CallToolRequest{}
			req.Params.Arguments = tc.args
			res, err := s.handleReconcileMergedPRs(context.Background(), req)
			if err != nil {
				t.Fatalf("handle: %v", err)
			}
			if !res.IsError {
				t.Fatalf("expected tool error, got: %s", resultText(res))
			}
			if !strings.Contains(resultText(res), tc.wantInErr) {
				t.Errorf("error must contain %q, got: %s", tc.wantInErr, resultText(res))
			}
		})
	}
}

// TestMCPReconcileMergedPRs_RepoValidation — MCP-side mirror of the HTTP
// handler's repo required + slug regex check. The slug downstream flows into
// `gh -R <slug>` so any whitespace / shell meta / path-traversal MUST be
// rejected at this boundary.
func TestMCPReconcileMergedPRs_RepoValidation(t *testing.T) {
	s := newTestWorkSessionServer(t)

	cases := []struct {
		name      string
		repo      any // nil = field omitted entirely; "" = present but empty
		wantInErr string
	}{
		{
			name:      "missing repo (field omitted)",
			repo:      nil,
			wantInErr: "repo is required",
		},
		{
			name:      "empty repo string",
			repo:      "",
			wantInErr: "repo is required",
		},
		{
			name:      "path traversal",
			repo:      "../etc/passwd",
			wantInErr: "owner/repo slug pattern",
		},
		{
			name:      "shell injection semi+rm",
			repo:      "owner/repo;rm -rf",
			wantInErr: "owner/repo slug pattern",
		},
		{
			name:      "whitespace in segment",
			repo:      "owner/repo with space",
			wantInErr: "owner/repo slug pattern",
		},
		{
			name:      "trailing newline",
			repo:      "owner/repo\n",
			wantInErr: "owner/repo slug pattern",
		},
		{
			name:      "missing slash",
			repo:      "owner",
			wantInErr: "owner/repo slug pattern",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := map[string]any{
				"url":      "https://github.com/o/r/pull/1",
				"head_ref": "feature/x",
			}
			if tc.repo != nil {
				entry["repo"] = tc.repo
			}
			r := callReconcile(t, s, map[string]any{
				"merged_prs": []map[string]any{entry},
			})
			if !r.IsError {
				t.Fatalf("expected tool error, got: %s", resultText(r))
			}
			if !strings.Contains(resultText(r), tc.wantInErr) {
				t.Errorf("error must contain %q, got: %s", tc.wantInErr, resultText(r))
			}
		})
	}
}

// TestMCPReconcileMergedPRs_TooManyEntries — cap enforcement.
func TestMCPReconcileMergedPRs_TooManyEntries(t *testing.T) {
	s := newTestWorkSessionServer(t)

	prs := make([]map[string]any, 201)
	for i := range prs {
		prs[i] = map[string]any{
			"url": "https://github.com/o/r/pull/1", "head_ref": "feature/x",
			"repo": "o/r",
		}
	}
	r := callReconcile(t, s, map[string]any{"merged_prs": prs})
	if !r.IsError {
		t.Fatal("expected tool error for >200 entries")
	}
	if !strings.Contains(resultText(r), "exceeds") {
		t.Errorf("error must mention cap, got: %s", resultText(r))
	}
}

// TestMCPReconcileMergedPRs_Idempotent — preview+confirm the same payload
// once, then preview the SAME payload again. The second preview must show
// the task in no_match (it's already completed, no longer pending/
// in_progress) rather than re-matching it. A second confirm call is not
// required to prove idempotency — the second preview's no_match=1 already
// demonstrates the already-applied batch is not re-matched.
func TestMCPReconcileMergedPRs_Idempotent(t *testing.T) {
	s := newTestWorkSessionServer(t)
	ctx := context.Background()

	branch := "feature/mcp-idem"
	task := seedBranchedTask(t, s, "mcp-idem", branch)

	payload := map[string]any{
		"merged_prs": []map[string]any{{
			"url": "https://github.com/o/r/pull/88", "head_ref": branch,
			"repo": "o/r",
		}},
	}

	// Round 1: preview then confirm.
	preview1Res := callReconcile(t, s, payload)
	preview1 := extractReconcileToken(t, preview1Res)
	if len(preview1.Matches) != 1 {
		t.Fatalf("round 1 preview matches = %d, want 1", len(preview1.Matches))
	}

	confirm1Res := callReconcileWithArgs(t, s, nil, map[string]any{
		"confirm":         true,
		"reconcile_token": preview1.ReconcileToken,
	})
	var confirm1 reconcileAppliedEnvelope
	if err := json.Unmarshal([]byte(resultText(confirm1Res)), &confirm1); err != nil {
		t.Fatalf("unmarshal confirm1: %v\nbody=%s", err, resultText(confirm1Res))
	}
	if confirm1.Applied != 1 {
		t.Fatalf("round 1 applied = %d, want 1 (body: %s)", confirm1.Applied, resultText(confirm1Res))
	}

	got, err := s.gtd.GetTaskByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if got.Status != taskStatusCompleted {
		t.Fatalf("task status = %q after round 1, want completed", got.Status)
	}

	// Round 2: preview the SAME payload again — the task is no longer
	// pending/in_progress, so MatchMergedPRs must not match it.
	preview2Res := callReconcile(t, s, payload)
	if preview2Res.IsError {
		t.Fatalf("round 2 preview error: %s", resultText(preview2Res))
	}
	var preview2 reconcilePreviewEnvelope
	if err := json.Unmarshal([]byte(resultText(preview2Res)), &preview2); err != nil {
		t.Fatalf("unmarshal round 2 preview: %v", err)
	}
	if len(preview2.Matches) != 0 {
		t.Errorf("round 2 matches = %d, want 0", len(preview2.Matches))
	}
	if preview2.NoMatch != 1 {
		t.Errorf("round 2 no_match = %d, want 1", preview2.NoMatch)
	}
}

// TestMCPReconcileMergedPRs_Preview_DoesNotComplete covers the preview-only
// path with 2 fixture tasks in the same call: one exactly matched (via
// branch_name) and one unrelated fuzzy-eligible task (matched via the
// PR-body-contains-task-UUID rule). Verifies:
//   - the exact match is surfaced but the task is NOT completed
//   - candidate_writes reflects ONLY the unrelated fuzzy-eligible task —
//     proving Clarification 3's exact-match exclusion does not accidentally
//     exclude OTHER unrelated fuzzy-eligible tasks in the same batch.
func TestMCPReconcileMergedPRs_Preview_DoesNotComplete(t *testing.T) {
	s := withReconcileCandidates(t, newTestWorkSessionServer(t))
	ctx := context.Background()

	branch := "feature/preview-exact"
	exactTask := seedBranchedTask(t, s, "exact-match task", branch)

	// Unrelated task, NOT linked to any branch/pr_url — fuzzy-eligible.
	fuzzyTask, err := s.gtd.CreateTask(ctx, gtd.CreateTaskParams{Title: "unrelated fuzzy-eligible task"})
	if err != nil {
		t.Fatalf("CreateTask fuzzyTask: %v", err)
	}

	// Single PR: head_ref matches exactTask's branch (exact match) AND body
	// contains fuzzyTask's UUID (forced fuzzy match, rule 1 in
	// gtd.MatchPendingTasksFuzzy).
	body := "Closes work tracked as " + fuzzyTask.ID.String()
	previewRes := callReconcile(t, s, map[string]any{
		"merged_prs": []map[string]any{{
			"url":      "https://github.com/owner/repo/pull/91",
			"head_ref": branch,
			"body":     body,
			"repo":     "owner/repo",
		}},
	})
	preview := extractReconcileToken(t, previewRes)
	if len(preview.Matches) != 1 {
		t.Fatalf("preview matches = %d, want 1", len(preview.Matches))
	}
	if preview.Matches[0].TaskID != exactTask.ID.String() {
		t.Errorf("matched task = %s, want %s", preview.Matches[0].TaskID, exactTask.ID)
	}
	if preview.CandidateWrites != 1 {
		t.Errorf("candidate_writes = %d, want 1 (only the unrelated fuzzy-eligible task)", preview.CandidateWrites)
	}

	// Neither task must be completed yet.
	gotExact, err := s.gtd.GetTaskByID(ctx, exactTask.ID)
	if err != nil {
		t.Fatalf("GetTaskByID exactTask: %v", err)
	}
	if gotExact.Status == taskStatusCompleted {
		t.Error("exactly-matched task must not be completed during preview")
	}
	gotFuzzy, err := s.gtd.GetTaskByID(ctx, fuzzyTask.ID)
	if err != nil {
		t.Fatalf("GetTaskByID fuzzyTask: %v", err)
	}
	if gotFuzzy.Status == taskStatusCompleted {
		t.Error("fuzzy-eligible task must not be completed by preview (fuzzy candidates never auto-apply)")
	}
}

// TestMCPReconcileMergedPRs_ConfirmWithoutTokenFails covers confirm=true with
// no reconcile_token at all — must be rejected (otherwise the gate is
// useless).
func TestMCPReconcileMergedPRs_ConfirmWithoutTokenFails(t *testing.T) {
	s := newTestWorkSessionServer(t)

	r := callReconcileWithArgs(t, s, nil, map[string]any{"confirm": true})
	if !r.IsError {
		t.Fatalf("confirm=true without token must error, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "reconcile_token is required") {
		t.Errorf("error should mention 'reconcile_token is required', got: %s", resultText(r))
	}
}

// TestMCPReconcileMergedPRs_ConfirmWithoutFirstCallFails covers confirm=true
// with a fabricated token and no prior preview call — the in-memory map has
// no entry, so it must be rejected.
func TestMCPReconcileMergedPRs_ConfirmWithoutFirstCallFails(t *testing.T) {
	s := newTestWorkSessionServer(t)

	r := callReconcileWithArgs(t, s, nil, map[string]any{
		"confirm":         true,
		"reconcile_token": "fabricated-token-not-from-us",
	})
	if !r.IsError {
		t.Fatalf("confirm without prior preview must error, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "no pending reconciliation") {
		t.Errorf("error should mention 'no pending reconciliation', got: %s", resultText(r))
	}
}

// TestMCPReconcileMergedPRs_ExpiredTokenFails freezes s.nowFn to a known
// instant, previews to obtain a token, then advances the clock past
// reconcileTokenTTL before confirming — the confirm call must reject the
// expired token.
func TestMCPReconcileMergedPRs_ExpiredTokenFails(t *testing.T) {
	s := newTestWorkSessionServer(t)
	base := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	current := base
	s.nowFn = func() time.Time { return current }

	branch := "feature/expiring"
	seedBranchedTask(t, s, "expiring-task", branch)

	previewRes := callReconcile(t, s, map[string]any{
		"merged_prs": []map[string]any{{
			"url": "https://github.com/o/r/pull/1", "head_ref": branch, "repo": "o/r",
		}},
	})
	preview := extractReconcileToken(t, previewRes)

	// Advance past the TTL window.
	current = base.Add(reconcileTokenTTL + 5*time.Second)

	r := callReconcileWithArgs(t, s, nil, map[string]any{
		"confirm":         true,
		"reconcile_token": preview.ReconcileToken,
	})
	if !r.IsError {
		t.Fatalf("expired token must error, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "expired") {
		t.Errorf("error should mention 'expired', got: %s", resultText(r))
	}
}

// TestMCPReconcileMergedPRs_TokenReplayFails verifies a reconcile_token is
// single-use: the 1st confirm succeeds, the 2nd (same token) must error.
func TestMCPReconcileMergedPRs_TokenReplayFails(t *testing.T) {
	s := newTestWorkSessionServer(t)

	branch := "feature/replay"
	seedBranchedTask(t, s, "replay-task", branch)

	previewRes := callReconcile(t, s, map[string]any{
		"merged_prs": []map[string]any{{
			"url": "https://github.com/o/r/pull/2", "head_ref": branch, "repo": "o/r",
		}},
	})
	preview := extractReconcileToken(t, previewRes)

	first := callReconcileWithArgs(t, s, nil, map[string]any{
		"confirm":         true,
		"reconcile_token": preview.ReconcileToken,
	})
	if first.IsError {
		t.Fatalf("first confirm must succeed, got: %s", resultText(first))
	}

	second := callReconcileWithArgs(t, s, nil, map[string]any{
		"confirm":         true,
		"reconcile_token": preview.ReconcileToken,
	})
	if !second.IsError {
		t.Fatalf("token replay must error, got: %s", resultText(second))
	}
	if !strings.Contains(resultText(second), "no pending reconciliation") {
		t.Errorf("error should mention 'no pending reconciliation', got: %s", resultText(second))
	}
}

// TestMCPReconcileMergedPRs_ConfirmIgnoresDifferentPayload is the anti-tamper
// test: a token obtained from a small legitimate preview (batch A, matching
// only taskA) must NOT apply a different/larger payload (batch B, matching
// taskB) resent alongside confirm=true. Only taskA gets completed; taskB is
// untouched; payload B is fully ignored.
func TestMCPReconcileMergedPRs_ConfirmIgnoresDifferentPayload(t *testing.T) {
	s := newTestWorkSessionServer(t)
	ctx := context.Background()

	branchA, branchB := "feature/batch-a", "feature/batch-b"
	taskA := seedBranchedTask(t, s, "batch-a-task", branchA)
	taskB := seedBranchedTask(t, s, "batch-b-task", branchB)

	// Preview A: small legit batch matching only taskA.
	previewRes := callReconcile(t, s, map[string]any{
		"merged_prs": []map[string]any{{
			"url": "https://github.com/o/r/pull/10", "head_ref": branchA, "repo": "o/r",
		}},
	})
	preview := extractReconcileToken(t, previewRes)
	if len(preview.Matches) != 1 || preview.Matches[0].TaskID != taskA.ID.String() {
		t.Fatalf("preview A must match only taskA, got: %+v", preview.Matches)
	}

	// Confirm with the token from preview A, but resend a DIFFERENT payload
	// (batch B, matching taskB) alongside confirm=true.
	confirmRes := callReconcileWithArgs(t, s, map[string]any{
		"merged_prs": []map[string]any{{
			"url": "https://github.com/o/r/pull/20", "head_ref": branchB, "repo": "o/r",
		}},
	}, map[string]any{
		"confirm":         true,
		"reconcile_token": preview.ReconcileToken,
	})
	if confirmRes.IsError {
		t.Fatalf("confirm must succeed, got: %s", resultText(confirmRes))
	}
	var applied reconcileAppliedEnvelope
	if err := json.Unmarshal([]byte(resultText(confirmRes)), &applied); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if applied.Applied != 1 || len(applied.Matches) != 1 || applied.Matches[0].TaskID != taskA.ID.String() {
		t.Fatalf("confirm must apply ONLY taskA (from preview A), got: %+v", applied)
	}

	gotA, err := s.gtd.GetTaskByID(ctx, taskA.ID)
	if err != nil {
		t.Fatalf("GetTaskByID taskA: %v", err)
	}
	if gotA.Status != taskStatusCompleted {
		t.Errorf("taskA status = %q, want completed", gotA.Status)
	}
	gotB, err := s.gtd.GetTaskByID(ctx, taskB.ID)
	if err != nil {
		t.Fatalf("GetTaskByID taskB: %v", err)
	}
	if gotB.Status == taskStatusCompleted {
		t.Error("taskB must NOT be completed — the resent payload B must be ignored by the confirm call")
	}
}

// TestMCPReconcileMergedPRs_TokenMapBounded verifies that when
// maxPendingReconciles valid tokens are already stored, a further preview
// call is rejected with the "too many pending reconciliations" error rather
// than growing the map without bound. Mirrors TestDeleteTask_TokenMapBounded.
func TestMCPReconcileMergedPRs_TokenMapBounded(t *testing.T) {
	s := newTestWorkSessionServer(t)

	// Freeze time so all tokens we insert stay valid (non-expired) throughout.
	base := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	s.nowFn = func() time.Time { return base }

	// Prime the map with maxPendingReconciles valid entries using distinct
	// synthetic token keys — the cap check only counts entries, so the
	// primed entries themselves need no real match data.
	for i := 0; i < maxPendingReconciles; i++ {
		key := uuid.NewString()
		s.reconcileTokens.Store(key, reconcileConfirmation{
			expiresAt: base.Add(reconcileTokenTTL),
		})
	}

	// Round-3 Finding 1 gates token issuance on the match RESULT: a payload
	// that resolves to zero Matches/Ambiguous now short-circuits BEFORE the
	// cap check (see TestMCPReconcileMergedPRs_EmptyOrNonMatchingNeverConsumesTokenCap).
	// This test exercises the cap check itself, so the triggering call MUST
	// produce a genuine match — seed a real branch-matched task.
	seedBranchedTask(t, s, "bounded-task", "feature/bounded")

	r := callReconcile(t, s, map[string]any{
		"merged_prs": []map[string]any{{
			"url": "https://github.com/o/r/pull/99", "head_ref": "feature/bounded", "repo": "o/r",
		}},
	})
	if !r.IsError {
		t.Fatalf("expected cap error, got success: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "too many pending reconciliations") {
		t.Errorf("expected 'too many pending reconciliations', got: %s", resultText(r))
	}
}

// TestMCPReconcileMergedPRs_PruneExpiredOnWrite verifies that expired
// reconcile tokens are removed from the map during a preview call so the map
// does not grow indefinitely through accumulated dead entries. Mirrors
// TestDeleteTask_PruneExpiredOnWrite.
func TestMCPReconcileMergedPRs_PruneExpiredOnWrite(t *testing.T) {
	s := newTestWorkSessionServer(t)

	base := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	current := base
	s.nowFn = func() time.Time { return current }

	// Seed 256 entries with distinct synthetic keys directly, all expiring at
	// base+TTL.
	for i := 0; i < 256; i++ {
		key := uuid.NewString()
		s.reconcileTokens.Store(key, reconcileConfirmation{
			expiresAt: base.Add(reconcileTokenTTL),
		})
	}

	// Advance clock past the TTL — all 256 entries are now expired.
	current = base.Add(reconcileTokenTTL + 5*time.Second)

	// Round-3 Finding 1 only stores a new token when the call resolves to a
	// genuine Match/Ambiguous — seed a real branch-matched task so this
	// preview call actually reaches the prune+store code path being tested.
	seedBranchedTask(t, s, "prune-task", "feature/prune")

	// Issue one preview call. The prune loop should delete all 256 expired
	// entries before storing the new one, leaving exactly 1 entry.
	r := callReconcile(t, s, map[string]any{
		"merged_prs": []map[string]any{{
			"url": "https://github.com/o/r/pull/100", "head_ref": "feature/prune", "repo": "o/r",
		}},
	})
	if r.IsError {
		t.Fatalf("preview after prune must succeed, got: %s", resultText(r))
	}

	var remaining int
	s.reconcileTokens.Range(func(_, _ any) bool {
		remaining++
		return true
	})
	if remaining != 1 {
		t.Errorf("after prune expected 1 entry in reconcileTokens, got %d", remaining)
	}
}

// TestMCPReconcileMergedPRs_EmptyPayloadNeverConsumesTokenCap is the
// regression test for security-engineer Finding 1 (token-cap DoS via free
// empty-payload preview calls): a completely empty merged_prs:[] payload
// costs an attacker nothing, so the handler MUST reject it BEFORE the
// prune+cap+token-issuance block runs — otherwise 256 (maxPendingReconciles)
// zero-cost calls fill s.reconcileTokens and every legitimate preview call
// starts failing with "too many pending reconciliations in flight".
//
// Loops 257 (maxPendingReconciles+1) empty-payload preview calls, asserts
// every single one returns the early-return error (never a token), asserts
// s.reconcileTokens never grows off of them, then proves a legitimate
// non-empty preview call still succeeds afterward — demonstrating the empty
// calls consumed zero cap budget. This also serves as the reviewer's
// suggested dedicated empty-payload test (Finding 4).
func TestMCPReconcileMergedPRs_EmptyPayloadNeverConsumesTokenCap(t *testing.T) {
	s := newTestWorkSessionServer(t)

	const emptyCallCount = maxPendingReconciles + 1 // 257
	for i := 0; i < emptyCallCount; i++ {
		r := callReconcile(t, s, map[string]any{"merged_prs": []map[string]any{}})
		if !r.IsError {
			t.Fatalf("call %d: expected error for empty merged_prs, got success: %s", i, resultText(r))
		}
		if !strings.Contains(resultText(r), "merged_prs must contain at least 1 entry") {
			t.Fatalf("call %d: error must mention the empty merged_prs requirement, got: %s",
				i, resultText(r))
		}
	}

	var tokenCount int
	s.reconcileTokens.Range(func(_, _ any) bool {
		tokenCount++
		return true
	})
	if tokenCount != 0 {
		t.Fatalf("reconcileTokens grew to %d entries from %d empty-payload calls, want 0",
			tokenCount, emptyCallCount)
	}

	// A legitimate (non-empty) preview call must still succeed — proves the
	// empty-payload loop above never consumed any of the maxPendingReconciles
	// cap budget.
	branch := "feature/empty-payload-guard"
	seedBranchedTask(t, s, "legit-after-empty-loop", branch)
	legitRes := callReconcile(t, s, map[string]any{
		"merged_prs": []map[string]any{{
			"url": "https://github.com/o/r/pull/321", "head_ref": branch, "repo": "o/r",
		}},
	})
	preview := extractReconcileToken(t, legitRes)
	if len(preview.Matches) != 1 {
		t.Fatalf("legitimate preview matches = %d, want 1", len(preview.Matches))
	}
}

// TestMCPReconcileMergedPRs_ConfirmDoesNotOverwriteCancelledTask is the
// regression test for security-engineer Finding 2 (CWE-367 TOCTOU): a task
// cancelled by the user AFTER preview but BEFORE confirm must NOT be silently
// re-completed by a still-valid reconcile_token. The guarded UPDATE in
// applyOnePRMatch (internal/gtd/store.go + internal/storage/sqlite/gtd.go)
// now requires status IN ('pending','in_progress') instead of merely
// status != 'completed', so the confirm's guarded UPDATE affects 0 rows for a
// task whose status drifted to 'cancelled' in the confirm window, and
// `applied` reflects that the stale match was skipped rather than applied.
func TestMCPReconcileMergedPRs_ConfirmDoesNotOverwriteCancelledTask(t *testing.T) {
	s := newTestWorkSessionServer(t)
	ctx := context.Background()

	branch := "feature/toctou-cancel"
	task := seedBranchedTask(t, s, "toctou-task", branch)

	// Step 1 — preview. Computes + stores the match, issues a reconcile_token.
	previewRes := callReconcile(t, s, map[string]any{
		"merged_prs": []map[string]any{{
			"url": "https://github.com/o/r/pull/123", "head_ref": branch, "repo": "o/r",
		}},
	})
	preview := extractReconcileToken(t, previewRes)
	if len(preview.Matches) != 1 || preview.Matches[0].TaskID != task.ID.String() {
		t.Fatalf("preview must match exactly task, got: %+v", preview.Matches)
	}

	// The user cancels the task IN THE WINDOW between preview and confirm
	// (e.g. via set_task_status). Simulate this directly via the store, same
	// as the underlying MCP tool would do.
	if _, err := s.gtd.UpdateTaskStatus(ctx, task.ID, gtd.TaskStatusCancelled); err != nil {
		t.Fatalf("UpdateTaskStatus cancel: %v", err)
	}

	// Step 2 — confirm using the ORIGINAL (still-valid, non-expired) token.
	confirmRes := callReconcileWithArgs(t, s, nil, map[string]any{
		"confirm":         true,
		"reconcile_token": preview.ReconcileToken,
	})
	if confirmRes.IsError {
		t.Fatalf("confirm must succeed (token still valid), got: %s", resultText(confirmRes))
	}
	var applied reconcileAppliedEnvelope
	if err := json.Unmarshal([]byte(resultText(confirmRes)), &applied); err != nil {
		t.Fatalf("unmarshal confirm envelope: %v\nbody=%s", err, resultText(confirmRes))
	}
	if applied.Applied != 0 {
		t.Errorf("applied = %d, want 0 — a cancelled task must not be silently re-completed",
			applied.Applied)
	}

	// Hard requirement: the task's DB status must STILL be cancelled, never
	// flipped to completed by the stale confirm.
	got, err := s.gtd.GetTaskByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if got.Status != string(gtd.TaskStatusCancelled) {
		t.Errorf("task status = %q, want %q — must not be silently re-completed by a stale confirm",
			got.Status, gtd.TaskStatusCancelled)
	}
}

// TestMCPReconcileMergedPRs_NonMatchingPayloadNeverConsumesTokenCap is the
// regression test for round-3 Finding 1 (token-cap DoS via schema-valid,
// non-matching payloads). Round 2's fix only rejected a completely EMPTY
// merged_prs:[] payload (see TestMCPReconcileMergedPRs_EmptyPayloadNeverConsumesTokenCap,
// a DIFFERENT code path — the len(p.MergedPRs)==0 short-circuit runs BEFORE
// format validation even executes). It did nothing against a
// syntactically-valid, non-empty, but semantically-bogus payload: a
// well-formed GitHub PR URL / branch name that matches ZERO real
// pending/in_progress tasks still passed reconcileMCPValidate, produced
// result.Matches AND result.Ambiguous both empty, and STILL unconditionally
// reached token issuance — consuming one of the maxPendingReconciles (256)
// cap slots at zero real cost (no knowledge of any actual task's branch name
// or PR URL required).
//
// Loops maxPendingReconciles+1 (257) such payloads, each referencing a
// distinct, never-seeded fake branch/PR so none can accidentally match a
// real task, asserting every single call:
//   - succeeds (IsError == false) — this is a normal, successful outcome,
//     not an error, per the Finding 1 fix design
//   - reports status="no_match"
//   - carries NO reconcile_token field
//
// then asserts s.reconcileTokens never grew off of them, then proves a
// legitimate (real-match) preview call still succeeds afterward — together
// demonstrating the 257 non-matching calls consumed zero cap budget.
func TestMCPReconcileMergedPRs_NonMatchingPayloadNeverConsumesTokenCap(t *testing.T) {
	s := newTestWorkSessionServer(t)

	const nonMatchingCallCount = maxPendingReconciles + 1 // 257
	for i := 0; i < nonMatchingCallCount; i++ {
		r := callReconcile(t, s, map[string]any{
			"merged_prs": []map[string]any{{
				"url":      fmt.Sprintf("https://github.com/o/r/pull/%d", 900000+i),
				"head_ref": fmt.Sprintf("feature/never-seeded-%d", i),
				"repo":     "o/r",
			}},
		})
		if r.IsError {
			t.Fatalf("call %d: expected success (no_match), got error: %s", i, resultText(r))
		}
		body := resultText(r)
		if !strings.Contains(body, `"status":"no_match"`) {
			t.Fatalf("call %d: expected status=no_match, got: %s", i, body)
		}
		// Check for the JSON KEY specifically (quote-colon), not a bare
		// substring — the "no_match" response's own message text legitimately
		// contains the words "reconcile_token" (e.g. "No reconcile_token
		// issued."), so a naive Contains(body, "reconcile_token") would
		// false-positive against that prose.
		if strings.Contains(body, `"reconcile_token":`) {
			t.Fatalf("call %d: no_match response must not carry a reconcile_token field, got: %s", i, body)
		}
	}

	var tokenCount int
	s.reconcileTokens.Range(func(_, _ any) bool {
		tokenCount++
		return true
	})
	if tokenCount != 0 {
		t.Fatalf("reconcileTokens grew to %d entries from %d non-matching payload calls, want 0",
			tokenCount, nonMatchingCallCount)
	}

	// A legitimate (real-match) preview call must still succeed — proves the
	// non-matching loop above consumed zero cap budget.
	branch := "feature/non-matching-guard"
	seedBranchedTask(t, s, "legit-after-non-matching-loop", branch)
	legitRes := callReconcile(t, s, map[string]any{
		"merged_prs": []map[string]any{{
			"url": "https://github.com/o/r/pull/999999", "head_ref": branch, "repo": "o/r",
		}},
	})
	preview := extractReconcileToken(t, legitRes)
	if len(preview.Matches) != 1 {
		t.Fatalf("legitimate preview matches = %d, want 1", len(preview.Matches))
	}
}

// TestMCPReconcileMergedPRs_ConfirmDoesNotWriteFalseAutoAppliedCandidate is
// the regression test for round-3 Finding 2 (false audit-trail row for
// TOCTOU-skipped tasks). Before this fix, handleReconcileMergedPRsConfirm
// called cs.WriteAutoApplied for EVERY entry in rec.matches unconditionally,
// regardless of whether that specific task's guarded UPDATE inside
// BatchCompleteTasksByPRMatch actually affected a row. A task correctly
// protected by the round-2 TOCTOU guard (cancelled between preview and
// confirm, tasks.status correctly stays 'cancelled') still got a
// completion_candidates row PERSISTED with status='auto_applied' — a false,
// durable claim that reconcile completed a task it never touched.
//
// Round 2's own regression test
// (TestMCPReconcileMergedPRs_ConfirmDoesNotOverwriteCancelledTask) did NOT
// catch this: it calls newTestWorkSessionServer directly WITHOUT
// withReconcileCandidates, so s.completionCandidates / s.reconcileCandidateStore()
// stays nil and the WriteAutoApplied loop never executes at all in that
// test — a test-harness blind spot, not evidence the bug didn't exist.
// cmd/server/main.go:411 wires a REAL, non-nil completion-candidate store in
// production, so this bug was reachable there.
//
// This test wires a REAL SQLite-backed candidate store via
// withReconcileCandidatesDB and queries completion_candidates DIRECTLY (not
// just the tool's response JSON, which could under-report drift) to prove no
// false row is written for the cancelled task.
func TestMCPReconcileMergedPRs_ConfirmDoesNotWriteFalseAutoAppliedCandidate(t *testing.T) {
	s, candDB := withReconcileCandidatesDB(t, newTestWorkSessionServer(t))
	ctx := context.Background()

	branch := "feature/toctou-false-candidate"
	task := seedBranchedTask(t, s, "toctou-candidate-task", branch)

	// Step 1 — preview. Computes + stores the match, issues a reconcile_token.
	previewRes := callReconcile(t, s, map[string]any{
		"merged_prs": []map[string]any{{
			"url": "https://github.com/o/r/pull/456", "head_ref": branch, "repo": "o/r",
		}},
	})
	preview := extractReconcileToken(t, previewRes)
	if len(preview.Matches) != 1 || preview.Matches[0].TaskID != task.ID.String() {
		t.Fatalf("preview must match exactly task, got: %+v", preview.Matches)
	}

	// The user cancels the task IN THE WINDOW between preview and confirm
	// (e.g. via set_task_status). Simulate this directly via the store, same
	// as the underlying MCP tool would do.
	if _, err := s.gtd.UpdateTaskStatus(ctx, task.ID, gtd.TaskStatusCancelled); err != nil {
		t.Fatalf("UpdateTaskStatus cancel: %v", err)
	}

	// Step 2 — confirm using the ORIGINAL (still-valid, non-expired) token.
	confirmRes := callReconcileWithArgs(t, s, nil, map[string]any{
		"confirm":         true,
		"reconcile_token": preview.ReconcileToken,
	})
	if confirmRes.IsError {
		t.Fatalf("confirm must succeed (token still valid), got: %s", resultText(confirmRes))
	}
	var applied reconcileAppliedEnvelope
	if err := json.Unmarshal([]byte(resultText(confirmRes)), &applied); err != nil {
		t.Fatalf("unmarshal confirm envelope: %v\nbody=%s", err, resultText(confirmRes))
	}
	if applied.Applied != 0 {
		t.Fatalf("applied = %d, want 0 — a cancelled task must not be silently re-completed",
			applied.Applied)
	}
	if applied.CandidateWrites != 0 {
		t.Errorf("candidate_writes = %d, want 0 — no candidate row should be written for a task the guard skipped",
			applied.CandidateWrites)
	}

	// Hard requirement (independent of the response JSON, which the bug did
	// NOT misreport — candidate_writes was already wrong pre-fix too, but the
	// real bug is the PERSISTED row): NO completion_candidates row exists for
	// this task with status='auto_applied'.
	if n := countAutoAppliedCandidates(t, candDB, task.ID); n != 0 {
		t.Errorf("completion_candidates has %d auto_applied row(s) for task %s cancelled mid-window, want 0",
			n, task.ID)
	}

	// Parity check (re-verified here under real candidate-store wiring,
	// unlike round 2's blind-spot test): the task's DB status is still
	// cancelled, never flipped to completed by the stale confirm.
	got, err := s.gtd.GetTaskByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if got.Status != string(gtd.TaskStatusCancelled) {
		t.Errorf("task status = %q, want %q", got.Status, gtd.TaskStatusCancelled)
	}
}
