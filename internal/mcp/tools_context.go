package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/Wayne997035/wayneblacktea/internal/snapshot"
	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// primaryProjectSlug is the slug used for the primary project's arch snapshot.
const primaryProjectSlug = "wayneblacktea"

func (s *Server) registerContextTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool(
		"get_today_context",
		mcp.WithDescription(
			"CALL AT SESSION START. Returns active goals, projects, weekly progress, pending session handoff, "+
				"and pulled_forward (up to 5 important tasks not yet due, surfaced early so they aren't missed). "+
				"This is a compact index, not full records: long text is clipped with a trailing … and "+
				"next_actions is capped (next_actions_total reports the real count). To read anything clipped, "+
				"call get_task(task_id) for a task's full description/context, get_project(name) or list_goals "+
				"for full project/goal text, and get_project_arch(\"wayneblacktea\") for the architecture snapshot.",
		),
	), s.handleGetTodayContext)

	ms.AddTool(mcp.NewTool(
		"list_active_repos",
		mcp.WithDescription("Returns all active repositories in the workspace."),
	), s.handleListActiveRepos)

	ms.AddTool(mcp.NewTool(
		"sync_repo",
		mcp.WithDescription("Creates or updates a repository entry with current state."),
		mcp.WithString("name", mcp.Description("Repository name (unique key)"), mcp.Required()),
		mcp.WithString("path", mcp.Description("Local filesystem path")),
		mcp.WithString("description", mcp.Description("Short description")),
		mcp.WithString("language", mcp.Description("Primary programming language")),
		mcp.WithString("current_branch", mcp.Description("Current git branch")),
		mcp.WithString("next_planned_step", mcp.Description("What to work on next")),
	), s.handleSyncRepo)
}

// ---------------------------------------------------------------------------
// get_today_context payload budget
// ---------------------------------------------------------------------------

// Rune caps for the free-text fields get_today_context returns. Every client
// calls this tool once per session, so its payload is pure prompt overhead:
// it has to stay a compact index rather than a data dump. Nothing is lost —
// each clipped field has a named follow-up tool in the tool description above.
//
// All caps live in this one block so the budget can be retuned in a single
// place; TestHandleGetTodayContext_PayloadBudget asserts the resulting size.
const (
	// pulledForwardDescMaxRunes is a user-locked floor: pulled-forward tasks
	// are surfaced precisely so their description is actionable without a
	// second round trip. Do not lower it to buy budget — tighten the caps
	// below first.
	pulledForwardDescMaxRunes = 150
	goalDescMaxRunes          = 200
	projectDescMaxRunes       = 150
	handoffIntentMaxRunes     = 300
	handoffSummaryMaxRunes    = 350
	nextActionFieldMaxRunes   = 120
	sprintSummaryMaxRunes     = 250
	// maxHandoffNextActions caps how many next_actions ride along in the
	// session-start payload. The real length is always reported as
	// next_actions_total so the model knows something was withheld.
	maxHandoffNextActions = 6
)

// clipMarker is the single-rune (U+2026) marker appended to clipped text. One
// rune, so a clipped field costs cap+1 runes and the marker can never be split
// across a truncation boundary.
const clipMarker = "…"

// clipRunes returns s capped at maxRunes, appending clipMarker if — and only
// if — s was actually shortened. Rune-based via truncateRunes
// (middleware_classify.go), so multi-byte text (CJK) is never cut mid-rune
// and the result is always valid UTF-8.
func clipRunes(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	return truncateRunes(s, maxRunes) + clipMarker
}

// textValue unwraps a nullable pgtype.Text; SQL NULL becomes "".
func textValue(t pgtype.Text) string {
	if !t.Valid {
		return ""
	}
	return t.String
}

// clipText is clipRunes over a nullable pgtype.Text.
func clipText(t pgtype.Text, maxRunes int) string {
	return clipRunes(textValue(t), maxRunes)
}

// ---------------------------------------------------------------------------
// slim wire types
// ---------------------------------------------------------------------------

type weeklyProgress struct {
	Completed int64 `json:"completed"`
	Total     int64 `json:"total"`
}

type latestStatusSnapshot struct {
	GeneratedAt    string `json:"generated_at"`
	SprintSummary  string `json:"sprint_summary"`
	SotaCatchupPct int    `json:"sota_catchup_pct"`
}

// goalSummary is the slim projection of db.Goal for get_today_context.
// Timestamps other than due_date, the workspace id and the full description
// are dropped — list_goals returns the complete record.
type goalSummary struct {
	ID          uuid.UUID          `json:"id"`
	Title       string             `json:"title"`
	Status      string             `json:"status"`
	Area        string             `json:"area,omitempty"`
	DueDate     pgtype.Timestamptz `json:"due_date"`
	Description string             `json:"description,omitempty"`
}

// projectSummary is the slim projection of db.Project for get_today_context.
// get_project(name) returns the complete record.
type projectSummary struct {
	ID          uuid.UUID   `json:"id"`
	Name        string      `json:"name"`
	Title       string      `json:"title"`
	Status      string      `json:"status"`
	Area        string      `json:"area,omitempty"`
	Priority    int32       `json:"priority"`
	GoalID      pgtype.UUID `json:"goal_id"`
	RepoName    string      `json:"repo_name,omitempty"`
	Description string      `json:"description,omitempty"`
}

// pulledForwardTask is the slim projection of db.Task for the pulled_forward
// field. The task's `context` column is deliberately not exposed at all (it is
// the single largest text column on the row); get_task(task_id) returns it.
type pulledForwardTask struct {
	ID          uuid.UUID          `json:"id"`
	ProjectID   pgtype.UUID        `json:"project_id"`
	Title       string             `json:"title"`
	Status      string             `json:"status"`
	Priority    int32              `json:"priority"`
	Importance  pgtype.Int2        `json:"importance"`
	DueDate     pgtype.Timestamptz `json:"due_date"`
	Kind        string             `json:"kind"`
	Description string             `json:"description,omitempty"`
}

// nextActionSummary is the clipped projection of session.NextAction used
// inside pendingHandoffSummary.
type nextActionSummary struct {
	Step      int    `json:"step"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Command   string `json:"command,omitempty"`
	Expected  string `json:"expected,omitempty"`
	RefTaskID string `json:"ref_task_id,omitempty"`
}

// pendingHandoffSummary is the clipped, session-start-sized view of a session
// handoff.
//
// It is intentionally a SEPARATE type from pendingHandoffView rather than a
// slimming of it: set_session_handoff and mark_next_action_done
// (tools_session.go) echo the caller's own text straight back through
// buildPendingHandoffView, and clipping there would silently truncate the
// text the user just wrote. Only get_today_context uses this type.
type pendingHandoffSummary struct {
	ID             uuid.UUID          `json:"id"`
	RepoName       string             `json:"repo_name,omitempty"`
	Intent         string             `json:"intent"`
	ContextSummary string             `json:"context_summary,omitempty"`
	CreatedAt      pgtype.Timestamptz `json:"created_at"`
	// NextActions carries at most maxHandoffNextActions entries and is always
	// a non-nil slice — empty array, never null.
	NextActions      []nextActionSummary `json:"next_actions"`
	NextActionsTotal int                 `json:"next_actions_total"`
}

// pendingHandoffView is the JSON-friendly view of a session handoff returned
// in get_today_context. next_actions is decoded from raw JSON bytes to a typed
// slice so callers see a proper array (not base64-encoded bytes).
type pendingHandoffView struct {
	ID             uuid.UUID            `json:"id"`
	ProjectID      pgtype.UUID          `json:"project_id"`
	RepoName       pgtype.Text          `json:"repo_name"`
	Intent         string               `json:"intent"`
	ContextSummary pgtype.Text          `json:"context_summary"`
	ResolvedAt     pgtype.Timestamptz   `json:"resolved_at"`
	CreatedAt      pgtype.Timestamptz   `json:"created_at"`
	WorkspaceID    pgtype.UUID          `json:"workspace_id"`
	NextActions    []session.NextAction `json:"next_actions"`
}

// buildPendingHandoffView converts a db.SessionHandoff into a pendingHandoffView,
// decoding the raw next_actions bytes into a typed slice.
//
// Do NOT add clipping here: tools_session.go returns this view verbatim from
// set_session_handoff / mark_next_action_done, where the response is an
// echo-back of what the caller just wrote and must stay byte-exact.
func buildPendingHandoffView(h *db.SessionHandoff) *pendingHandoffView {
	if h == nil {
		return nil
	}
	v := &pendingHandoffView{
		ID:             h.ID,
		ProjectID:      h.ProjectID,
		RepoName:       h.RepoName,
		Intent:         h.Intent,
		ContextSummary: h.ContextSummary,
		ResolvedAt:     h.ResolvedAt,
		CreatedAt:      h.CreatedAt,
		WorkspaceID:    h.WorkspaceID,
		NextActions:    []session.NextAction{},
	}
	if len(h.NextActions) > 0 && string(h.NextActions) != "[]" {
		var actions []session.NextAction
		if err := json.Unmarshal(h.NextActions, &actions); err != nil {
			slog.Warn("buildPendingHandoffView: corrupt next_actions column", "handoff_id", h.ID, "err", err)
		} else {
			v.NextActions = actions
		}
	}
	return v
}

// buildPendingHandoffSummary converts a db.SessionHandoff into the clipped
// session-start view. It reuses buildPendingHandoffView for the next_actions
// decode so there is exactly one place that handles a corrupt column.
func buildPendingHandoffSummary(h *db.SessionHandoff) *pendingHandoffSummary {
	if h == nil {
		return nil
	}
	full := buildPendingHandoffView(h)
	v := &pendingHandoffSummary{
		ID:               h.ID,
		RepoName:         textValue(h.RepoName),
		Intent:           clipRunes(h.Intent, handoffIntentMaxRunes),
		ContextSummary:   clipText(h.ContextSummary, handoffSummaryMaxRunes),
		CreatedAt:        h.CreatedAt,
		NextActions:      make([]nextActionSummary, 0, maxHandoffNextActions),
		NextActionsTotal: len(full.NextActions),
	}
	for i := range full.NextActions {
		if i >= maxHandoffNextActions {
			break
		}
		a := &full.NextActions[i]
		v.NextActions = append(v.NextActions, nextActionSummary{
			Step:      a.Step,
			Title:     clipRunes(a.Title, nextActionFieldMaxRunes),
			Status:    string(a.Status),
			Command:   clipRunes(a.Command, nextActionFieldMaxRunes),
			Expected:  clipRunes(a.Expected, nextActionFieldMaxRunes),
			RefTaskID: clipPtr(a.RefTaskID, nextActionFieldMaxRunes),
		})
	}
	return v
}

// clipPtr is clipRunes over an optional string pointer; nil becomes "".
func clipPtr(p *string, maxRunes int) string {
	if p == nil {
		return ""
	}
	return clipRunes(*p, maxRunes)
}

func slimGoals(goals []db.Goal) []goalSummary {
	out := make([]goalSummary, 0, len(goals))
	for i := range goals {
		g := &goals[i]
		out = append(out, goalSummary{
			ID:          g.ID,
			Title:       g.Title,
			Status:      g.Status,
			Area:        textValue(g.Area),
			DueDate:     g.DueDate,
			Description: clipText(g.Description, goalDescMaxRunes),
		})
	}
	return out
}

func slimProjects(projects []db.Project) []projectSummary {
	out := make([]projectSummary, 0, len(projects))
	for i := range projects {
		p := &projects[i]
		out = append(out, projectSummary{
			ID:          p.ID,
			Name:        p.Name,
			Title:       p.Title,
			Status:      p.Status,
			Area:        p.Area,
			Priority:    p.Priority,
			GoalID:      p.GoalID,
			RepoName:    textValue(p.RepoName),
			Description: clipText(p.Description, projectDescMaxRunes),
		})
	}
	return out
}

func slimPulledForward(tasks []db.Task) []pulledForwardTask {
	out := make([]pulledForwardTask, 0, len(tasks))
	for i := range tasks {
		t := &tasks[i]
		out = append(out, pulledForwardTask{
			ID:          t.ID,
			ProjectID:   t.ProjectID,
			Title:       t.Title,
			Status:      t.Status,
			Priority:    t.Priority,
			Importance:  t.Importance,
			DueDate:     t.DueDate,
			Kind:        t.Kind,
			Description: clipText(t.Description, pulledForwardDescMaxRunes),
		})
	}
	return out
}

type todayContext struct {
	Goals                []goalSummary          `json:"goals"`
	Projects             []projectSummary       `json:"projects"`
	WeeklyProgress       weeklyProgress         `json:"weekly_progress"`
	PendingHandoff       *pendingHandoffSummary `json:"pending_handoff"`
	LatestStatusSnapshot *latestStatusSnapshot  `json:"latest_status_snapshot,omitempty"`
	// PulledForward holds up to gtd.PullForwardCap important (importance=1)
	// tasks not yet due, auto-surfaced into today's context (2026-07-19 user
	// decision: automatic, data-only, no manual pull tool). Always a non-nil
	// slice — empty array, never null, so clients don't need a presence check.
	PulledForward []pulledForwardTask `json:"pulled_forward"`
}

// ---------------------------------------------------------------------------
// parallel fetch
// ---------------------------------------------------------------------------

// todayContextRaw collects the results of the parallel store lookups behind
// get_today_context. Every goroutine writes to its own distinct set of fields
// (distinct memory locations, so no mutex is required) and wg.Wait()
// establishes the happens-before edge that publishes those writes to the
// reader.
type todayContextRaw struct {
	goals       []db.Goal
	goalsErr    error
	projects    []db.Project
	projectsErr error
	completed   int64
	total       int64
	progressErr error
	handoff     *db.SessionHandoff
	handoffErr  error
	pulled      []db.Task
	pulledErr   error
	latestSnap  *latestStatusSnapshot
}

// fetchTodayContext runs the six independent store lookups behind
// get_today_context concurrently, turning ~6 serial round trips into one.
//
// sync.WaitGroup, deliberately NOT errgroup:
//   - Error reporting must stay positional (goals → projects → progress →
//     handoff → pull-forward) so a multi-failure response is deterministic.
//     errgroup.Wait returns whichever goroutine happened to fail first, which
//     would make that contract depend on the scheduler.
//   - errgroup.WithContext cancels the sibling goroutines on the first error,
//     which would turn the best-effort snapshot lookup into a spurious
//     context.Canceled slog.Warn — a behaviour regression, not a speed-up.
//
// ctx is passed through unmodified: these are read-only lookups performed on
// behalf of the caller, so the caller's deadline and cancellation are exactly
// the right ones (no detached background writes here).
func (s *Server) fetchTodayContext(ctx context.Context) *todayContextRaw {
	raw := &todayContextRaw{}

	var wg sync.WaitGroup
	wg.Add(6)

	go func() {
		defer wg.Done()
		raw.goals, raw.goalsErr = s.gtd.ActiveGoals(ctx)
	}()

	go func() {
		defer wg.Done()
		raw.projects, raw.projectsErr = s.gtd.ListActiveProjects(ctx)
	}()

	// WeeklyProgress issues two queries internally; they stay inside this one
	// goroutine so the store keeps owning their ordering.
	go func() {
		defer wg.Done()
		raw.completed, raw.total, raw.progressErr = s.gtd.WeeklyProgress(ctx)
	}()

	go func() {
		defer wg.Done()
		h, err := s.session.LatestHandoff(ctx)
		if err != nil && !errors.Is(err, session.ErrNotFound) {
			raw.handoffErr = err
			return
		}
		raw.handoff = h
	}()

	go func() {
		defer wg.Done()
		raw.pulled, raw.pulledErr = s.gtd.PullForwardTasks(ctx, time.Now())
	}()

	go func() {
		defer wg.Done()
		raw.latestSnap = s.fetchLatestStatusSnapshot(ctx)
	}()

	wg.Wait()
	return raw
}

// fetchLatestStatusSnapshot is the best-effort latest-status-snapshot lookup
// (age < 24 h) for the primary project. An unwired store, a missing row or a
// stale row all yield nil; any other failure is logged at warn level and the
// field is simply omitted, so session start never fails on it. Full text is
// available via generate_project_status.
func (s *Server) fetchLatestStatusSnapshot(ctx context.Context) *latestStatusSnapshot {
	if s.snapshotStore == nil {
		return nil
	}
	snap, err := s.snapshotStore.LatestFresh(ctx, primaryProjectSlug, 24*time.Hour)
	if err != nil {
		if !snapshot.IsNotFound(err) {
			slog.Warn("get_today_context: loading latest snapshot", "err", err)
		}
		return nil
	}
	return &latestStatusSnapshot{
		GeneratedAt:    snap.GeneratedAt.UTC().Format(time.RFC3339),
		SprintSummary:  clipRunes(snap.SprintSummary, sprintSummaryMaxRunes),
		SotaCatchupPct: snap.SotaCatchupPct,
	}
}

// toolError returns the first store failure in the documented priority order
// (goals → projects → progress → handoff → pull-forward), or nil when every
// required lookup succeeded. Fixed order = deterministic message even when
// several lookups fail in the same call.
func (r *todayContextRaw) toolError() *mcp.CallToolResult {
	switch {
	case r.goalsErr != nil:
		return mcp.NewToolResultError(fmt.Sprintf("loading goals: %v", r.goalsErr))
	case r.projectsErr != nil:
		return mcp.NewToolResultError(fmt.Sprintf("loading projects: %v", r.projectsErr))
	case r.progressErr != nil:
		return mcp.NewToolResultError(fmt.Sprintf("loading progress: %v", r.progressErr))
	case r.handoffErr != nil:
		return mcp.NewToolResultError(fmt.Sprintf("loading handoff: %v", r.handoffErr))
	case r.pulledErr != nil:
		return mcp.NewToolResultError(fmt.Sprintf("loading pull-forward tasks: %v", r.pulledErr))
	default:
		return nil
	}
}

// toContext projects the raw store results into the slim wire payload.
func (r *todayContextRaw) toContext() todayContext {
	return todayContext{
		Goals:                slimGoals(r.goals),
		Projects:             slimProjects(r.projects),
		WeeklyProgress:       weeklyProgress{Completed: r.completed, Total: r.total},
		PendingHandoff:       buildPendingHandoffSummary(r.handoff),
		LatestStatusSnapshot: r.latestSnap,
		PulledForward:        slimPulledForward(r.pulled),
	}
}

// compactJSONText marshals v as compact JSON and returns it as a tool result,
// mirroring jsonText's error convention.
//
// Deliberately NOT jsonText: that shared helper pretty-prints with two-space
// indentation, which is worth the bytes for tools a human reads on demand but
// costs ~17 % of this payload in pure whitespace (1050 of 6350 runes at the
// caps above). get_today_context is injected into every session's prompt
// unconditionally, so that whitespace is the largest single saving available
// here and it costs no information whatsoever — the JSON is byte-for-byte
// equivalent once parsed. jsonText itself is untouched and still serves every
// other tool, including the two in tools_session.go.
func compactJSONText(v any) (*mcp.CallToolResult, error) {
	out, err := json.Marshal(v)
	if err != nil {
		return mcp.NewToolResultError("marshaling response"), nil
	}
	return mcp.NewToolResultText(string(out)), nil
}

func (s *Server) handleGetTodayContext(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	raw := s.fetchTodayContext(ctx)
	if errResult := raw.toolError(); errResult != nil {
		return errResult, nil
	}
	return compactJSONText(raw.toContext())
}

func (s *Server) handleListActiveRepos(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repos, err := s.workspace.ActiveRepos(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("loading repos: %v", err)), nil
	}
	return jsonText(repos)
}

func (s *Server) handleSyncRepo(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	name := stringArg(args, "name")
	if name == "" {
		return mcp.NewToolResultError("name is required"), nil
	}

	repo, err := s.workspace.UpsertRepo(ctx, workspace.UpsertRepoParams{
		Name:            name,
		Path:            stringArg(args, "path"),
		Description:     stringArg(args, "description"),
		Language:        stringArg(args, "language"),
		CurrentBranch:   stringArg(args, "current_branch"),
		NextPlannedStep: stringArg(args, "next_planned_step"),
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("syncing repo: %v", err)), nil
	}
	return jsonText(repo)
}
