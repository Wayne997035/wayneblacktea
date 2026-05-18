// Package mcp — tools_reconcile.go exposes the reconcile_merged_prs MCP tool.
// Closes the "PR merged but task still pending" gap (sprint
// feature/gtd-enforce-server-side GTD-fix 9/12).
//
// Same payload semantics as the HTTP /api/tasks/reconcile-merged-prs endpoint
// (handler/reconcile_handler.go). Kept structurally distinct so MCP and HTTP
// surfaces can evolve independently — duplication is intentional.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/completioncandidate"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/sanitize"
	"github.com/Wayne997035/wayneblacktea/internal/validator"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
	mcpsrv "github.com/mark3labs/mcp-go/server"
)

// reconcileMCPMaxEntries mirrors the HTTP handler cap. Kept separate so we
// don't have to import the handler package (cycle risk).
const reconcileMCPMaxEntries = 200

// reconcileMCPMaxStringField mirrors the HTTP handler cap.
const reconcileMCPMaxStringField = 4 * 1024

// registerReconcileTools wires reconcile_merged_prs into the MCP server.
func (s *Server) registerReconcileTools(ms *mcpsrv.MCPServer) {
	ms.AddTool(mcpmsg.NewTool("reconcile_merged_prs",
		mcpmsg.WithDescription(
			"Accepts a Claude-supplied list of recently-merged PRs and auto-closes "+
				"every pending/in_progress task whose pr_url or branch_name matches. "+
				"Idempotent: a second call with the same payload returns 0 changes. "+
				"Ambiguous branch_name matches (>1 task on same branch) auto-apply the "+
				"most-recent task and surface siblings as completion_candidates "+
				"(status='pending') for manual resolution. "+
				"Payload: {\"merged_prs\":[{\"url\":\"...\",\"head_ref\":\"...\","+
				"\"merged_at\":\"RFC3339\",\"title\":\"...\",\"body\":\"...\","+
				"\"repo\":\"owner/repo\"}, ...]}",
		),
		mcpmsg.WithString("payload",
			mcpmsg.Description("JSON string of {\"merged_prs\":[...]}. Required. "+
				"Same shape as POST /api/tasks/reconcile-merged-prs."),
			mcpmsg.Required()),
	), s.handleReconcileMergedPRs)
}

// reconcileMCPPayload is the parsed payload string.
type reconcileMCPPayload struct {
	MergedPRs []reconcileMCPMergedPR `json:"merged_prs"`
}

// reconcileMCPMergedPR mirrors the HTTP handler shape — kept local so MCP can
// own its own validation messages.
type reconcileMCPMergedPR struct {
	URL      string `json:"url"`
	HeadRef  string `json:"head_ref"`
	MergedAt string `json:"merged_at"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Repo     string `json:"repo"`
}

// reconcileMCPResponse is the MCP response body (JSON text-result).
type reconcileMCPResponse struct {
	Matches         []reconcileMCPMatch     `json:"matches"`
	Ambiguous       []reconcileMCPAmbiguous `json:"ambiguous"`
	Applied         int                     `json:"applied"`
	NoMatch         int                     `json:"no_match"`
	CandidateWrites int                     `json:"candidate_writes"`
}

type reconcileMCPMatch struct {
	TaskID    string `json:"task_id"`
	Reason    string `json:"reason"`
	PRUrl     string `json:"pr_url"`
	PRHeadRef string `json:"pr_head_ref"`
}

type reconcileMCPAmbiguous struct {
	TaskID    string `json:"task_id"`
	Reason    string `json:"reason"`
	PRUrl     string `json:"pr_url"`
	PRHeadRef string `json:"pr_head_ref"`
}

// handleReconcileMergedPRs implements reconcile_merged_prs.
func (s *Server) handleReconcileMergedPRs(
	ctx context.Context,
	req mcpmsg.CallToolRequest,
) (*mcpmsg.CallToolResult, error) {
	args := req.GetArguments()
	raw := stringArg(args, "payload")
	if raw == "" {
		return mcpmsg.NewToolResultError("payload is required"), nil
	}

	var p reconcileMCPPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return mcpmsg.NewToolResultError("invalid payload JSON: " + err.Error()), nil
	}
	if len(p.MergedPRs) == 0 {
		return jsonText(reconcileMCPResponse{
			Matches:   []reconcileMCPMatch{},
			Ambiguous: []reconcileMCPAmbiguous{},
		})
	}
	if len(p.MergedPRs) > reconcileMCPMaxEntries {
		return mcpmsg.NewToolResultError(
			fmt.Sprintf("merged_prs exceeds %d entries per call", reconcileMCPMaxEntries)), nil
	}

	prs, msg := reconcileMCPValidate(p.MergedPRs)
	if msg != "" {
		return mcpmsg.NewToolResultError(msg), nil
	}

	result, err := gtd.MatchMergedPRs(ctx, s.gtd, prs)
	if err != nil {
		return mcpmsg.NewToolResultError(fmt.Sprintf("match: %v", err)), nil
	}

	applied, err := s.gtd.BatchCompleteTasksByPRMatch(ctx, result.Matches)
	if err != nil {
		return mcpmsg.NewToolResultError(fmt.Sprintf("batch complete: %v", err)), nil
	}

	candidateWrites := 0
	if cs := s.reconcileCandidateStore(); cs != nil {
		for _, m := range result.Matches {
			if werr := cs.WriteAutoApplied(ctx, m.TaskID,
				[]string{m.PRUrl}, m.PRUrl); werr != nil {
				// Same policy as HTTP path — log via tool error trail only on
				// the final result; mid-loop failures are surfaced via
				// candidate_writes < applied so callers can detect drift.
				continue
			}
			candidateWrites++
		}
	}

	matchOut := make([]reconcileMCPMatch, 0, len(result.Matches))
	for _, m := range result.Matches {
		matchOut = append(matchOut, reconcileMCPMatch{
			TaskID: m.TaskID.String(), Reason: string(m.Reason),
			PRUrl: m.PRUrl, PRHeadRef: m.PRHeadRef,
		})
	}
	ambigOut := make([]reconcileMCPAmbiguous, 0, len(result.Ambiguous))
	for _, a := range result.Ambiguous {
		ambigOut = append(ambigOut, reconcileMCPAmbiguous{
			TaskID: a.TaskID.String(), Reason: string(a.Reason),
			PRUrl: a.PRUrl, PRHeadRef: a.PRHeadRef,
		})
	}

	return jsonText(reconcileMCPResponse{
		Matches:         matchOut,
		Ambiguous:       ambigOut,
		Applied:         applied,
		NoMatch:         result.NoMatch,
		CandidateWrites: candidateWrites,
	})
}

// reconcileMCPValidate is the MCP twin of validateAndConvertMergedPRs in
// the HTTP handler. Kept separate so MCP error messages can evolve independently.
func reconcileMCPValidate(in []reconcileMCPMergedPR) ([]gtd.MergedPR, string) {
	out := make([]gtd.MergedPR, 0, len(in))
	for _, e := range in {
		if e.URL == "" {
			return nil, "merged_prs[i].url is required"
		}
		if !validator.GitHubPRURLRe.MatchString(e.URL) {
			return nil, "merged_prs[i].url must be a valid GitHub PR URL"
		}
		if e.HeadRef == "" {
			return nil, "merged_prs[i].head_ref is required"
		}
		if reconcileMCPHasControlChars(e.HeadRef) {
			return nil, "merged_prs[i].head_ref must not contain control characters"
		}
		for _, p := range []struct{ name, val string }{
			{"url", e.URL},
			{"head_ref", e.HeadRef},
			{"title", e.Title},
			{"body", e.Body},
			{"repo", e.Repo},
		} {
			if len([]rune(p.val)) > reconcileMCPMaxStringField {
				return nil, "merged_prs[i]." + p.name + " exceeds 4 KiB"
			}
		}

		var merged time.Time
		if mt := strings.TrimSpace(e.MergedAt); mt != "" {
			if t, perr := time.Parse(time.RFC3339Nano, mt); perr == nil {
				merged = t
			} else if t, perr2 := time.Parse(time.RFC3339, mt); perr2 == nil {
				merged = t
			} else {
				return nil, "merged_prs[i].merged_at must be RFC3339"
			}
		}

		out = append(out, gtd.MergedPR{
			URL:      strings.TrimSpace(e.URL),
			HeadRef:  e.HeadRef,
			MergedAt: merged,
			Title:    sanitize.Notes(e.Title),
			Body:     sanitize.Notes(e.Body),
			Repo:     sanitize.Notes(e.Repo),
		})
	}
	return out, ""
}

func reconcileMCPHasControlChars(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7F {
			return true
		}
	}
	return false
}

// reconcileCandidateStore returns the server's completion candidate store
// when the live store actually implements the full completioncandidate.Store
// interface (PG or SQLite). Returns nil when the feature is disabled or when
// the wired store is a narrowed fake (e.g. dashboard test stub) that doesn't
// expose WriteAutoApplied.
func (s *Server) reconcileCandidateStore() completioncandidate.Store {
	if s.completionCandidates == nil {
		return nil
	}
	if cs, ok := s.completionCandidates.(completioncandidate.Store); ok {
		return cs
	}
	return nil
}
