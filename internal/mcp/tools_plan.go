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
	ms.AddTool(mcp.NewTool("confirm_plan",
		mcp.WithDescription(
			"CALL THIS when user confirms a plan ('可以','好','go','ok','明天做','start','開始'). "+
				"Atomically creates GTD tasks for each phase AND logs each decision in one call. "+
				"Also creates an in_progress work_session linking all phase tasks. "+
				"Use this INSTEAD of calling add_task + log_decision separately — it is more reliable.",
		),
		mcp.WithString("phases",
			mcp.Description(`JSON array of phases as tasks. Each: {"title":"...","description":"...","priority":2}`),
			mcp.Required(),
		),
		mcp.WithString("decisions",
			mcp.Description(`JSON array of decisions. Each: {"title":"...","context":"...","decision":"...","rationale":"...","alternatives":""}`),
		),
		mcp.WithString("project_id", mcp.Description("Project UUID (optional)")),
		mcp.WithString("repo_name", mcp.Description("Repository name (optional)")),
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
			return mcp.NewToolResultError("invalid project_id UUID"), nil
		}
		projectID = &id
	}
	repoName := stringArg(args, "repo_name")

	// Create phase tasks and collect their UUIDs for the work session link.
	createdTasks, taskIDs, err := s.createPhaseTasksWithIDs(ctx, phases, projectID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	loggedDecisions, err := s.logPlanDecisions(ctx, decisions, projectID, repoName)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Always create an in_progress work session (D2: no bool flag).
	// Best-effort: work session failure must not block the tasks/decisions result.
	sessionID := s.createWorkSessionForPlan(ctx, repoName, projectID, phases, taskIDs)

	var sb strings.Builder
	fmt.Fprintf(&sb, "Plan confirmed. Tasks created (%d):\n", len(createdTasks))
	for _, t := range createdTasks {
		fmt.Fprintf(&sb, "  • %s\n", t)
	}
	if len(loggedDecisions) > 0 {
		fmt.Fprintf(&sb, "\nDecisions logged (%d):\n", len(loggedDecisions))
		for _, d := range loggedDecisions {
			fmt.Fprintf(&sb, "  • %s\n", d)
		}
	}
	if sessionID != nil {
		fmt.Fprintf(&sb, "\nWork session started: %s\n", *sessionID)
	}
	return mcp.NewToolResultText(sb.String()), nil
}

// createPhaseTasksWithIDs creates tasks for each phase and returns both the
// title list (for the text response) and the UUID list (for work session linking).
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
			return nil, nil, fmt.Errorf("creating task %q: %w", phase.Title, err)
		}
		created = append(created, task.Title)
		ids = append(ids, task.ID)
	}
	return created, ids, nil
}

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
		})
		if err != nil {
			return nil, fmt.Errorf("logging decision %q: %w", d.Title, err)
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
	})
	if err != nil {
		// ErrAlreadyActive: a session is already in_progress for this repo.
		// Log a warning; do not block confirm_plan.
		slog.Warn("confirm_plan: could not create work session",
			"repo_name", repoName,
			"err", err,
		)
		return nil
	}

	id := sess.ID.String()
	return &id
}
