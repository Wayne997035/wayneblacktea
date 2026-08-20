package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// knowledgeNavItem is the lightweight projection returned by navigate_knowledge
// and outline_knowledge — content is intentionally omitted for efficiency.
type knowledgeNavItem struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	HeadingPath  string `json:"heading_path,omitempty"`
	HeadingLevel int    `json:"heading_level"`
	Type         string `json:"type"`
	CreatedAt    string `json:"created_at,omitempty"`
}

// navItemFromDB projects a db.KnowledgeItem into the lightweight nav shape.
// Title/HeadingPath go through clipSafe (tools_context.go) — U13
// (2026-08-20-mcp-surface-spec.md); reusing tools_knowledge.go's
// knowledgeTitleMaxRunes keeps this file's bound in sync with the same
// column's cap on the full-record readers (add_knowledge/search_knowledge/
// list_knowledge). One fix here covers all 3 call sites in this file
// (navigate_knowledge's root/children branches and outline_knowledge) since
// every one of them funnels through this function.
func navItemFromDB(item *db.KnowledgeItem) knowledgeNavItem {
	nav := knowledgeNavItem{
		ID:    item.ID.String(),
		Title: clipSafe(item.Title, knowledgeTitleMaxRunes),
		Type:  item.Type,
	}
	if item.HeadingPath.Valid {
		nav.HeadingPath = clipSafe(item.HeadingPath.String, knowledgeTitleMaxRunes)
	}
	if item.HeadingLevel.Valid {
		nav.HeadingLevel = int(item.HeadingLevel.Int32)
	}
	if item.CreatedAt.Valid {
		nav.CreatedAt = item.CreatedAt.Time.UTC().Format(time.RFC3339)
	}
	return nav
}

func (s *Server) registerKnowledgeNavTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool(
		"navigate_knowledge",
		mcp.WithDescription(
			"Browse the knowledge tree. With no arguments returns all root-level items "+
				"(parent_id IS NULL). Supply parent_id to list the direct children of that item "+
				"(section headings of a document). Returns lightweight metadata only — no content.",
		),
		mcp.WithString("parent_id", mcp.Description("UUID of the parent item whose children to list. Omit to list root items.")),
	), s.handleNavigateKnowledge)

	ms.AddTool(mcp.NewTool(
		"outline_knowledge",
		mcp.WithDescription(
			"Returns the heading tree for a root document. Fetches all children of the given "+
				"item_id ordered by heading level — useful as a lightweight table of contents "+
				"before deciding which section to read. Returns no content, only headings.",
		),
		mcp.WithString("item_id", mcp.Description("UUID of the root knowledge item to outline"), mcp.Required()),
	), s.handleOutlineKnowledge)
}

func (s *Server) handleNavigateKnowledge(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	rawParentID := stringArg(args, "parent_id")

	if rawParentID == "" {
		// List root items (parent_id IS NULL).
		items, err := s.knowledge.ListRoots(ctx)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("listing root knowledge items: %v", err)), nil
		}
		nav := make([]knowledgeNavItem, 0, len(items))
		for _, item := range items {
			nav = append(nav, navItemFromDB(item))
		}
		return jsonText(nav)
	}

	parentID, err := uuid.Parse(rawParentID)
	if err != nil {
		return mcp.NewToolResultError("invalid parent_id UUID"), nil
	}

	items, err := s.knowledge.ListChildren(ctx, parentID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("listing children of %s: %v", rawParentID, err)), nil
	}
	nav := make([]knowledgeNavItem, 0, len(items))
	for _, item := range items {
		nav = append(nav, navItemFromDB(item))
	}
	return jsonText(nav)
}

func (s *Server) handleOutlineKnowledge(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	itemID, errResult := requireUUIDArg(args, "item_id", "invalid item_id UUID")
	if errResult != nil {
		return errResult, nil
	}

	items, err := s.knowledge.ListChildren(ctx, itemID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("outlining knowledge item %s: %v", itemID, err)), nil
	}
	nav := make([]knowledgeNavItem, 0, len(items))
	for _, item := range items {
		nav = append(nav, navItemFromDB(item))
	}
	return jsonText(nav)
}
