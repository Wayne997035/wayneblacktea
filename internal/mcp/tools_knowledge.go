package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const maxKnowledgeListLimit = 200

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
				"Searches by full-text and vector similarity. "+
				"mode='coarse' searches only root-level documents (ignores section children) "+
				"for a quick overview; mode='fine' (default) searches all rows including "+
				"sections and returns heading_path in results.",
		),
		mcp.WithString("query", mcp.Description("Search query"), mcp.Required()),
		mcp.WithNumber("limit", mcp.Description("Maximum results to return (default 10)")),
		mcp.WithString("mode", mcp.Description("Search mode: 'fine' (default, all rows) or 'coarse' (root docs only)")),
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

const (
	mcpKnowledgeMaxTitleLen   = 200
	mcpKnowledgeMaxContentLen = 10 * 1024 * 1024 // 10 MB
	mcpKnowledgeMaxTagItemLen = 50
)

// validateKnowledgeArgs checks field-level invariants for add_knowledge tool
// input. It returns a non-empty human-readable message on the first violation.
func validateKnowledgeArgs(itemType, title, content, url string, tags []string) string {
	validTypes := map[string]bool{"article": true, "til": true, "bookmark": true, "zettelkasten": true}
	switch {
	case itemType == "" || title == "":
		return "type and title are required"
	case !validTypes[itemType]:
		return "type must be one of: article, til, bookmark, zettelkasten"
	case len(title) > mcpKnowledgeMaxTitleLen:
		return "title exceeds 200-character limit"
	case len(content) > mcpKnowledgeMaxContentLen:
		return "content exceeds 10 MB limit"
	case url != "" && !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://"):
		return "url must start with http:// or https://"
	case len(tags) > maxTagCount:
		return "tags array exceeds 20-entry limit"
	}
	for _, tag := range tags {
		if len(tag) > mcpKnowledgeMaxTagItemLen {
			return "each tag must be 50 characters or fewer"
		}
	}
	return ""
}

func (s *Server) handleAddKnowledge(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	itemType := stringArg(args, "type")
	title := stringArg(args, "title")
	content := stringArg(args, "content")
	url := stringArg(args, "url")

	var tags []string
	if raw := stringArg(args, "tags"); raw != "" {
		for _, t := range strings.Split(raw, ",") {
			if trimmed := strings.TrimSpace(t); trimmed != "" {
				tags = append(tags, trimmed)
			}
		}
	}

	if msg := validateKnowledgeArgs(itemType, title, content, url, tags); msg != "" {
		return mcp.NewToolResultError(msg), nil
	}

	cleanedTags, reason := sanitizeTags(tags)
	if reason != "" {
		return mcp.NewToolResultError(string(reason)), nil
	}
	tags = cleanedTags

	item, err := s.knowledge.AddItem(ctx, knowledge.AddItemParams{
		Type:    itemType,
		Title:   title,
		Content: content,
		URL:     url,
		Tags:    tags,
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("adding knowledge item: %v", err)), nil
	}

	s.launchAtomize("knowledge_items", item.ID, item.Content)

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
	const maxKnowledgeSearchLimit = 200
	if limit > maxKnowledgeSearchLimit {
		limit = maxKnowledgeSearchLimit
	}

	mode := stringArg(args, "mode")
	if mode != "" && mode != "fine" && mode != "coarse" {
		return mcp.NewToolResultError("mode must be 'fine' or 'coarse'"), nil
	}

	var (
		items []db.KnowledgeItem
		err   error
	)
	if mode == "coarse" {
		items, err = s.knowledge.SearchCoarse(ctx, query, limit)
	} else {
		items, err = s.knowledge.Search(ctx, query, limit)
	}
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
	if limit > maxKnowledgeListLimit {
		slog.Warn("list_knowledge limit clamped", "requested", limit, "max", maxKnowledgeListLimit)
		limit = maxKnowledgeListLimit
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
		return mcp.NewToolResultError("Notion integration not configured (NOTION_INTEGRATION_SECRET not set)"), nil
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
