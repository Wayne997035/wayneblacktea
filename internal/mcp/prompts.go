package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerPrompts registers the 4 workflow prompts.
// All prompts are static (no arguments) and return a single user-role message
// that references resource URIs — they do not embed tool-call instructions
// that could bypass the proposal gate (§2 adversarial-input posture).
func (s *Server) registerPrompts(ms *server.MCPServer) {
	ms.AddPrompt(
		mcp.NewPrompt("start_work",
			mcp.WithPromptDescription(
				"Workflow prompt for beginning a work session. "+
					"Guides the AI client to orient itself via dashboard resources "+
					"before dispatching any agents.",
			),
		),
		s.handlePromptStartWork,
	)

	ms.AddPrompt(
		mcp.NewPrompt("closeout_session",
			mcp.WithPromptDescription(
				"Workflow prompt for closing out a session. "+
					"Guides the AI client to persist handoff, complete in-progress tasks, "+
					"document decisions, and verify the system is clean.",
			),
		),
		s.handlePromptCloseoutSession,
	)

	ms.AddPrompt(
		mcp.NewPrompt("plan_tomorrow",
			mcp.WithPromptDescription(
				"Workflow prompt for planning the next work day. "+
					"Guides the AI client to review upcoming tasks and forgotten signals, "+
					"then create well-formed follow-up tasks with file:line + acceptance criteria.",
			),
		),
		s.handlePromptPlanTomorrow,
	)

	ms.AddPrompt(
		mcp.NewPrompt("reconcile_dashboard",
			mcp.WithPromptDescription(
				"Workflow prompt for reconciling the dashboard after undocumented changes. "+
					"Guides the AI client to review system health, log missing decisions, "+
					"and triage completion candidates.",
			),
		),
		s.handlePromptReconcileDashboard,
	)
}

// promptUserMessage is a convenience constructor for a single-message
// PromptResult with role=user and text content.
func promptUserMessage(text string) *mcp.GetPromptResult {
	return &mcp.GetPromptResult{
		Messages: []mcp.PromptMessage{
			{
				Role:    mcp.RoleUser,
				Content: mcp.TextContent{Type: "text", Text: text},
			},
		},
	}
}

// handlePromptStartWork returns the start_work workflow prompt.
func (s *Server) handlePromptStartWork(
	_ context.Context,
	_ mcp.GetPromptRequest,
) (*mcp.GetPromptResult, error) {
	return promptUserMessage(
		"SESSION START WORKFLOW\n\n" +
			"1. Read wayneblacktea://dashboard/overview. " +
			"If pending_handoff=true, call resolve_handoff tool immediately.\n" +
			"2. Read wayneblacktea://gtd/current to identify top_task.\n" +
			"3. If top_task is non-null, call update_task(id, status=\"in_progress\") " +
			"before dispatching any agent or beginning any implementation.\n" +
			"4. If unresolved_handoff=true and age < 48h, read the handoff context " +
			"and incorporate it into the first task's dispatch prompt.\n\n" +
			"Do NOT begin any implementation until steps 1–3 are complete.",
	), nil
}

// handlePromptCloseoutSession returns the closeout_session workflow prompt.
func (s *Server) handlePromptCloseoutSession(
	_ context.Context,
	_ mcp.GetPromptRequest,
) (*mcp.GetPromptResult, error) {
	return promptUserMessage(
		"SESSION CLOSEOUT WORKFLOW\n\n" +
			"1. Call set_session_handoff with the highest-priority next-session task " +
			"and a clear context_summary so the next session can start without re-reading.\n" +
			"2. For any task currently in_progress whose build just passed, " +
			"call complete_task(id, artifact=\"<PR URL or commit SHA>\") immediately.\n" +
			"3. For any architectural choice made this session without a preceding log_decision, " +
			"call log_decision now (title + rationale required).\n" +
			"4. Read wayneblacktea://system/health and verify tasks.in_progress stuck count is 0. " +
			"If stuck > 0, investigate and either complete or re-scope those tasks before signing off.\n\n" +
			"A clean closeout means: handoff set, no stuck tasks, all decisions documented.",
	), nil
}

// handlePromptPlanTomorrow returns the plan_tomorrow workflow prompt.
func (s *Server) handlePromptPlanTomorrow(
	_ context.Context,
	_ mcp.GetPromptRequest,
) (*mcp.GetPromptResult, error) {
	return promptUserMessage(
		"PLAN TOMORROW WORKFLOW\n\n" +
			"1. Read wayneblacktea://dashboard/upcoming. " +
			"Identify tasks in today / tomorrow buckets with due_date in the next 48h.\n" +
			"2. Read wayneblacktea://system/health. " +
			"Check forgotten_signals and discipline.drift_count_24h.\n" +
			"3. For each follow-up task discovered, call add_task with:\n" +
			"   - title: descriptive, includes file:line when relevant\n" +
			"   - description: exact change + acceptance criteria (e.g. test name that must pass)\n" +
			"   - due_date: set to tomorrow or the relevant deadline\n" +
			"4. Call set_session_handoff with the single highest-priority task for tomorrow " +
			"and a one-sentence rationale.\n\n" +
			"Vague task descriptions (\"fix the bug\", \"see above\") are rejected — " +
			"every task MUST have file:line + acceptance criteria.",
	), nil
}

// handlePromptReconcileDashboard returns the reconcile_dashboard workflow prompt.
func (s *Server) handlePromptReconcileDashboard(
	_ context.Context,
	_ mcp.GetPromptRequest,
) (*mcp.GetPromptResult, error) {
	return promptUserMessage(
		"RECONCILE DASHBOARD WORKFLOW\n\n" +
			"1. Read wayneblacktea://system/health.\n" +
			"2. If discipline.drift_count_24h > 0: for each undocumented mutation, " +
			"call log_decision with the title of what changed and why.\n" +
			"3. Call detect_completion_candidates tool and review the returned candidates list.\n" +
			"4. For each candidate: if the task is genuinely done, call complete_task(id, artifact); " +
			"if not done, call update_task to add a note explaining why evidence exists but task is still open.\n" +
			"5. After triage, re-read wayneblacktea://system/health to confirm " +
			"discipline.drift_count_24h and tasks.stuck have both decreased.\n\n" +
			"Goal: zero undocumented mutations, zero stuck tasks, all candidates triaged.",
	), nil
}
