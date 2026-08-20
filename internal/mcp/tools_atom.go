package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/atom"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/redact"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ---------------------------------------------------------------------------
// U13 Phase B — boundary-marker neutralisation for tools_atom.go
// (2026-08-20-mcp-surface-spec.md; .specs/2026-08-20-u13-inventory.md).
// ---------------------------------------------------------------------------

// atomContentMaxRunes bounds Atom.Content at read time — U13. Mirrors
// gtdBodyMaxRunes/decisionBodyMaxRunes: generous, above any existing
// write-time cap (atom.CheckPromotionEligibility only enforces a MINIMUM
// content length for promotion, atom.PromotionMinContentRunes; there is no
// write-time maximum on Content) — exists purely to stop marker-stuffing /
// pathological growth on read.
//
// atomKeywordMaxRunes bounds each Keywords/Tags entry — much smaller than
// atomContentMaxRunes because these are meant to be single-word/short-phrase
// labels (ai.Atomizer.Atomize's own prompt asks for keywords, not
// sentences); still generous relative to that intent, existing purely as a
// marker-stuffing backstop like every other read-time cap in this dispatch.
const (
	atomContentMaxRunes = 20000
	atomKeywordMaxRunes = 200
)

// wrapUntrustedAtom returns a copy of a with Content clipSafe'd and every
// Keywords/Tags entry clipSafe'd — U13. Mirrors wrapUntrustedDecision/
// wrapUntrustedTask's copy-not-mutate contract. Content is the field flagged
// in the U13 inventory (Phase A); Keywords/Tags are additionally in scope
// here because they are ALSO ai.Atomizer LLM output (internal/ai/
// atomizer.go) derived from the same parent text as Content — capped at 5/3
// ENTRIES there but with no per-string LENGTH cap, so a forged marker fits
// inside a single keyword/tag string just as easily as inside Content.
func wrapUntrustedAtom(a atom.Atom) atom.Atom {
	out := a
	out.Content = clipSafe(a.Content, atomContentMaxRunes)
	if len(a.Keywords) > 0 {
		out.Keywords = clipSafeSlice(a.Keywords, atomKeywordMaxRunes)
	}
	if len(a.Tags) > 0 {
		out.Tags = clipSafeSlice(a.Tags, atomKeywordMaxRunes)
	}
	return out
}

// wrapUntrustedAtoms maps wrapUntrustedAtom over a slice, preserving element
// order — mirrors wrapUntrustedDecisions.
func wrapUntrustedAtoms(atoms []atom.Atom) []atom.Atom {
	out := make([]atom.Atom, len(atoms))
	for i := range atoms {
		out[i] = wrapUntrustedAtom(atoms[i])
	}
	return out
}

// clipSafeSlice maps clipSafe over every element of ss, preserving order.
func clipSafeSlice(ss []string, maxRunes int) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = clipSafe(s, maxRunes)
	}
	return out
}

func (s *Server) registerAtomTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool(
		"promote_atom_to_knowledge",
		mcp.WithDescription(
			fmt.Sprintf(
				"Manually promote a consolidated memory atom into a pending knowledge proposal. "+
					"The atom must have content >= %d chars and at least %d tags. "+
					"The proposal is created with proposed_by='atom_bridge' and requires user "+
					"confirmation via confirm_proposal before becoming a real knowledge item. "+
					"Deduplication happens at confirm time (similarity threshold 0.88).",
				atom.PromotionMinContentRunes, atom.PromotionMinTags,
			),
		),
		mcp.WithString("atom_id",
			mcp.Description("UUID of the atom to promote"),
			mcp.Required()),
	), s.handlePromoteAtomToKnowledge)

	ms.AddTool(mcp.NewTool(
		"traverse_atoms",
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

	ms.AddTool(mcp.NewTool(
		"search_atoms",
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
	startID, errResult := requireUUIDArg(args, "start_atom_id", "invalid start_atom_id UUID")
	if errResult != nil {
		return errResult, nil
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
	result.Atoms = wrapUntrustedAtoms(result.Atoms)
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
	return jsonText(wrapUntrustedAtoms(atoms))
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
	// Scrub credentials from skill text before sending to the Haiku API
	// (backend-security-design.md §3.1 — raw LLM tool input redaction).
	text = redact.ForLLM(text)
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
			continue
		}
		if sErr := store.SetDigestStatus(ctx, a.ID, atom.DigestStatusDone, ""); sErr != nil {
			slog.Warn("atomize: set digest status done failed", "atom_id", a.ID, "err", sErr)
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
//
// s.atomizeFn is used instead of calling atomizeAndPersist directly so tests
// can inject a fake without changing production behaviour.
func (s *Server) launchAtomize(parentTable string, parentID uuid.UUID, text string) {
	if s.atomizer == nil || s.atom == nil {
		return
	}
	fn := s.atomizeFn
	if fn == nil {
		fn = atomizeAndPersist
	}
	go func(pid uuid.UUID, t string) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("launchAtomize: recovered panic", "parent_id", pid, "panic", r)
			}
		}()
		select {
		case s.atomizeSem <- struct{}{}:
			defer func() { <-s.atomizeSem }()
		default:
			slog.Warn("launchAtomize: concurrency limit reached, skipping", "parent_id", pid)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		fn(ctx, s.atomizer, s.atom, s.workspaceUUID(), parentTable, pid, t)
	}(parentID, text)
}

// handlePromoteAtomToKnowledge manually promotes a single consolidated atom
// to a pending knowledge proposal with proposed_by='atom_bridge'.
// The tool applies the same quality gate as the weekly atom-bridge cron job:
// content length >= 80 chars AND len(tags) >= 2.
// Deduplication is delegated to AddItem's similarity check at confirm_proposal time.
func (s *Server) handlePromoteAtomToKnowledge(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	atomID, errResult := requireUUIDArg(args, "atom_id", "invalid atom_id UUID")
	if errResult != nil {
		return errResult, nil
	}
	if s.atom == nil {
		return mcp.NewToolResultError("atom store not available"), nil
	}
	if s.proposal == nil {
		return mcp.NewToolResultError("proposal store not available"), nil
	}
	a, errMsg := s.fetchAtomForPromotion(ctx, atomID)
	if errMsg != "" {
		return mcp.NewToolResultError(errMsg), nil
	}
	return s.promoteSingleAtom(ctx, atomID, a)
}

// fetchAtomForPromotion retrieves an atom by ID using Traverse (depth=1)
// and verifies that its digest_status is atom.DigestStatusConsolidated
// before returning it. Only consolidated atoms may be promoted — atoms in
// any other status (pending, done, failed, promoted) are rejected with a
// clear error message.
// Returns the atom and an error message (non-empty = failure).
func (s *Server) fetchAtomForPromotion(ctx context.Context, atomID uuid.UUID) (atom.Atom, string) {
	result, err := s.atom.Traverse(ctx, atomID, 1)
	if err != nil {
		return atom.Atom{}, fmt.Sprintf("looking up atom: %v", err)
	}
	if result == nil || len(result.Atoms) == 0 {
		return atom.Atom{}, fmt.Sprintf("atom %s not found", atomID)
	}
	a := result.Atoms[0]
	// Guard: nil DigestStatus or any status other than consolidated is rejected.
	// The scheduler's atom-bridge job only promotes consolidated atoms; this MCP
	// tool must apply the same vetting (security — prevents promoting unreviewed atoms).
	if a.DigestStatus == nil || *a.DigestStatus != atom.DigestStatusConsolidated {
		var statusStr string
		if a.DigestStatus == nil {
			statusStr = "<nil>"
		} else {
			statusStr = *a.DigestStatus
		}
		return atom.Atom{}, fmt.Sprintf(
			"atom %s has digest_status=%q; only %q atoms may be promoted to knowledge",
			atomID, statusStr, atom.DigestStatusConsolidated,
		)
	}
	return a, ""
}

// promoteSingleAtom validates quality gate and creates a knowledge proposal.
// The eligibility check (content length + tag count) is shared with the
// weekly atom-bridge cron job (internal/scheduler/atom_bridge.go) via
// atom.CheckPromotionEligibility so the manual and cron promotion paths
// cannot silently diverge (G6 decision 8f25c58d).
func (s *Server) promoteSingleAtom(ctx context.Context, atomID uuid.UUID, a atom.Atom) (*mcp.CallToolResult, error) {
	elig := atom.CheckPromotionEligibility(a.Content, a.Tags)
	if !elig.ContentOK {
		return mcp.NewToolResultError(
			fmt.Sprintf("atom content too short (%d runes); minimum is %d runes for promotion", elig.ContentRunes, atom.PromotionMinContentRunes),
		), nil
	}
	if !elig.TagsOK {
		return mcp.NewToolResultError(
			fmt.Sprintf("atom has %d tag(s); minimum is %d tags for promotion", elig.TagCount, atom.PromotionMinTags),
		), nil
	}
	// Rune-aware title truncation prevents panic and invalid UTF-8 on CJK/emoji.
	title := a.Content
	if runes := []rune(title); len(runes) > 80 {
		title = string(runes[:80])
	}
	payload, marshalErr := json.Marshal(proposal.KnowledgePayload{
		Title:   title,
		Content: a.Content,
		Tags:    a.Tags,
	})
	if marshalErr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("building proposal payload: %v", marshalErr)), nil
	}
	prop, createErr := s.proposal.Create(ctx, proposal.CreateParams{
		WorkspaceID: s.workspaceUUID(),
		Type:        proposal.TypeKnowledge,
		Payload:     payload,
		ProposedBy:  "atom_bridge",
	})
	if createErr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("creating knowledge proposal: %v", createErr)), nil
	}
	if setErr := s.atom.SetDigestStatus(ctx, atomID, atom.DigestStatusPromoted, ""); setErr != nil {
		slog.Warn("promote_atom_to_knowledge: setting promoted status failed",
			"atom_id", atomID, "err", setErr)
	}
	return jsonText(map[string]any{
		"proposal_id": prop.ID,
		"atom_id":     atomID,
		"status":      "pending",
		"message":     "atom promoted to pending knowledge proposal; confirm via confirm_proposal",
	})
}
