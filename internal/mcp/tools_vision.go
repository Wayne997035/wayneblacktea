package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/validator"
	"github.com/Wayne997035/wayneblacktea/internal/vision"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// MCP length caps for add_vision_item — mirror the HTTP boundary
// (internal/handler/vision_handler.go:45-66) exactly. The MCP path had no
// caps at all before this change (unbounded write), which is the reverse of
// the usual "MCP is stricter than HTTP" framing for this repo — see
// backend-security-design.md §2.
const (
	mcpVisionMaxTitleRunes      = 255
	mcpVisionMaxWhyBlockedRunes = 2000
)

func (s *Server) registerVisionTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool(
		"add_vision_item",
		mcp.WithDescription(
			"Records a future idea that cannot be acted on now. "+
				"CALL when user says: '未來想做', '之後再說', '現在還不能', "+
				"'等 X 完成才能做', '記一下以後', or anything that is conceptually "+
				"valuable but currently blocked by dependencies, resources, or timing.",
		),
		mcp.WithString("title", mcp.Description("Short descriptive title of the vision item"), mcp.Required()),
		mcp.WithString("why_blocked", mcp.Description("Why this cannot be acted on now — what is blocking it"), mcp.Required()),
		mcp.WithString("depends_on", mcp.Description("JSON array of task IDs or strings this item depends on (e.g. '[\"task-abc\"]')")),
		mcp.WithString("parent_initiative", mcp.Description("Name of the broader initiative or roadmap this belongs to")),
		mcp.WithString("context_md", mcp.Description("Optional markdown notes with additional context")),
		mcp.WithString("repo_name", mcp.Description("Repository or project slug this idea belongs to")),
	), s.handleAddVisionItem)

	ms.AddTool(mcp.NewTool(
		"list_vision_items",
		mcp.WithDescription("Lists vision items (future ideas). Returns summary view (no context_md) for brevity."),
		mcp.WithString("parent_initiative", mcp.Description("Filter by initiative/roadmap name")),
		mcp.WithString("status", mcp.Description(
			"Filter by status: open, discussing, maturing, promoted, dismissed. Default: all non-dismissed.",
		)),
	), s.handleListVisionItems)

	ms.AddTool(mcp.NewTool(
		"update_vision_item",
		mcp.WithDescription("Updates a vision item's status or context. Auto-sets last_discussed_at to NOW() if not provided."),
		mcp.WithString("id", mcp.Description("Vision item UUID"), mcp.Required()),
		mcp.WithString("status", mcp.Description("New status: open, discussing, maturing, promoted, dismissed")),
		mcp.WithString("context_md", mcp.Description(
			"Updated markdown context notes. REPLACES the stored value entirely — no "+
				"append/merge. Omitting this field (or passing an empty string) leaves "+
				"the stored value unchanged; there is no way to explicitly clear it via "+
				"this tool.",
		)),
		mcp.WithString("last_discussed_at", mcp.Description("Override discussion timestamp in RFC3339 format")),
	), s.handleUpdateVisionItem)

	ms.AddTool(mcp.NewTool(
		"promote_vision_to_task",
		mcp.WithDescription(
			"Promotes a vision item to a real GTD task. "+
				"Creates the GTD task, marks vision item status='promoted', "+
				"and links vision item to the new task via promoted_task_id.",
		),
		mcp.WithString("id", mcp.Description("Vision item UUID to promote"), mcp.Required()),
		mcp.WithString("title", mcp.Description("GTD task title (defaults to vision item title if omitted)")),
		mcp.WithString("description", mcp.Description("GTD task description")),
		mcp.WithNumber("priority", mcp.Description("GTD task priority 1-5 (lower is higher). Default: 3.")),
		mcp.WithString("due_date", mcp.Description("Optional due date for the promoted task in RFC3339 format (e.g. 2026-05-31T00:00:00Z)")),
	), s.handlePromoteVisionToTask)
}

func (s *Server) handleAddVisionItem(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	title := stringArg(args, "title")
	whyBlocked := stringArg(args, "why_blocked")
	if title == "" {
		return mcp.NewToolResultError("title is required"), nil
	}
	if len([]rune(title)) > mcpVisionMaxTitleRunes {
		return mcp.NewToolResultError("title exceeds 255 characters"), nil
	}
	if whyBlocked == "" {
		return mcp.NewToolResultError("why_blocked is required"), nil
	}
	if len([]rune(whyBlocked)) > mcpVisionMaxWhyBlockedRunes {
		return mcp.NewToolResultError("why_blocked exceeds 2000 characters"), nil
	}

	// Vagueness check on title and why_blocked (warn-only, matching the HTTP
	// twin — vision items are intentionally forward-looking and not subject
	// to strict-mode rejection). No HTTP headers are available over MCP, so
	// warnings ride along in the result body instead.
	var allWarnings []string
	allWarnings = append(allWarnings, validator.CheckVagueness("title", title, "general")...)
	allWarnings = append(allWarnings, validator.CheckVagueness("why_blocked", whyBlocked, "general")...)

	var dependsOn []string
	if raw := stringArg(args, "depends_on"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &dependsOn); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("depends_on must be a valid JSON array: %v", err)), nil
		}
	}

	p := vision.AddVisionParams{
		Title:            title,
		WhyBlocked:       whyBlocked,
		DependsOn:        dependsOn,
		ParentInitiative: stringArg(args, "parent_initiative"),
		ContextMD:        stringArg(args, "context_md"),
		RepoName:         stringArg(args, "repo_name"),
	}
	// Scope to workspace if available.
	if wsID := s.workspaceUUID(); wsID != nil {
		p.WorkspaceID = wsID
	}

	item, err := s.vision.Add(ctx, p)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("adding vision item: %v", err)), nil
	}
	if len(allWarnings) > 0 {
		return jsonText(map[string]any{"item": item, "warnings": allWarnings})
	}
	return jsonText(item)
}

func (s *Server) handleListVisionItems(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	filter := vision.ListVisionFilter{
		ParentInitiative: stringArg(args, "parent_initiative"),
	}
	if raw := stringArg(args, "status"); raw != "" {
		s := vision.VisionStatus(raw)
		switch s {
		case vision.VisionStatusOpen, vision.VisionStatusDiscussing,
			vision.VisionStatusMaturing, vision.VisionStatusPromoted,
			vision.VisionStatusDismissed:
		default:
			return mcp.NewToolResultError("status must be one of: open, discussing, maturing, promoted, dismissed"), nil
		}
		filter.Status = s
	}

	items, err := s.vision.List(ctx, filter)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("listing vision items: %v", err)), nil
	}
	if items == nil {
		items = []vision.VisionItemSummary{}
	}
	return jsonText(items)
}

func (s *Server) handleUpdateVisionItem(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	id, errResult := requireUUIDArg(args, "id", "invalid id UUID")
	if errResult != nil {
		return errResult, nil
	}

	p := vision.UpdateVisionParams{}

	if raw := stringArg(args, "status"); raw != "" {
		st := vision.VisionStatus(raw)
		switch st {
		case vision.VisionStatusOpen, vision.VisionStatusDiscussing,
			vision.VisionStatusMaturing, vision.VisionStatusPromoted,
			vision.VisionStatusDismissed:
		default:
			return mcp.NewToolResultError("status must be one of: open, discussing, maturing, promoted, dismissed"), nil
		}
		p.Status = &st
	}

	if raw := stringArg(args, "context_md"); raw != "" {
		v := raw
		p.ContextMD = &v
	}

	if raw := stringArg(args, "last_discussed_at"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return mcp.NewToolResultError("invalid last_discussed_at: must be RFC3339"), nil
		}
		p.LastDiscussedAt = &t
	}

	item, err := s.vision.Update(ctx, id, p)
	if errors.Is(err, vision.ErrNotFound) {
		return mcp.NewToolResultError("vision item not found"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("updating vision item: %v", err)), nil
	}
	return jsonText(item)
}

func (s *Server) handlePromoteVisionToTask(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	visionID, errResult := requireUUIDArg(args, "id", "invalid id UUID")
	if errResult != nil {
		return errResult, nil
	}

	// Load vision item to get default title.
	visionItem, err := s.vision.GetByID(ctx, visionID)
	if errors.Is(err, vision.ErrNotFound) {
		return mcp.NewToolResultError("vision item not found"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("loading vision item: %v", err)), nil
	}

	// Determine GTD task title.
	title := stringArg(args, "title")
	if title == "" {
		title = visionItem.Title
	}

	// Create GTD task, linking back to the vision item (migration 000050).
	priority := numberArg(args, "priority")
	if priority == 0 {
		priority = 3
	}
	taskParams := gtd.CreateTaskParams{
		Title:        title,
		Description:  stringArg(args, "description"),
		Priority:     priority,
		VisionItemID: &visionID,
	}
	if rawDue := stringArg(args, "due_date"); rawDue != "" {
		t, parseErr := time.Parse(time.RFC3339, rawDue)
		if parseErr != nil {
			return mcp.NewToolResultError("invalid due_date: must be RFC3339 (e.g. 2026-05-31T00:00:00Z)"), nil
		}
		taskParams.DueDate = &t
	}
	task, err := s.gtd.CreateTask(ctx, taskParams)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("creating GTD task: %v", err)), nil
	}

	// Mark vision item as promoted.
	taskID := task.ID
	promoted, err := s.vision.Promote(ctx, visionID, vision.PromoteParams{PromotedTaskID: taskID})
	if errors.Is(err, vision.ErrNotFound) {
		return mcp.NewToolResultError("vision item not found after task creation"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("promoting vision item: %v", err)), nil
	}

	return jsonText(map[string]any{
		"task":        task,
		"vision_item": promoted,
	})
}
