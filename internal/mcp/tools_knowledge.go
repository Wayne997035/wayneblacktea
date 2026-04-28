package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (s *Server) registerKnowledgeTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool("add_knowledge",
		mcp.WithDescription(
			"CALL to save a new knowledge item (article, TIL, bookmark, zettelkasten). "+
				"Used after Discord bot analysis or manual learning.",
		),
		mcp.WithString("type", mcp.Description("Item type: article, til, bookmark, or zettelkasten"), mcp.Required()),
		mcp.WithString("title", mcp.Description("Item title"), mcp.Required()),
		mcp.WithString("content", mcp.Description("Item body / notes")),
		mcp.WithString("url", mcp.Description("Source URL")),
		mcp.WithString("tags", mcp.Description("Comma-separated tags")),
	), s.handleAddKnowledge)

	ms.AddTool(mcp.NewTool("search_knowledge",
		mcp.WithDescription(
			"CALL before fetching/analyzing a URL — check if content is already saved. "+
				"Searches by full-text and vector similarity.",
		),
		mcp.WithString("query", mcp.Description("Search query"), mcp.Required()),
		mcp.WithNumber("limit", mcp.Description("Maximum results to return (default 10)")),
	), s.handleSearchKnowledge)

	ms.AddTool(mcp.NewTool("list_knowledge",
		mcp.WithDescription("Lists knowledge items ordered by creation date."),
		mcp.WithNumber("limit", mcp.Description("Maximum results to return (default 20)")),
		mcp.WithNumber("offset", mcp.Description("Pagination offset (default 0)")),
	), s.handleListKnowledge)

	ms.AddTool(mcp.NewTool("sync_to_notion",
		mcp.WithDescription("Syncs a knowledge item to the configured Notion database and returns the page URL."),
		mcp.WithString("knowledge_id", mcp.Description("Knowledge item UUID"), mcp.Required()),
	), s.handleSyncToNotion)
}

func (s *Server) handleAddKnowledge(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	itemType := stringArg(args, "type")
	title := stringArg(args, "title")

	if itemType == "" || title == "" {
		return mcp.NewToolResultError("type and title are required"), nil
	}

	validTypes := map[string]bool{"article": true, "til": true, "bookmark": true, "zettelkasten": true}
	if !validTypes[itemType] {
		return mcp.NewToolResultError("type must be one of: article, til, bookmark, zettelkasten"), nil
	}

	var tags []string
	if raw := stringArg(args, "tags"); raw != "" {
		for _, t := range strings.Split(raw, ",") {
			if trimmed := strings.TrimSpace(t); trimmed != "" {
				tags = append(tags, trimmed)
			}
		}
	}

	item, err := s.knowledge.AddItem(ctx, knowledge.AddItemParams{
		Type:    itemType,
		Title:   title,
		Content: stringArg(args, "content"),
		URL:     stringArg(args, "url"),
		Tags:    tags,
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("adding knowledge item: %v", err)), nil
	}

	// Auto-propose a concept card for review-eligible item types so the spaced
	// repetition queue is fed without an explicit user step. Failure is logged
	// but does not roll back the knowledge item.
	prop, perr := s.proposal.AutoProposeConceptFromKnowledge(ctx, item, "mcp:add_knowledge")
	if perr != nil {
		slog.Warn("auto-propose concept failed", "knowledge_id", item.ID, "err", perr)
	}
	resp := addKnowledgeResult{Item: item}
	if prop != nil {
		resp.ConceptProposalID = prop.ID.String()
	}
	return jsonText(resp)
}

// addKnowledgeResult wraps the freshly-created knowledge item with the optional
// concept proposal ID so MCP clients can immediately call confirm_proposal.
type addKnowledgeResult struct {
	Item              any    `json:"item"`
	ConceptProposalID string `json:"concept_proposal_id,omitempty"`
}

func (s *Server) handleSearchKnowledge(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	query := stringArg(args, "query")
	if query == "" {
		return mcp.NewToolResultError("query is required"), nil
	}

	limit := int(numberArg(args, "limit"))
	if limit <= 0 {
		limit = 10
	}

	items, err := s.knowledge.Search(ctx, query, limit)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("searching knowledge: %v", err)), nil
	}
	return jsonText(items)
}

func (s *Server) handleListKnowledge(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	limit := int(numberArg(args, "limit"))
	if limit <= 0 {
		limit = 20
	}
	offset := int(numberArg(args, "offset"))
	if offset < 0 {
		offset = 0
	}

	items, err := s.knowledge.List(ctx, limit, offset)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("listing knowledge: %v", err)), nil
	}
	return jsonText(items)
}

func (s *Server) handleSyncToNotion(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	rawID := stringArg(args, "knowledge_id")
	if rawID == "" {
		return mcp.NewToolResultError("knowledge_id is required"), nil
	}

	id, err := uuid.Parse(rawID)
	if err != nil {
		return mcp.NewToolResultError("invalid knowledge_id UUID"), nil
	}

	if s.notion == nil {
		return mcp.NewToolResultError("Notion integration not configured (NOTION_API_KEY not set)"), nil
	}

	item, err := s.knowledge.GetByID(ctx, id)
	if errors.Is(err, knowledge.ErrNotFound) {
		return mcp.NewToolResultError(fmt.Sprintf("knowledge item %s not found", rawID)), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("fetching knowledge item: %v", err)), nil
	}

	pageURL, err := s.notion.CreatePage(ctx, item.Title, item.Content, item.Type)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("creating Notion page: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Notion page created: %s", pageURL)), nil
}
