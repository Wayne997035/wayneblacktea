package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/waynechen/wayneblacktea/internal/decision"
	"github.com/waynechen/wayneblacktea/internal/gtd"
)

func (s *Server) registerPlanTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool("confirm_plan",
		mcp.WithDescription(
			"CALL THIS when user confirms a plan ('可以','好','go','ok','明天做','start','開始'). "+
				"Atomically creates GTD tasks for each phase AND logs each decision in one call. "+
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

	createdTasks, err := s.createPhaseTasks(ctx, phases, projectID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	loggedDecisions, err := s.logPlanDecisions(ctx, decisions, projectID, repoName)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

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
	return mcp.NewToolResultText(sb.String()), nil
}

func (s *Server) createPhaseTasks(ctx context.Context, phases []phaseInput, projectID *uuid.UUID) ([]string, error) {
	var created []string
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
			return nil, fmt.Errorf("creating task %q: %w", phase.Title, err)
		}
		created = append(created, task.Title)
	}
	return created, nil
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
