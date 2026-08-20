package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/playbook"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (s *Server) registerPlaybookTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool(
		"list_playbooks",
		mcp.WithDescription(
			"Returns procedural playbooks — generalized rules derived from past decisions. "+
				"Call BEFORE responding to any complex task to check if a matching rule exists. "+
				"Example: before architecting a new feature, call list_playbooks with context_keywords "+
				"to see if there is a relevant past pattern to follow.",
		),
		mcp.WithString("context_keywords",
			mcp.Description("Space-separated or comma-separated keywords to filter playbooks by relevance. Optional.")),
	), s.handleListPlaybooks)
}

// playbookTextMaxRunes bounds TriggerPattern/ActionTemplate on read — U13
// (2026-08-20-mcp-surface-spec.md). Playbooks are derived-rule text
// generalised from past decisions with no write-time neutralisation, so
// this is sized like wrapUntrustedTask's gtdBodyMaxRunes (tools_gtd.go):
// long-form by nature, not a short title.
const playbookTextMaxRunes = gtdBodyMaxRunes

// wrapUntrustedPlaybook returns a copy of p with TriggerPattern/ActionTemplate
// clipSafe'd (tools_context.go). nil in, nil out.
func wrapUntrustedPlaybook(p *playbook.Playbook) *playbook.Playbook {
	if p == nil {
		return nil
	}
	out := *p
	out.TriggerPattern = clipSafe(p.TriggerPattern, playbookTextMaxRunes)
	out.ActionTemplate = clipSafe(p.ActionTemplate, playbookTextMaxRunes)
	return &out
}

// wrapUntrustedPlaybooks maps wrapUntrustedPlaybook over a slice, always
// non-nil (list_playbooks already guards nil -> [] at its own call site
// before this runs).
func wrapUntrustedPlaybooks(playbooks []*playbook.Playbook) []*playbook.Playbook {
	out := make([]*playbook.Playbook, len(playbooks))
	for i, p := range playbooks {
		out[i] = wrapUntrustedPlaybook(p)
	}
	return out
}

func (s *Server) handleListPlaybooks(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	raw := stringArg(args, "context_keywords")

	var keywords []string
	if raw != "" {
		// Split on comma or space; filter empties.
		parts := strings.FieldsFunc(raw, func(r rune) bool {
			return r == ',' || r == ' '
		})
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				keywords = append(keywords, p)
			}
		}
	}

	const maxKeywords = 10
	const maxKeywordLen = 100
	if len(keywords) > maxKeywords {
		keywords = keywords[:maxKeywords]
	}
	for i, kw := range keywords {
		if len([]rune(kw)) > maxKeywordLen {
			keywords[i] = string([]rune(kw)[:maxKeywordLen])
		}
	}

	params := playbook.ListParams{
		ContextKeywords: keywords,
	}
	if wsID := s.workspaceUUID(); wsID != nil {
		params.WorkspaceID = wsID
	}

	playbooks, err := s.playbook.List(ctx, params)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("listing playbooks: %v", err)), nil
	}
	if playbooks == nil {
		playbooks = []*playbook.Playbook{}
	}
	return jsonText(wrapUntrustedPlaybooks(playbooks))
}
