package mcp

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/discipline"
	"github.com/Wayne997035/wayneblacktea/internal/validator"
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
	GeneratedAt           time.Time           `json:"generated_at"`
	Workspace             string              `json:"workspace,omitempty"`
	Tasks                 taskHealth          `json:"tasks"`
	PendingProposals      proposalHealth      `json:"pending_proposals"`
	DueReviews            reviewHealth        `json:"due_reviews"`
	ToolCallSummary       map[string]int      `json:"tool_call_counts"`
	RecentCalls           []watchdog.ToolCall `json:"recent_calls"`
	ForgottenSignals      []string            `json:"forgotten_signals,omitempty"`
	CompletionDrift       []DriftCandidate    `json:"completion_drift_candidates,omitempty"`
	Discipline            disciplineHealth    `json:"discipline,omitempty"`
	VaguePendingTaskCount int                 `json:"vague_pending_task_count"`
	VagueSampleIDs        []string            `json:"vague_sample_ids,omitempty"`
}

// maxVagueSampleIDs caps how many vague-task IDs are surfaced per snapshot.
// Matches the SA spec — enough to inspect a leak signal, small enough to keep
// the JSON response bounded.
const maxVagueSampleIDs = 5

// disciplineHealth is the per-snapshot discipline drift summary surfaced by
// system_health. DriftCount24h is the number of mutating MCP tool calls in
// the last 24 hours that did NOT have a log_decision / confirm_plan in the
// same session within the preceding driftWindow (15 minutes).
type disciplineHealth struct {
	DriftCount24h int                `json:"drift_count_24h"`
	RecentDrifts  []disciplineSample `json:"recent_drifts,omitempty"`
}

// disciplineSample is one drift record (the mutating call that lacked a
// preceding decision). Capped at maxDisciplineSamples per snapshot.
type disciplineSample struct {
	ToolName   string    `json:"tool_name"`
	ObservedAt time.Time `json:"observed_at"`
	RepoName   string    `json:"repo_name,omitempty"`
}

const (
	// driftWindow is how far back from a mutating call we look for a
	// log_decision / confirm_plan event in the same session. Anything
	// outside this window means the mutating call was NOT preceded by an
	// explicit decision and therefore counts as drift.
	driftWindow = 15 * time.Minute
	// disciplineLookback is the time horizon for "drift in the last 24h"
	// — the headline number on the system_health output.
	disciplineLookback = 24 * time.Hour
	// maxDisciplineSamples caps how many drift samples we surface per
	// snapshot. Bounded so the JSON response stays small.
	maxDisciplineSamples = 5
	// disciplineFetchLimit caps the rows we pull from the store for the
	// 24h window. Far above expected real-world volume but still bounded.
	disciplineFetchLimit = 500
)

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
		snap.VaguePendingTaskCount, snap.VagueSampleIDs = countVaguePending(tasks)
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

	snap.Discipline = s.collectDisciplineHealth(ctx)

	signals := detectForgottenSignals(snap, s.watchdog)
	if len(snap.CompletionDrift) > 0 {
		signals = append(signals, fmt.Sprintf(
			"%d pending task(s) reference files that exist in the repo — check if complete_task was missed.",
			len(snap.CompletionDrift),
		))
	}
	if snap.Discipline.DriftCount24h > 0 {
		signals = append(signals, fmt.Sprintf(
			"%d mutating MCP calls in last 24h with no preceding log_decision — likely undocumented changes.",
			snap.Discipline.DriftCount24h,
		))
	}
	if snap.VaguePendingTaskCount > 0 {
		signals = append(signals, fmt.Sprintf(
			"%d pending/in_progress task(s) flagged by validator.CheckVagueness — descriptions are placeholders or too vague.",
			snap.VaguePendingTaskCount,
		))
	}
	snap.ForgottenSignals = signals

	return jsonText(snap)
}

// collectDisciplineHealth queries the discipline store for mutating calls in
// the last 24 hours and computes drift = mutating calls without a
// log_decision / confirm_plan in the same session within driftWindow before
// the mutating call.
//
// Decision (chosen here, documented for reviewer): when several mutating
// calls happen back-to-back AFTER a single log_decision, only the calls
// outside the 15-minute window are drift. The window is sliding per call,
// not "one decision authorises one mutation". This matches how a human
// would judge "did Claude document the change?" — a fresh decision
// continues to cover follow-up mutations for the next 15 min.
//
// Errors are logged and swallowed — a missing/broken discipline store must
// not break system_health (matches the disciplineMiddleware policy).
func (s *Server) collectDisciplineHealth(ctx context.Context) disciplineHealth {
	if s.discipline == nil {
		return disciplineHealth{}
	}

	now := time.Now().UTC()
	since := now.Add(-disciplineLookback)

	mutatingEvents, err := s.discipline.RecentMutating(ctx, since, disciplineFetchLimit)
	if err != nil {
		slog.Warn("collectDisciplineHealth: RecentMutating failed", "error", err)
		return disciplineHealth{}
	}
	if len(mutatingEvents) == 0 {
		return disciplineHealth{}
	}

	// Cache decision timestamps per session so we don't re-query for each
	// mutating event in the same session. The cache scope is one
	// system_health call.
	decisionCache := make(map[string][]time.Time)
	getDecisions := func(sessionID string) []time.Time {
		if ts, ok := decisionCache[sessionID]; ok {
			return ts
		}
		// Look back driftWindow further than the 24h horizon so a decision
		// just before `since` can still cover an early mutation in the
		// window.
		ts, qErr := s.discipline.RecentDecisionTimes(ctx, sessionID, since.Add(-driftWindow))
		if qErr != nil {
			slog.Warn("collectDisciplineHealth: RecentDecisionTimes failed",
				"session_id", sessionID,
				"error", qErr,
			)
			ts = nil
		}
		decisionCache[sessionID] = ts
		return ts
	}

	health := disciplineHealth{}
	for _, ev := range mutatingEvents {
		decisions := getDecisions(ev.SessionID)
		if hasDecisionInWindow(ev.ObservedAt, decisions) {
			continue
		}
		health.DriftCount24h++
		if len(health.RecentDrifts) < maxDisciplineSamples {
			health.RecentDrifts = append(health.RecentDrifts, disciplineSample{
				ToolName:   ev.ToolName,
				ObservedAt: ev.ObservedAt,
				RepoName:   ev.RepoName,
			})
		}
	}
	return health
}

// hasDecisionInWindow returns true when at least one decision timestamp falls
// inside [observedAt - driftWindow, observedAt]. decisionTimes is expected
// in any order (we walk the slice).
func hasDecisionInWindow(observedAt time.Time, decisionTimes []time.Time) bool {
	if len(decisionTimes) == 0 {
		return false
	}
	lower := observedAt.Add(-driftWindow)
	for _, t := range decisionTimes {
		// Ignore decisions logged AFTER the mutating call — they can't be
		// "preceding". Tie at observedAt is treated as preceding (the
		// timestamps came from the same DB clock; both nearly-coincident
		// rows count as the user logging a decision in the same call).
		if (t.Equal(observedAt) || t.Before(observedAt)) && !t.Before(lower) {
			return true
		}
	}
	return false
}

// EvaluateDisciplineDrift is a test-friendly accessor that exposes the
// drift-counting logic without spinning up a full Server. Production code
// goes through collectDisciplineHealth above; tests use this to validate
// the rule (window, ordering, decision-hit) against a stub store.
func EvaluateDisciplineDrift(
	ctx context.Context,
	store discipline.Store,
	now time.Time,
) disciplineHealth {
	if store == nil {
		return disciplineHealth{}
	}
	since := now.Add(-disciplineLookback)
	mutatingEvents, err := store.RecentMutating(ctx, since, disciplineFetchLimit)
	if err != nil || len(mutatingEvents) == 0 {
		return disciplineHealth{}
	}
	decisionCache := make(map[string][]time.Time)
	getDecisions := func(sessionID string) []time.Time {
		if ts, ok := decisionCache[sessionID]; ok {
			return ts
		}
		ts, qErr := store.RecentDecisionTimes(ctx, sessionID, since.Add(-driftWindow))
		if qErr != nil {
			ts = nil
		}
		decisionCache[sessionID] = ts
		return ts
	}
	health := disciplineHealth{}
	for _, ev := range mutatingEvents {
		decisions := getDecisions(ev.SessionID)
		if hasDecisionInWindow(ev.ObservedAt, decisions) {
			continue
		}
		health.DriftCount24h++
		if len(health.RecentDrifts) < maxDisciplineSamples {
			health.RecentDrifts = append(health.RecentDrifts, disciplineSample{
				ToolName:   ev.ToolName,
				ObservedAt: ev.ObservedAt,
				RepoName:   ev.RepoName,
			})
		}
	}
	return health
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
	if !isSafeKeyword(kw) {
		return false
	}
	boundary := filepath.Clean(repoRoot)
	// Canonicalize boundary so the prefix check still works when the OS
	// resolves the parent (e.g. macOS /var → /private/var).
	if resolved, err := filepath.EvalSymlinks(boundary); err == nil {
		boundary = resolved
	}
	if strings.Contains(kw, "/") {
		return filePathExistsInBoundary(kw, boundary)
	}
	return migrationGlobExists(kw, boundary)
}

// isSafeKeyword rejects keywords containing ".." segments or ASCII control
// bytes (incl. NUL / CR / LF) before they reach any filesystem call.
func isSafeKeyword(kw string) bool {
	if strings.Contains(kw, "..") {
		return false
	}
	for _, ch := range kw {
		if ch < 0x20 {
			return false
		}
	}
	return true
}

// filePathExistsInBoundary checks whether a slash-bearing keyword resolves to
// an existing file inside boundary. Two-layer defence: lexical prefix check
// after Join + EvalSymlinks-resolved prefix check (CWE-59 link following).
func filePathExistsInBoundary(kw, boundary string) bool {
	sep := string(filepath.Separator)
	joined := filepath.Join(boundary, kw)
	if joined != boundary && !strings.HasPrefix(joined, boundary+sep) {
		return false
	}
	resolved, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return false
	}
	return resolved == boundary || strings.HasPrefix(resolved, boundary+sep)
}

// migrationGlobExists checks the digit-only kw against migrations/<kw>*.sql.
// Defensive: rejects non-digit input even though the caller's regex already
// constrains it.
func migrationGlobExists(kw, boundary string) bool {
	for _, ch := range kw {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	sep := string(filepath.Separator)
	pattern := filepath.Join(boundary, "migrations", kw+"*.sql")
	if !strings.HasPrefix(pattern, boundary+sep) {
		return false
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return false
	}
	// Defence in depth: a user-planted migrations/ symlink to outside dir
	// would otherwise let glob results escape boundary (CWE-59 carry-over).
	for _, m := range matches {
		resolved, err := filepath.EvalSymlinks(m)
		if err != nil {
			continue
		}
		if resolved == boundary || strings.HasPrefix(resolved, boundary+sep) {
			return true
		}
	}
	return false
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

// countVaguePending scans tasks for pending/in_progress entries whose
// Description triggers validator.CheckVagueness for the task's Kind. Returns
// the total count plus the first maxVagueSampleIDs IDs (lexical UUID order as
// returned by the store query — sample, not ranked).
//
// Status filter mirrors detectCompletionDrift: both pending and in_progress
// are inspected so a vague placeholder noticed after the task has already
// been started is still surfaced.
//
// CheckVagueness already returns nil for kind=="chore" so chore tasks with
// short or marker-flavoured descriptions are correctly skipped here too.
// Tasks with an empty / unset description are NOT considered vague by this
// helper — an empty description is a separate signal handled elsewhere (and
// the validator would otherwise flag every empty-string task as "too short").
func countVaguePending(tasks []db.Task) (int, []string) {
	count := 0
	var sampleIDs []string
	for _, t := range tasks {
		if t.Status != taskStatusPending && t.Status != taskStatusInProgress {
			continue
		}
		if !t.Description.Valid || t.Description.String == "" {
			continue
		}
		warnings := validator.CheckVagueness("description", t.Description.String, t.Kind)
		if len(warnings) == 0 {
			continue
		}
		count++
		if len(sampleIDs) < maxVagueSampleIDs {
			sampleIDs = append(sampleIDs, t.ID.String())
		}
	}
	return count, sampleIDs
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
