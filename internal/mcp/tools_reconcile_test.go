package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/completioncandidate"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
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
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open candidate db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
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
	if _, err := db.ExecContext(context.Background(), ddl); err != nil {
		t.Fatalf("apply candidate schema: %v", err)
	}
	cs := completioncandidate.NewSQLiteStore(db, "")
	s.WithCompletionCandidates(cs)
	return s
}

// callReconcile invokes the reconcile_merged_prs handler directly.
func callReconcile(t *testing.T, s *Server, payload any) *mcpmsg.CallToolResult {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = map[string]any{"payload": string(raw)}
	res, err := s.handleReconcileMergedPRs(context.Background(), req)
	if err != nil {
		t.Fatalf("handleReconcileMergedPRs error: %v", err)
	}
	return res
}

// TestMCPReconcileMergedPRs_ExactMatch_AutoApplies covers the MCP-side happy path.
func TestMCPReconcileMergedPRs_ExactMatch_AutoApplies(t *testing.T) {
	s := withReconcileCandidates(t, newTestWorkSessionServer(t))
	ctx := context.Background()

	task, err := s.gtd.CreateTask(ctx, gtd.CreateTaskParams{Title: "mcp-recon"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	branch := "feature/mcp-x"
	if _, err := s.gtd.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{BranchName: &branch}); err != nil {
		t.Fatalf("seed branch: %v", err)
	}

	r := callReconcile(t, s, map[string]any{
		"merged_prs": []map[string]any{{
			"url":       "https://github.com/owner/repo/pull/77",
			"head_ref":  branch,
			"merged_at": "2026-05-18T12:00:00Z",
			"repo":      "owner/repo",
		}},
	})
	if r.IsError {
		t.Fatalf("reconcile error: %s", resultText(r))
	}
	var resp struct {
		Applied int `json:"applied"`
		Matches []struct {
			TaskID string `json:"task_id"`
			Reason string `json:"reason"`
		} `json:"matches"`
		CandidateWrites int `json:"candidate_writes"`
	}
	if err := json.Unmarshal([]byte(resultText(r)), &resp); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, resultText(r))
	}
	if resp.Applied != 1 || len(resp.Matches) != 1 {
		t.Errorf("applied=%d matches=%d, want 1+1", resp.Applied, len(resp.Matches))
	}
	if resp.Matches[0].TaskID != task.ID.String() {
		t.Errorf("matched task = %s, want %s", resp.Matches[0].TaskID, task.ID)
	}
	if resp.CandidateWrites != 1 {
		t.Errorf("candidate_writes = %d, want 1 (SQLite store satisfies WriteAutoApplied)",
			resp.CandidateWrites)
	}

	// Verify task is closed.
	got, err := s.gtd.GetTaskByID(ctx, task.ID)
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

// TestMCPReconcileMergedPRs_Idempotent — second call returns 0 changes.
func TestMCPReconcileMergedPRs_Idempotent(t *testing.T) {
	s := newTestWorkSessionServer(t)
	ctx := context.Background()

	task, err := s.gtd.CreateTask(ctx, gtd.CreateTaskParams{Title: "mcp-idem"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	branch := "feature/mcp-idem"
	if _, err := s.gtd.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{BranchName: &branch}); err != nil {
		t.Fatalf("seed branch: %v", err)
	}

	payload := map[string]any{
		"merged_prs": []map[string]any{{
			"url": "https://github.com/o/r/pull/88", "head_ref": branch,
			"repo": "o/r",
		}},
	}

	r1 := callReconcile(t, s, payload)
	var first struct{ Applied int }
	_ = json.Unmarshal([]byte(resultText(r1)), &first)
	if first.Applied != 1 {
		t.Fatalf("first applied = %d, want 1 (body: %s)", first.Applied, resultText(r1))
	}

	r2 := callReconcile(t, s, payload)
	var second struct {
		Applied int `json:"applied"`
		NoMatch int `json:"no_match"`
	}
	if err := json.Unmarshal([]byte(resultText(r2)), &second); err != nil {
		t.Fatalf("unmarshal second: %v", err)
	}
	if second.Applied != 0 {
		t.Errorf("second applied = %d, want 0", second.Applied)
	}
	if second.NoMatch != 1 {
		t.Errorf("second no_match = %d, want 1", second.NoMatch)
	}
}
