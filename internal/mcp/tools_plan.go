package mcp

import (
	"context"
	"encoding/json"
	"errors"
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

// errWorkSessionNotAttempted is createWorkSessionForPlan's sentinel for its
// two legitimate skip cases — no work-session store wired, no repo_name —
// as opposed to an attempted-and-failed worksession.Store.Create call
// ([F0930-16]). A (*string, error) function returning (nil, nil) for "not
// applicable" is ambiguous to callers (golangci-lint's nilnil rule: nothing
// distinguishes it from "succeeded with a nil value"); a named sentinel
// makes the three-way outcome (skip / attempted-and-failed / succeeded)
// explicit in the type instead of relying on callers remembering the
// convention. handleConfirmPlan errors.Is-checks for this sentinel and
// treats it exactly like the old (nil, nil) — silent, no response-text line
// — so the two legitimate-skip tests' behavior is unchanged.
var errWorkSessionNotAttempted = errors.New("mcp: work session creation not attempted")

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
		return inputErrorResult("invalid phases JSON", err), nil
	}
	if len(phases) == 0 {
		return mcp.NewToolResultError("phases must not be empty"), nil
	}

	var decisions []decisionInput
	if raw := stringArg(args, "decisions"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &decisions); err != nil {
			return inputErrorResult("invalid decisions JSON", err), nil
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
	// [F0925-29] Checked before the transaction: the name reaches both the
	// logged decisions and the plan's work session.
	if errResult := repoNameArgError(repoName); errResult != nil {
		return errResult, nil
	}

	// P6.8: assignee is optional but, when present, MUST resolve through
	// gtd.NormalizeActor's whitelist (LLM tool input is adversarial). Same
	// resolveAssigneeArg helper start_work
	// and add_task/update_task already use.
	assignee, assigneeErrMsg := resolveAssigneeArg(args)
	if assigneeErrMsg != "" {
		return mcp.NewToolResultError(assigneeErrMsg), nil
	}

	// Create phase tasks + log decisions. materializePlan dispatches to a
	// real-transaction path on both shipped backends (PG/SQLite) — see its
	// doc comment for the two documented exceptions to the atomicity claim.
	createdTasks, taskIDs, loggedDecisions, decisionIDs, err := s.materializePlan(ctx, phases, decisions, projectID, repoName)
	if err != nil {
		// U14: the failure text must NOT carry err — materializePlan wraps
		// store and transaction errors, so %v here handed the caller pgx
		// wire messages naming tables and constraints. The partial-progress
		// listing below it is the part that mattered to the caller and is
		// kept; the error itself goes to the log.
		//
		// F0911-02: narrowed, not removed. withTagNoiseDetail (tool_errors.go)
		// appends the chain's own message when it errors.Is
		// sanitize.ErrTagNoise — this package's own bounded validation text
		// (field name + bounded excerpt, clipped in tool_errors.go — [F175-03]), never driver output — so the caller
		// sees WHICH decision field was rejected instead of guessing;
		// confirm_plan has zero front-gate calls, so this is the only place
		// that can tell it. Every other error class passes through unchanged.
		logToolError("confirming plan", err)
		text := withTagNoiseDetail(planResultText(createdTasks, taskIDs, loggedDecisions, decisionIDs, nil, true), err)
		return mcp.NewToolResultError(text), nil
	}

	// Always create an in_progress work session (D2: no bool flag).
	// Best-effort for legitimate skips (no store wired / no repo_name); an
	// ATTEMPTED-and-failed Create (e.g. ErrAlreadyActive) is surfaced in the
	// response text instead of being silently indistinguishable from those
	// two skips — [F0930-16].
	sessionID, sessionErr := s.createWorkSessionForPlan(ctx, repoName, projectID, phases, taskIDs, assignee)

	text := planResultText(createdTasks, taskIDs, loggedDecisions, decisionIDs, sessionID, false)
	if sessionErr != nil && !errors.Is(sessionErr, errWorkSessionNotAttempted) {
		// errWorkSessionNotAttempted: one of the two legitimate skips (no
		// store wired / no repo_name) — stays silent, same as the old
		// (nil, nil) contract. Every other non-nil sessionErr is an
		// attempted-and-failed Create and IS surfaced below.
		//
		// [F0930-16] Purely informational — no retry suggestion. Retrying
		// confirm_plan here would attempt to re-create the phase tasks it
		// already created above (same double-submission hazard the
		// OUTCOME UNKNOWN text a few lines up already goes out of its way to
		// avoid for a different failure class). The reason text is
		// sanitized (U14): sessionErr may wrap a store/pgx error, never
		// surfaced verbatim to the caller.
		text += "\nWork session not started (" + sanitizeWorkSessionErrorReason(sessionErr) +
			"); phase tasks remain pending/unassigned.\n"
	}
	return mcp.NewToolResultText(text), nil
}

// planFailedHeadline is the client-facing text for a confirm_plan failure.
//
// It carries no error detail by design — U14. materializePlan wraps store and
// transaction errors, so the old "Plan confirmation failed: %v" handed an LLM
// (and therefore whoever reads its context) pgx wire messages naming tables
// and constraints. What the caller can actually act on is the partial-progress
// listing that follows this line; the error itself goes to the server log via
// logToolError.
const planFailedHeadline = "Plan confirmation failed."

// planErrorTitleMaxRunes caps a caller-supplied decision title at 80 runes
// wherever it is folded into a "logging decision %q" error wrap
// (F0911-07) — same value and purpose as truncateForFinishWorkLog
// (tools_worksession.go:1337-1346). The %w chain that follows the title in
// each of those wraps is NOT capped by this constant: it is this package's
// own bounded ErrTagNoise text (sanitize/tagnoise.go), and capping the
// title is what keeps an oversized title from pushing that diagnostic out
// of a length-limited response — the defect a prior round of this spec
// caught (an upper bound on the WHOLE message truncates the field name and
// excerpt, not the harmless title).
const planErrorTitleMaxRunes = 80

// planResultText renders confirm_plan's response text for both the success
// path (failed == false) and the partial-failure path (failed == true).
//
// P-atomicity-honesty: createPhaseTasksWithIDs / logPlanDecisions return
// whatever was successfully created BEFORE an error instead of discarding it
// (the old behavior threw the partial slices away on error, so the caller
// had no way to learn which tasks/decisions — if any — actually exist after
// a mid-loop failure; it had to re-list everything to find out). When
// failed is true but nothing was created yet (createdTasks and
// loggedDecisions both empty — e.g. the very first phase task fails, or an
// atomic backend rolled the whole transaction back), the plain error message
// is returned without the empty "already created" headers.
//
// taskIDs and decisionIDs are parallel-indexed to createdTasks/loggedDecisions
// by construction — every append to a title slice in materializePlan's three
// backend variants is paired with an append to the matching ID slice on the
// same loop iteration — [F0930-15]. Each bullet line gains a trailing
// "(id: <uuid>)" so a caller (in particular the mcpInstructions rule
// requiring update_task(task_id, status="in_progress") for every task
// worked) has the id without a separate list_tasks/list_decisions round
// trip. The existing "  • %s\n" prefix and title text are unchanged —
// additive suffix only, so every pre-existing strings.Contains assertion on
// those substrings still holds.
func planResultText(
	createdTasks []string, taskIDs []uuid.UUID,
	loggedDecisions []string, decisionIDs []uuid.UUID,
	sessionID *string, failed bool,
) string {
	if failed && len(createdTasks) == 0 && len(loggedDecisions) == 0 {
		return planFailedHeadline
	}
	var sb strings.Builder
	if failed {
		fmt.Fprintf(&sb, "%s\n", planFailedHeadline)
		fmt.Fprintf(&sb, "Already created before the failure — tasks (%d):\n", len(createdTasks))
	} else {
		fmt.Fprintf(&sb, "Plan confirmed. Tasks created (%d):\n", len(createdTasks))
	}
	for i, t := range createdTasks {
		if i < len(taskIDs) {
			fmt.Fprintf(&sb, "  • %s (id: %s)\n", t, taskIDs[i])
		} else {
			// Defensive only: materializePlan's three variants always append
			// title and id together, so this branch is unreachable in
			// practice — but a bullet line must never be silently dropped
			// just because an id happened to be missing.
			fmt.Fprintf(&sb, "  • %s\n", t)
		}
	}
	if len(loggedDecisions) > 0 {
		label := "Decisions logged"
		if failed {
			label = "Decisions logged before the failure"
		}
		fmt.Fprintf(&sb, "\n%s (%d):\n", label, len(loggedDecisions))
		for i, d := range loggedDecisions {
			if i < len(decisionIDs) {
				fmt.Fprintf(&sb, "  • %s (id: %s)\n", d, decisionIDs[i])
			} else {
				fmt.Fprintf(&sb, "  • %s\n", d)
			}
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
// UUIDs, logged decision titles, logged decision UUIDs, error) — [F0930-15]
// added the decision UUID slice, parallel-indexed to the title slice the
// same way the task UUID slice always has been.
//
// On the PG and SQLite paths a mid-loop failure returns nil/nil/nil/nil —
// the whole transaction rolled back, so there is nothing "already created"
// to report; the error text itself says so. On the sequential fallback path
// (neither backend wired — not a real deployment) a mid-loop failure returns
// whatever was actually written, matching materializePlanSequential's
// non-transactional semantics.
func (s *Server) materializePlan(
	ctx context.Context, phases []phaseInput, decisions []decisionInput, projectID *uuid.UUID, repoName string,
) ([]string, []uuid.UUID, []string, []uuid.UUID, error) {
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
) ([]string, []uuid.UUID, []string, []uuid.UUID, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("beginning plan transaction: %w", err)
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
			return nil, nil, nil, nil, fmt.Errorf("creating task %q (transaction rolled back, no changes made): %w", phase.Title, terr)
		}
		created = append(created, task.Title)
		ids = append(ids, task.ID)
	}

	decTx := s.pgDecision.WithTx(tx)
	var logged []string
	var decIDs []uuid.UUID
	for _, d := range decisions {
		if d.Title == "" || d.Decision == "" {
			continue
		}
		dec, derr := decTx.Log(ctx, decision.LogParams{
			ProjectID:      projectID,
			RepoName:       repoName,
			Title:          d.Title,
			Context:        d.Context,
			Decision:       d.Decision,
			Rationale:      d.Rationale,
			Alternatives:   d.Alternatives,
			Source:         decision.SourceManual,
			ActorSessionID: s.auditSessionID(ctx),
		})
		if derr != nil {
			return nil, nil, nil, nil, fmt.Errorf(
				"logging decision %q (transaction rolled back, no changes made): %w",
				clipSafe(d.Title, planErrorTitleMaxRunes), derr,
			)
		}
		logged = append(logged, dec.Title)
		decIDs = append(decIDs, dec.ID) // [F0930-15] dec.ID was already available here, previously unread.
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, nil, nil, fmt.Errorf(
			"committing plan transaction (OUTCOME UNKNOWN: the commit may or may not have been "+
				"applied by the database; verify with list_tasks/list_decisions before retrying): %w", err,
		)
	}
	return created, ids, logged, decIDs, nil
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
) ([]string, []uuid.UUID, []string, []uuid.UUID, error) {
	tx, err := s.sqliteGTD.DB().BeginTx(ctx)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("beginning plan transaction: %w", err)
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
			return nil, nil, nil, nil, fmt.Errorf("creating task %q (transaction rolled back, no changes made): %w", phase.Title, terr)
		}
		created = append(created, phase.Title)
		ids = append(ids, taskID)
	}

	if len(decisions) > 0 && s.sqliteDecision == nil {
		return nil, nil, nil, nil, fmt.Errorf("logging decisions (transaction rolled back, no changes made): sqlite decision store not wired")
	}
	var logged []string
	var decIDs []uuid.UUID
	for _, d := range decisions {
		if d.Title == "" || d.Decision == "" {
			continue
		}
		// [F0930-15] Capture LogTx's returned uuid.UUID — the ID is the only
		// thing it returns besides the error, and this used to be discarded
		// via `if _, derr := ...`.
		decID, derr := s.sqliteDecision.LogTx(ctx, tx, decision.LogParams{
			ProjectID:      projectID,
			RepoName:       repoName,
			Title:          d.Title,
			Context:        d.Context,
			Decision:       d.Decision,
			Rationale:      d.Rationale,
			Alternatives:   d.Alternatives,
			Source:         decision.SourceManual,
			ActorSessionID: s.auditSessionID(ctx),
		})
		if derr != nil {
			return nil, nil, nil, nil, fmt.Errorf(
				"logging decision %q (transaction rolled back, no changes made): %w",
				clipSafe(d.Title, planErrorTitleMaxRunes), derr,
			)
		}
		logged = append(logged, d.Title)
		decIDs = append(decIDs, decID)
	}

	if err := tx.Commit(); err != nil {
		return nil, nil, nil, nil, fmt.Errorf(
			"committing plan transaction (OUTCOME UNKNOWN: the commit may or may not have been "+
				"applied by the database; verify with list_tasks/list_decisions before retrying): %w", err,
		)
	}
	return created, ids, logged, decIDs, nil
}

// materializePlanSequential is the non-atomic fallback used only when the
// server is wired with neither a Postgres pool nor a SQLite GTD store (not a
// real deployment configuration — storage.NewServerStores only ever produces
// one or the other). Delegates to the pre-transaction createPhaseTasksWithIDs
// / logPlanDecisions pair, which preserve partial results on a mid-loop
// failure instead of discarding them.
func (s *Server) materializePlanSequential(
	ctx context.Context, phases []phaseInput, decisions []decisionInput, projectID *uuid.UUID, repoName string,
) ([]string, []uuid.UUID, []string, []uuid.UUID, error) {
	created, ids, err := s.createPhaseTasksWithIDs(ctx, phases, projectID)
	if err != nil {
		return created, ids, nil, nil, err
	}
	logged, decIDs, err := s.logPlanDecisions(ctx, decisions, projectID, repoName)
	if err != nil {
		return created, ids, logged, decIDs, err
	}
	return created, ids, logged, decIDs, nil
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
// materializePlanSequential (the non-atomic fallback path). Returns
// (logged decision titles, logged decision UUIDs, error) — [F0930-15] added
// the UUID slice, parallel-indexed to titles by the same append-together
// pattern as createPhaseTasksWithIDs.
func (s *Server) logPlanDecisions(
	ctx context.Context, decisions []decisionInput, projectID *uuid.UUID, repoName string,
) ([]string, []uuid.UUID, error) {
	var logged []string
	var ids []uuid.UUID
	for _, d := range decisions {
		if d.Title == "" || d.Decision == "" {
			continue
		}
		dec, err := s.decision.Log(ctx, decision.LogParams{
			ProjectID:      projectID,
			RepoName:       repoName,
			Title:          d.Title,
			Context:        d.Context,
			Decision:       d.Decision,
			Rationale:      d.Rationale,
			Alternatives:   d.Alternatives,
			Source:         decision.SourceManual,
			ActorSessionID: s.auditSessionID(ctx),
		})
		if err != nil {
			return logged, ids, fmt.Errorf(
				"logging decision %q (%d already logged): %w",
				clipSafe(d.Title, planErrorTitleMaxRunes), len(logged), err,
			)
		}
		logged = append(logged, dec.Title)
		ids = append(ids, dec.ID)
	}
	return logged, ids, nil
}

// createWorkSessionForPlan creates an in_progress work_session linking all
// phase tasks. It is best-effort for the two legitimate skip cases — no
// workSession store wired, no repo_name — which return (nil, nil): the
// caller (handleConfirmPlan) must not treat those as a failure to surface.
// An ATTEMPTED call to s.workSession.Create that returns any error (not just
// ErrAlreadyActive) is a THIRD, distinct case — [F0930-16]: it returns
// (nil, err) so the caller can tell "not attempted" apart from
// "attempted and failed" and only surface the latter, instead of the
// previous behaviour where all three produced byte-identical silence.
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
) (*string, error) {
	if s.workSession == nil {
		return nil, errWorkSessionNotAttempted
	}
	if repoName == "" {
		// No repo context — skip silently (non-repo sessions require start_work).
		return nil, errWorkSessionNotAttempted
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
		// [F0930-16] This IS an attempted-and-failed case (ErrAlreadyActive
		// or otherwise) — distinct from the two legitimate skips above, which
		// return before ever calling Create. Still does not block
		// confirm_plan's overall success (the tasks/decisions already
		// committed independently of this call); the caller surfaces this
		// error in the response text instead of only logging it.
		slog.Warn(
			"confirm_plan: could not create work session",
			"repo_name", repoName,
			"err", err,
		)
		// %w (not raw err): wrapcheck wants an external-package error
		// (worksession.Store.Create's) wrapped with local call-site context
		// before it crosses this function's boundary. %w still preserves the
		// chain for sanitizeWorkSessionErrorReason's
		// errors.Is(sessionErr, worksession.ErrAlreadyActive) check — only
		// the wrap's own added text is new, nothing is lost.
		return nil, fmt.Errorf("creating work session: %w", err)
	}

	id := sess.ID.String()
	return &id, nil
}
