package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/worksession"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (s *Server) registerPlanTools(ms *server.MCPServer) {
	// Description budget: confirm_plan is in the always-visible core tool set
	// (toolgroups.go). The atomicity carve-outs, response-shape contract and
	// JSON field examples moved to mcpProtocolAppendix ("Per-tool detail",
	// tools_onboarding.go); what stays here is only what changes behaviour at
	// call time — the trigger, the "use this instead" rule, and the
	// do-NOT-re-send rule, which is a correctness hazard, not commentary.
	ms.AddTool(mcp.NewTool(
		"confirm_plan",
		mcp.WithDescription(
			"CALL THIS when the user confirms a multi-phase plan ('可以','好','go','ok','開始'). "+
				"Atomically creates one GTD task per phase AND logs each decision. Use this "+
				"INSTEAD of add_task + log_decision separately. ALWAYS read the response instead "+
				"of assuming success; on a failure saying OUTCOME UNKNOWN the plan MAY already be "+
				"stored — do NOT re-send it, check list_tasks/list_decisions and retry only what "+
				"is missing. See initial_instructions (Per-tool detail) for the field shapes and "+
				"the two atomicity carve-outs.",
		),
		mcp.WithString(
			"phases",
			mcp.Description(`JSON array of tasks: {"title","description","priority"}`),
			mcp.Required(),
		),
		mcp.WithString(
			"decisions",
			mcp.Description(`JSON array: {"title","context","decision","rationale","alternatives"}`),
		),
		mcp.WithString("project_id", mcp.Description("Project UUID (optional)")),
		mcp.WithString("repo_name", mcp.Description("Repository name (optional)")),
		mcp.WithString("assignee", mcp.Description(
			"Canonical actor: claude | codex | human (or a recognised alias). A phase task with "+
				"no assignee and no value here stays pending instead of flipping to in_progress.",
		)),
	), s.handleConfirmPlan)
}

type phaseInput struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    int32  `json:"priority"`
}

type decisionInput struct {
	Title        string `json:"title"`
	Context      string `json:"context"`
	Decision     string `json:"decision"`
	Rationale    string `json:"rationale"`
	Alternatives string `json:"alternatives"`
}

func (s *Server) handleConfirmPlan(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	rawPhases := stringArg(args, "phases")
	if rawPhases == "" {
		return mcp.NewToolResultError("phases is required"), nil
	}
	var phases []phaseInput
	if err := json.Unmarshal([]byte(rawPhases), &phases); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid phases JSON: %v", err)), nil
	}
	if len(phases) == 0 {
		return mcp.NewToolResultError("phases must not be empty"), nil
	}

	var decisions []decisionInput
	if raw := stringArg(args, "decisions"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &decisions); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid decisions JSON: %v", err)), nil
		}
	}

	var projectID *uuid.UUID
	if raw := stringArg(args, "project_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return mcp.NewToolResultError(errMsgInvalidProjectIDUUID), nil
		}
		projectID = &id
	}
	repoName := stringArg(args, "repo_name")

	// P6.8: assignee is optional but, when present, MUST resolve through
	// gtd.NormalizeActor's whitelist (backend-security-design.md §2.1 — LLM
	// tool input is adversarial). Same resolveAssigneeArg helper start_work
	// and add_task/update_task already use.
	assignee, assigneeErrMsg := resolveAssigneeArg(args)
	if assigneeErrMsg != "" {
		return mcp.NewToolResultError(assigneeErrMsg), nil
	}

	// Create phase tasks + log decisions. materializePlan dispatches to a
	// real-transaction path on both shipped backends (PG/SQLite) — see its
	// doc comment for the two documented exceptions to the atomicity claim.
	createdTasks, taskIDs, loggedDecisions, err := s.materializePlan(ctx, phases, decisions, projectID, repoName)
	if err != nil {
		return mcp.NewToolResultError(planResultText(createdTasks, loggedDecisions, nil, err)), nil
	}

	// Always create an in_progress work session (D2: no bool flag).
	// Best-effort: work session failure must not block the tasks/decisions result.
	sessionID := s.createWorkSessionForPlan(ctx, repoName, projectID, phases, taskIDs, assignee)

	return mcp.NewToolResultText(planResultText(createdTasks, loggedDecisions, sessionID, nil)), nil
}

// planResultText renders confirm_plan's response text for both the success
// path (planErr == nil) and the partial-failure path (planErr != nil).
//
// P-atomicity-honesty: createPhaseTasksWithIDs / logPlanDecisions return
// whatever was successfully created BEFORE an error instead of discarding it
// (the old behavior threw the partial slices away on error, so the caller
// had no way to learn which tasks/decisions — if any — actually exist after
// a mid-loop failure; it had to re-list everything to find out). When
// planErr is non-nil but nothing was created yet (createdTasks and
// loggedDecisions both empty — e.g. the very first phase task fails, or an
// atomic backend rolled the whole transaction back), the plain error message
// is returned without the empty "already created" headers.
func planResultText(createdTasks, loggedDecisions []string, sessionID *string, planErr error) string {
	if planErr != nil && len(createdTasks) == 0 && len(loggedDecisions) == 0 {
		return fmt.Sprintf("Plan confirmation failed: %v", planErr)
	}
	var sb strings.Builder
	if planErr != nil {
		fmt.Fprintf(&sb, "Plan confirmation failed: %v\n", planErr)
		fmt.Fprintf(&sb, "Already created before the failure — tasks (%d):\n", len(createdTasks))
	} else {
		fmt.Fprintf(&sb, "Plan confirmed. Tasks created (%d):\n", len(createdTasks))
	}
	for _, t := range createdTasks {
		fmt.Fprintf(&sb, "  • %s\n", t)
	}
	if len(loggedDecisions) > 0 {
		label := "Decisions logged"
		if planErr != nil {
			label = "Decisions logged before the failure"
		}
		fmt.Fprintf(&sb, "\n%s (%d):\n", label, len(loggedDecisions))
		for _, d := range loggedDecisions {
			fmt.Fprintf(&sb, "  • %s\n", d)
		}
	}
	if sessionID != nil {
		fmt.Fprintf(&sb, "\nWork session started: %s\n", *sessionID)
	}
	return sb.String()
}

// materializePlan creates confirm_plan's phase tasks + decisions, dispatching
// to a real-transaction path on whichever backend is wired (mirrors the
// s.pool != nil / s.sqliteGTD != nil / else pattern in acceptProposal,
// tools_proposal.go:337-345). Returns (created task titles, created task
// UUIDs, logged decision titles, error).
//
// On the PG and SQLite paths a mid-loop failure returns nil/nil/nil — the
// whole transaction rolled back, so there is nothing "already created" to
// report; the error text itself says so. On the sequential fallback path
// (neither backend wired — not a real deployment) a mid-loop failure returns
// whatever was actually written, matching materializePlanSequential's
// non-transactional semantics.
func (s *Server) materializePlan(
	ctx context.Context, phases []phaseInput, decisions []decisionInput, projectID *uuid.UUID, repoName string,
) ([]string, []uuid.UUID, []string, error) {
	if s.pool != nil {
		return s.materializePlanPg(ctx, phases, decisions, projectID, repoName)
	}
	if s.sqliteGTD != nil {
		return s.materializePlanSQLite(ctx, phases, decisions, projectID, repoName)
	}
	return s.materializePlanSequential(ctx, phases, decisions, projectID, repoName)
}

// materializePlanPg runs the phase-task-creation + decision-logging loops
// inside a single pgx.Tx so a mid-loop failure rolls back everything written
// so far in this call — the Postgres half of confirm_plan's atomicity claim.
// Mirrors acceptProposalPg's tx shape (tools_proposal.go:350-382).
func (s *Server) materializePlanPg(
	ctx context.Context, phases []phaseInput, decisions []decisionInput, projectID *uuid.UUID, repoName string,
) ([]string, []uuid.UUID, []string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("beginning plan transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // safe: no-op if already committed

	gtdTx := s.pgGTD.WithTx(tx)
	var created []string
	var ids []uuid.UUID
	for _, phase := range phases {
		if phase.Title == "" {
			continue
		}
		priority := phase.Priority
		if priority == 0 {
			priority = 2
		}
		task, terr := gtdTx.CreateTask(ctx, gtd.CreateTaskParams{
			ProjectID:   projectID,
			Title:       phase.Title,
			Description: phase.Description,
			Priority:    priority,
		})
		if terr != nil {
			return nil, nil, nil, fmt.Errorf("creating task %q (transaction rolled back, no changes made): %w", phase.Title, terr)
		}
		created = append(created, task.Title)
		ids = append(ids, task.ID)
	}

	decTx := s.pgDecision.WithTx(tx)
	var logged []string
	for _, d := range decisions {
		if d.Title == "" || d.Decision == "" {
			continue
		}
		dec, derr := decTx.Log(ctx, decision.LogParams{
			ProjectID:    projectID,
			RepoName:     repoName,
			Title:        d.Title,
			Context:      d.Context,
			Decision:     d.Decision,
			Rationale:    d.Rationale,
			Alternatives: d.Alternatives,
			Source:       decision.SourceManual,
		})
		if derr != nil {
			return nil, nil, nil, fmt.Errorf("logging decision %q (transaction rolled back, no changes made): %w", d.Title, derr)
		}
		logged = append(logged, dec.Title)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, nil, fmt.Errorf(
			"committing plan transaction (OUTCOME UNKNOWN: the commit may or may not have been "+
				"applied by the database; verify with list_tasks/list_decisions before retrying): %w", err,
		)
	}
	return created, ids, logged, nil
}

// materializePlanSQLite is the SQLite counterpart of materializePlanPg. It
// opens one *sql.Tx via s.sqliteGTD.DB().BeginTx and passes it to both
// CreateTaskTx and DecisionStore.LogTx so tasks and decisions commit or roll
// back together — SQLite is single-writer, so this MUST be one tx shared
// across both stores, not two separate BeginTx calls (that would deadlock on
// the single pooled connection; see gtd.go's ResolveGuardBlocked doc
// comment for the same constraint on a different call path).
func (s *Server) materializePlanSQLite(
	ctx context.Context, phases []phaseInput, decisions []decisionInput, projectID *uuid.UUID, repoName string,
) ([]string, []uuid.UUID, []string, error) {
	tx, err := s.sqliteGTD.DB().BeginTx(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("beginning plan transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit

	var created []string
	var ids []uuid.UUID
	for _, phase := range phases {
		if phase.Title == "" {
			continue
		}
		priority := phase.Priority
		if priority == 0 {
			priority = 2
		}
		taskID, terr := s.sqliteGTD.CreateTaskTx(ctx, tx, gtd.CreateTaskParams{
			ProjectID:   projectID,
			Title:       phase.Title,
			Description: phase.Description,
			Priority:    priority,
		})
		if terr != nil {
			return nil, nil, nil, fmt.Errorf("creating task %q (transaction rolled back, no changes made): %w", phase.Title, terr)
		}
		created = append(created, phase.Title)
		ids = append(ids, taskID)
	}

	if len(decisions) > 0 && s.sqliteDecision == nil {
		return nil, nil, nil, fmt.Errorf("logging decisions (transaction rolled back, no changes made): sqlite decision store not wired")
	}
	var logged []string
	for _, d := range decisions {
		if d.Title == "" || d.Decision == "" {
			continue
		}
		if _, derr := s.sqliteDecision.LogTx(ctx, tx, decision.LogParams{
			ProjectID:    projectID,
			RepoName:     repoName,
			Title:        d.Title,
			Context:      d.Context,
			Decision:     d.Decision,
			Rationale:    d.Rationale,
			Alternatives: d.Alternatives,
			Source:       decision.SourceManual,
		}); derr != nil {
			return nil, nil, nil, fmt.Errorf("logging decision %q (transaction rolled back, no changes made): %w", d.Title, derr)
		}
		logged = append(logged, d.Title)
	}

	if err := tx.Commit(); err != nil {
		return nil, nil, nil, fmt.Errorf(
			"committing plan transaction (OUTCOME UNKNOWN: the commit may or may not have been "+
				"applied by the database; verify with list_tasks/list_decisions before retrying): %w", err,
		)
	}
	return created, ids, logged, nil
}

// materializePlanSequential is the non-atomic fallback used only when the
// server is wired with neither a Postgres pool nor a SQLite GTD store (not a
// real deployment configuration — storage.NewServerStores only ever produces
// one or the other). Delegates to the pre-transaction createPhaseTasksWithIDs
// / logPlanDecisions pair, which preserve partial results on a mid-loop
// failure instead of discarding them.
func (s *Server) materializePlanSequential(
	ctx context.Context, phases []phaseInput, decisions []decisionInput, projectID *uuid.UUID, repoName string,
) ([]string, []uuid.UUID, []string, error) {
	created, ids, err := s.createPhaseTasksWithIDs(ctx, phases, projectID)
	if err != nil {
		return created, ids, nil, err
	}
	logged, err := s.logPlanDecisions(ctx, decisions, projectID, repoName)
	if err != nil {
		return created, ids, logged, err
	}
	return created, ids, logged, nil
}

// createPhaseTasksWithIDs creates tasks for each phase and returns both the
// title list (for the text response) and the UUID list (for work session
// linking). On a mid-loop failure it returns whatever was created BEFORE the
// failing phase (not nil) — see planResultText's doc comment. Used only by
// materializePlanSequential (the non-atomic fallback path); the PG/SQLite
// paths have their own tx-scoped loops above.
func (s *Server) createPhaseTasksWithIDs(ctx context.Context, phases []phaseInput, projectID *uuid.UUID) ([]string, []uuid.UUID, error) {
	var created []string
	var ids []uuid.UUID
	for _, phase := range phases {
		if phase.Title == "" {
			continue
		}
		priority := phase.Priority
		if priority == 0 {
			priority = 2
		}
		task, err := s.gtd.CreateTask(ctx, gtd.CreateTaskParams{
			ProjectID:   projectID,
			Title:       phase.Title,
			Description: phase.Description,
			Priority:    priority,
		})
		if err != nil {
			return created, ids, fmt.Errorf("creating task %q (%d already created): %w", phase.Title, len(created), err)
		}
		created = append(created, task.Title)
		ids = append(ids, task.ID)
	}
	return created, ids, nil
}

// logPlanDecisions returns whatever was logged BEFORE a mid-loop failure
// (not nil) — see planResultText's doc comment. Used only by
// materializePlanSequential (the non-atomic fallback path).
func (s *Server) logPlanDecisions(ctx context.Context, decisions []decisionInput, projectID *uuid.UUID, repoName string) ([]string, error) {
	var logged []string
	for _, d := range decisions {
		if d.Title == "" || d.Decision == "" {
			continue
		}
		dec, err := s.decision.Log(ctx, decision.LogParams{
			ProjectID:    projectID,
			RepoName:     repoName,
			Title:        d.Title,
			Context:      d.Context,
			Decision:     d.Decision,
			Rationale:    d.Rationale,
			Alternatives: d.Alternatives,
			Source:       decision.SourceManual,
		})
		if err != nil {
			return logged, fmt.Errorf("logging decision %q (%d already logged): %w", d.Title, len(logged), err)
		}
		logged = append(logged, dec.Title)
	}
	return logged, nil
}

// createWorkSessionForPlan creates an in_progress work_session linking all
// phase tasks. It is best-effort: any error is logged at Warn level and
// returns nil so the caller can still surface the tasks/decisions result.
//
// SECURITY: workspace_id is taken from the store (env-configured), never from
// tool input. task_ids verification (all tasks belong to same workspace) is
// implicitly enforced by the gtd.CreateTask above — all tasks were just
// created scoped to s.workspaceID.
func (s *Server) createWorkSessionForPlan(
	ctx context.Context,
	repoName string,
	projectID *uuid.UUID,
	phases []phaseInput,
	taskIDs []uuid.UUID,
	assignee string,
) *string {
	if s.workSession == nil {
		return nil
	}
	if repoName == "" {
		// No repo context — skip silently (non-repo sessions require start_work).
		return nil
	}

	// Build a title from the first phase title.
	title := "Confirm plan"
	if len(phases) > 0 && phases[0].Title != "" {
		title = phases[0].Title
	}

	// Build goal from phase titles.
	var goalParts []string
	for _, ph := range phases {
		if ph.Title != "" {
			goalParts = append(goalParts, ph.Title)
		}
	}
	goal := strings.Join(goalParts, " → ")
	if goal == "" {
		goal = title
	}

	wsID := s.workspaceUUIDVal()
	sess, err := s.workSession.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID,
		RepoName:    repoName,
		ProjectID:   projectID,
		Title:       title,
		Goal:        goal,
		Source:      "confirm_plan",
		TaskIDs:     taskIDs,
		Assignee:    assignee,
	})
	if err != nil {
		// ErrAlreadyActive: a session is already in_progress for this repo.
		// Log a warning; do not block confirm_plan.
		slog.Warn(
			"confirm_plan: could not create work session",
			"repo_name", repoName,
			"err", err,
		)
		return nil
	}

	id := sess.ID.String()
	return &id
}
