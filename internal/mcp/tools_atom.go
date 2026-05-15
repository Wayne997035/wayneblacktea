package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/atom"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (s *Server) registerAtomTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool("traverse_atoms",
		mcp.WithDescription(
			"Traverse the memory atom graph from a starting atom, following links up to depth hops. "+
				"Returns visited atoms and links. Use to explore how facts relate.",
		),
		mcp.WithString("start_atom_id",
			mcp.Description("UUID of the starting atom"),
			mcp.Required()),
		mcp.WithNumber("depth",
			mcp.Description("Max hops to follow (1-5, default 2)")),
	), s.handleTraverseAtoms)

	ms.AddTool(mcp.NewTool("search_atoms",
		mcp.WithDescription(
			"Search memory atoms by keyword across content, keywords, and tags. "+
				"Returns atoms whose content or metadata contains the query string.",
		),
		mcp.WithString("query",
			mcp.Description("Search query"),
			mcp.Required()),
		mcp.WithNumber("limit",
			mcp.Description("Max results (default 10, max 20)")),
	), s.handleSearchAtoms)
}

// handleTraverseAtoms performs BFS traversal from a start atom.
func (s *Server) handleTraverseAtoms(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	rawID := stringArg(args, "start_atom_id")
	if rawID == "" {
		return mcp.NewToolResultError("start_atom_id is required"), nil
	}
	startID, err := uuid.Parse(rawID)
	if err != nil {
		return mcp.NewToolResultError("invalid start_atom_id UUID"), nil
	}

	depth := int(numberArg(args, "depth"))
	if depth <= 0 {
		depth = 2
	}
	if depth > 5 {
		depth = 5
	}

	result, err := s.atom.Traverse(ctx, startID, depth)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("traversing atoms: %v", err)), nil
	}
	if result == nil {
		result = &atom.TraverseResult{
			Atoms: []atom.Atom{},
			Links: []atom.Link{},
		}
	}
	if result.Atoms == nil {
		result.Atoms = []atom.Atom{}
	}
	if result.Links == nil {
		result.Links = []atom.Link{}
	}
	return jsonText(result)
}

// handleSearchAtoms searches atoms by keyword.
func (s *Server) handleSearchAtoms(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	query := stringArg(args, "query")
	if query == "" {
		return mcp.NewToolResultError("query is required"), nil
	}

	limit := int(numberArg(args, "limit"))
	if limit <= 0 {
		limit = 10
	}
	if limit > 20 {
		limit = 20
	}

	wsID := s.workspaceUUID()
	atoms, err := s.atom.Search(ctx, wsID, query, limit)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("searching atoms: %v", err)), nil
	}
	if atoms == nil {
		atoms = []atom.Atom{}
	}
	return jsonText(atoms)
}

// atomizeAndPersist calls the LLM atomizer and persists atoms+links.
// Called in a goroutine; logs errors via slog.Warn, never panics.
func atomizeAndPersist(
	ctx context.Context,
	atomizer *ai.Atomizer,
	store atom.StoreIface,
	wsID *uuid.UUID,
	parentTable string,
	parentID uuid.UUID,
	text string,
) {
	result, err := atomizer.Atomize(ctx, text)
	if errors.Is(err, ai.ErrEmptyAtomizeResponse) {
		// Empty response is not unusual (e.g. very short text); log at debug and return.
		slog.Warn("atomize: empty LLM response", "parent_table", parentTable, "parent_id", parentID)
		return
	}
	if err != nil || result == nil {
		slog.Warn("atomize: LLM call failed", "err", err)
		return
	}
	atomIDs := make([]uuid.UUID, 0, len(result.Atoms))
	for _, c := range result.Atoms {
		a, err := store.AddAtom(ctx, atom.AddAtomParams{
			WorkspaceID: wsID,
			ParentTable: parentTable,
			ParentID:    parentID,
			Content:     ai.RedactCredentials(c.Content), // M1: scrub before persist
			Keywords:    c.Keywords,
			Tags:        c.Tags,
		})
		if err != nil {
			slog.Warn("atomize: persist atom failed", "err", err)
			return
		}
		atomIDs = append(atomIDs, a.ID)
	}

	persistLinks(ctx, store, atomIDs, result.Links)
}

// allowedLinkTypes is the Go-layer allowlist for LLM-emitted link types (M2).
var allowedLinkTypes = map[string]bool{
	"same_entity": true, "same_action": true,
	"same_time": true, "same_project": true,
}

// persistLinks validates and stores LLM-emitted link candidates.
// Extracted to keep atomizeAndPersist under gocyclo limit.
func persistLinks(ctx context.Context, store atom.StoreIface, atomIDs []uuid.UUID, links []ai.LinkCandidate) {
	for _, l := range links {
		// Guard negative, out-of-bounds, and self-loop indices (reviewer Major + M2).
		if l.FromIdx < 0 || l.ToIdx < 0 || l.FromIdx >= len(atomIDs) || l.ToIdx >= len(atomIDs) || l.FromIdx == l.ToIdx {
			continue
		}
		if !allowedLinkTypes[l.LinkType] {
			slog.Warn("atomize: invalid link_type from LLM", "link_type", l.LinkType)
			continue
		}
		// Clamp confidence to [0, 1] (M2).
		if l.Confidence < 0 {
			l.Confidence = 0
		} else if l.Confidence > 1 {
			l.Confidence = 1
		}
		if err := store.AddLink(ctx, atom.AddLinkParams{
			FromAtomID: atomIDs[l.FromIdx],
			ToAtomID:   atomIDs[l.ToIdx],
			LinkType:   l.LinkType,
			Confidence: l.Confidence,
		}); err != nil {
			slog.Warn("atomize: persist link failed", "err", err)
		}
	}
}

// LaunchAtomize is the exported form of launchAtomize, used to inject background
// atomization into HTTP handler paths that share the same MCP server's AI chain.
func (s *Server) LaunchAtomize(parentTable string, parentID uuid.UUID, text string) {
	s.launchAtomize(parentTable, parentID, text)
}

// launchAtomize spawns atomizeAndPersist in a background goroutine with its own
// independent timeout so the MCP request is never blocked. A 5-slot semaphore
// (atomizeSem) caps concurrent Haiku API calls to prevent budget exhaustion on
// rapid add_* bursts. (security M4)
func (s *Server) launchAtomize(parentTable string, parentID uuid.UUID, text string) {
	if s.atomizer == nil || s.atom == nil {
		return
	}
	go func(pid uuid.UUID, t string) {
		select {
		case s.atomizeSem <- struct{}{}:
			defer func() { <-s.atomizeSem }()
		default:
			slog.Warn("launchAtomize: concurrency limit reached, skipping", "parent_id", pid)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		atomizeAndPersist(ctx, s.atomizer, s.atom, s.workspaceUUID(), parentTable, pid, t)
	}(parentID, text)
}
