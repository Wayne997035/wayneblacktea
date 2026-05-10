package mcp

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/watchdog"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// taskStatusPending and taskStatusInProgress are the literal status string
// values used by `db.Task.Status`. Hoisted to package-level constants so the
// same literal isn't repeated across multiple files (goconst).
const (
	taskStatusPending    = "pending"
	taskStatusInProgress = "in_progress"
)

func (s *Server) registerHealthTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool("system_health",
		mcp.WithDescription(
			"Returns a snapshot of the personal-OS state: in-progress task "+
				"count, stuck tasks (in_progress > 4h), pending proposals, "+
				"due reviews, recent MCP tool invocations. CALL when you want "+
				"to know if Claude has been forgetting to close out work.",
		),
		mcp.WithNumber("recent_calls", mcp.Description("How many recent tool calls to include (default 20)")),
		mcp.WithNumber("stuck_threshold_hours", mcp.Description("Tasks in_progress longer than this are flagged stuck (default 4)")),
	), s.handleSystemHealth)
}

// healthSnapshot is the JSON shape returned by system_health.
type healthSnapshot struct {
	GeneratedAt      time.Time           `json:"generated_at"`
	Workspace        string              `json:"workspace,omitempty"`
	Tasks            taskHealth          `json:"tasks"`
	PendingProposals proposalHealth      `json:"pending_proposals"`
	DueReviews       reviewHealth        `json:"due_reviews"`
	ToolCallSummary  map[string]int      `json:"tool_call_counts"`
	RecentCalls      []watchdog.ToolCall `json:"recent_calls"`
	ForgottenSignals []string            `json:"forgotten_signals,omitempty"`
	CompletionDrift  []DriftCandidate    `json:"completion_drift_candidates,omitempty"`
}

// DriftCandidate is a pending task whose description references artifacts that
// already exist in the codebase — a signal that complete_task may have been missed.
type DriftCandidate struct {
	TaskID   string   `json:"task_id"`
	Title    string   `json:"title"`
	Evidence []string `json:"evidence"` // file paths / migration numbers found on disk
}

type taskHealth struct {
	InProgress int      `json:"in_progress"`
	Stuck      int      `json:"stuck"`
	StuckIDs   []string `json:"stuck_ids,omitempty"`
}

type proposalHealth struct {
	Pending int `json:"pending"`
}

type reviewHealth struct {
	Due int `json:"due"`
}

func (s *Server) handleSystemHealth(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	recentN := int(numberArg(args, "recent_calls"))
	if recentN <= 0 {
		recentN = 20
	}
	stuckHours := int(numberArg(args, "stuck_threshold_hours"))
	if stuckHours <= 0 {
		stuckHours = 4
	}

	repoRoot, _ := findRepoRoot() // ignore error; drift check is advisory

	snap := healthSnapshot{
		GeneratedAt:     time.Now().UTC(),
		ToolCallSummary: s.watchdog.CountByTool(),
		RecentCalls:     s.watchdog.Recent(recentN),
	}
	snap.Workspace = workspaceIDString(s.gtd.WorkspaceID())

	var tasks []db.Task
	if fetched, err := s.gtd.Tasks(ctx, nil); err == nil {
		tasks = fetched
		stuckCutoff := time.Now().Add(-time.Duration(stuckHours) * time.Hour)
		for _, t := range tasks {
			if t.Status != taskStatusInProgress {
				continue
			}
			snap.Tasks.InProgress++
			if t.UpdatedAt.Valid && t.UpdatedAt.Time.Before(stuckCutoff) {
				snap.Tasks.Stuck++
				snap.Tasks.StuckIDs = append(snap.Tasks.StuckIDs, t.ID.String())
			}
		}
	}

	if proposals, err := s.proposal.ListPending(ctx); err == nil {
		snap.PendingProposals.Pending = len(proposals)
	}

	if n, err := s.learning.CountDueReviews(ctx); err == nil {
		snap.DueReviews.Due = n
	}

	if tasks != nil {
		snap.CompletionDrift = detectCompletionDrift(tasks, repoRoot)
	}

	signals := detectForgottenSignals(snap, s.watchdog)
	if len(snap.CompletionDrift) > 0 {
		signals = append(signals, fmt.Sprintf(
			"%d pending task(s) reference files that exist in the repo — check if complete_task was missed.",
			len(snap.CompletionDrift),
		))
	}
	snap.ForgottenSignals = signals

	return jsonText(snap)
}

// detectForgottenSignals applies a few cheap heuristics to point out
// likely Claude omissions. Each signal is human-readable and short.
func detectForgottenSignals(snap healthSnapshot, w *watchdog.Watchdog) []string {
	var signals []string

	if snap.Tasks.Stuck > 0 {
		signals = append(signals,
			"There are stuck in-progress tasks. Claude likely forgot to call complete_task after finishing.")
	}

	if snap.PendingProposals.Pending >= 5 {
		signals = append(signals,
			"5+ pending proposals queued. Either ask Claude to confirm/reject them, or it stopped triaging.")
	}

	counts := snap.ToolCallSummary
	addTaskCount := counts["add_task"] + counts["confirm_plan"]
	completeCount := counts["complete_task"]
	if addTaskCount >= 3 && completeCount == 0 {
		signals = append(signals,
			"Several tasks added in this session but none completed. Are any actually done?")
	}

	// Decision logged without a session-start get_today_context call:
	// flag because it usually means Claude skipped MANDATORY session-start
	// recall. (Cosmetic-ish — only fire when there were enough decisions.)
	decisionCount := counts["log_decision"] + counts["confirm_plan"]
	if decisionCount >= 2 && w.LastSuccessful("get_today_context").IsZero() {
		signals = append(signals,
			"Decisions logged but get_today_context never called this session — Claude likely skipped session-start recall.")
	}

	return signals
}

// workspaceIDString turns the store's pgtype.UUID workspace into a string,
// or returns "(unscoped)" when WORKSPACE_ID is unset.
func workspaceIDString(ws pgtype.UUID) string {
	if !ws.Valid {
		return "(unscoped)"
	}
	// pgtype.UUID stores the 16 raw bytes; render as canonical 8-4-4-4-12.
	b := ws.Bytes
	return hex.EncodeToString(b[0:4]) + "-" +
		hex.EncodeToString(b[4:6]) + "-" +
		hex.EncodeToString(b[6:8]) + "-" +
		hex.EncodeToString(b[8:10]) + "-" +
		hex.EncodeToString(b[10:16])
}

var (
	reFilePath        = regexp.MustCompile(`(?:internal|cmd|web|migrations|sql|scripts|build)/[^\s"']+`)
	reMigrationNumber = regexp.MustCompile(`\b[0-9]{6}\b`)
)

// extractKeywords pulls two classes of keyword from desc:
//   - file paths starting with a known top-level directory segment
//   - 6-digit migration numbers (e.g. 000025)
//
// Returns de-duplicated results, capped at 20 to bound per-task disk cost.
func extractKeywords(desc string) []string {
	seen := make(map[string]struct{})
	var out []string

	add := func(kw string) {
		if _, ok := seen[kw]; ok {
			return
		}
		seen[kw] = struct{}{}
		out = append(out, kw)
	}

	for _, m := range reFilePath.FindAllString(desc, -1) {
		add(m)
	}
	for _, m := range reMigrationNumber.FindAllString(desc, -1) {
		add(m)
	}

	if len(out) > 20 {
		out = out[:20]
	}
	return out
}

// keywordExistsOnDisk returns true when the keyword corresponds to an artifact
// on disk beneath repoRoot. Adversarial-input safe: rejects ".." segments,
// ASCII control bytes (incl. NUL / CR / LF), and any join that escapes repoRoot.
//
//   - File paths: stat(repoRoot/kw)
//   - 6-digit migration numbers: glob migrations/<kw>*.sql
func keywordExistsOnDisk(kw, repoRoot string) bool {
	if strings.Contains(kw, "..") {
		return false
	}
	for _, ch := range kw {
		if ch < 0x20 {
			return false
		}
	}
	boundary := filepath.Clean(repoRoot)
	sep := string(filepath.Separator)

	if strings.Contains(kw, "/") {
		joined := filepath.Join(boundary, kw)
		if joined != boundary && !strings.HasPrefix(joined, boundary+sep) {
			return false
		}
		_, err := os.Stat(joined)
		return err == nil
	}

	// Migration number branch — defensive: must be all digits.
	for _, ch := range kw {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	pattern := filepath.Join(boundary, "migrations", kw+"*.sql")
	if !strings.HasPrefix(pattern, boundary+sep) {
		return false
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return false
	}
	return len(matches) > 0
}

// maxDriftCandidates caps the number of drift candidates returned per
// invocation, bounding worst-case stat/glob load when many pending tasks
// happen to reference on-disk artifacts.
const maxDriftCandidates = 50

// detectCompletionDrift scans pending / in-progress tasks for descriptions
// that reference artifacts already present in repoRoot, surfacing them as
// advisory drift candidates. Returns nil when repoRoot is empty.
//
// Both `pending` and `in_progress` are inspected: an in_progress task whose
// referenced artifacts already exist on disk is itself a missed
// complete_task signal.
func detectCompletionDrift(tasks []db.Task, repoRoot string) []DriftCandidate {
	if repoRoot == "" {
		return nil
	}

	var candidates []DriftCandidate
	for _, t := range tasks {
		if len(candidates) >= maxDriftCandidates {
			break
		}
		if t.Status != taskStatusPending && t.Status != taskStatusInProgress {
			continue
		}
		if !t.Description.Valid || t.Description.String == "" {
			continue
		}
		keywords := extractKeywords(t.Description.String)
		var evidence []string
		for _, kw := range keywords {
			if keywordExistsOnDisk(kw, repoRoot) {
				evidence = append(evidence, kw)
			}
		}
		if len(evidence) > 0 {
			// Strip ASCII control characters from the title before surfacing
			// it (per backend-security-design.md §5.4). Tabs are preserved.
			title := strings.Map(func(r rune) rune {
				if r < 0x20 && r != '\t' {
					return -1
				}
				return r
			}, t.Title)
			candidates = append(candidates, DriftCandidate{
				TaskID:   t.ID.String(),
				Title:    title,
				Evidence: evidence,
			})
		}
	}
	return candidates
}

// findRepoRoot returns the git repository root by walking up from cwd.
// Returns "" when no .git directory is found within 6 levels.
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", errors.New("no git repo found")
}
