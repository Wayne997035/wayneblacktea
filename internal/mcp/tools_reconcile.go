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
	"log/slog"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/completioncandidate"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/mergedprs"
	"github.com/Wayne997035/wayneblacktea/internal/sanitize"
	"github.com/Wayne997035/wayneblacktea/internal/validator"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	mcpsrv "github.com/mark3labs/mcp-go/server"
)

// reconcileMCPMaxEntries mirrors the HTTP handler cap. Kept separate so we
// don't have to import the handler package (cycle risk).
const reconcileMCPMaxEntries = 200

// reconcileMCPMaxStringField mirrors the HTTP handler cap.
const reconcileMCPMaxStringField = 4 * 1024

// registerReconcileTools wires reconcile_merged_prs into the MCP server.
func (s *Server) registerReconcileTools(ms *mcpsrv.MCPServer) {
	ms.AddTool(mcp.NewTool(
		"reconcile_merged_prs",
		mcp.WithDescription(
			"Accepts a Claude-supplied list of recently-merged PRs and matches them "+
				"against pending/in_progress tasks by pr_url or branch_name. TWO-STEP: "+
				"the first call (confirm absent/false) only PREVIEWS matches — returns "+
				"{matches, ambiguous, no_match, reconcile_token, expires_at} — and does "+
				"NOT complete any tasks. The second call MUST include confirm=true and "+
				"the reconcile_token from the first call to actually apply the "+
				"completions computed during preview; on the second call any resent "+
				"payload is ignored entirely — only the exact match-set computed during "+
				"preview is applied. Tokens expire after 60s. Idempotent: a preview for "+
				"an already-applied batch shows those tasks in no_match. Ambiguous "+
				"branch_name matches (>1 task on same branch) auto-apply the "+
				"most-recent task; siblings are surfaced ONLY in this call's own "+
				"'ambiguous' response field for manual resolution — they are NOT "+
				"persisted as completion_candidates rows, so a client that needs to "+
				"act on them later must capture this response. "+
				"Payload: {\"merged_prs\":[{\"url\":\"...\",\"head_ref\":\"...\","+
				"\"merged_at\":\"RFC3339\",\"title\":\"...\",\"body\":\"...\","+
				"\"repo\":\"owner/repo\"}, ...]}",
		),
		mcp.WithString("payload",
			mcp.Description("JSON string of {\"merged_prs\":[...]}. Required on the "+
				"preview call (confirm absent/false); ignored entirely when confirm=true. "+
				"Same shape as POST /api/tasks/reconcile-merged-prs.")),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true on the second call to apply the completions computed during preview")),
		mcp.WithString("reconcile_token",
			mcp.Description("Token returned by the preview call; required when confirm=true")),
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

// reconcileMatchesOut converts gtd.Match values into the MCP-JSON shape.
// Shared by the preview response (matches computed just now) and the confirm
// response (matches echoed back from the stored reconcileConfirmation).
//
// [F160-01/02] PRUrl is regex-shape-constrained at input parsing
// (reconcileMCPValidate: validator.GitHubPRURLRe), which forecloses forged
// marker text on its own. PRHeadRef is only screened for control characters
// there (reconcileMCPHasControlChars) — no newlines, but the marker text
// itself is single-line, so a caller-supplied head_ref could still smuggle
// it verbatim. neutralizeBoundaryMarkers here matches the same defence-in-
// depth judgement neutralizeSessionMetadataFields (tools_worksession.go)
// already applies to its own single-line-enforced fields.
func reconcileMatchesOut(matches []gtd.Match) []reconcileMCPMatch {
	out := make([]reconcileMCPMatch, 0, len(matches))
	for _, m := range matches {
		out = append(out, reconcileMCPMatch{
			TaskID: m.TaskID.String(), Reason: string(m.Reason),
			PRUrl: m.PRUrl, PRHeadRef: neutralizeBoundaryMarkers(m.PRHeadRef),
		})
	}
	return out
}

// reconcileAmbiguousOut converts gtd.Ambiguous values into the MCP-JSON
// shape. See reconcileMatchesOut's doc comment for PRHeadRef's treatment.
func reconcileAmbiguousOut(ambig []gtd.Ambiguous) []reconcileMCPAmbiguous {
	out := make([]reconcileMCPAmbiguous, 0, len(ambig))
	for _, a := range ambig {
		out = append(out, reconcileMCPAmbiguous{
			TaskID: a.TaskID.String(), Reason: string(a.Reason),
			PRUrl: a.PRUrl, PRHeadRef: neutralizeBoundaryMarkers(a.PRHeadRef),
		})
	}
	return out
}

// handleReconcileMergedPRs implements the reconcile_merged_prs 2-step confirm
// flow (mirrors handleDeleteTask, tools_gtd.go:1097-1181). confirm
// absent/false dispatches to the preview branch (read-only match computation,
// no completion); confirm=true dispatches to the confirm branch (applies the
// EXACT match-set computed during a prior preview call, identified by a
// server-issued reconcile_token — never a resent payload).
//
// This closes a Least-Agency gap: a prompt-injected/fabricated "PR merged"
// claim can no longer silently complete real tasks in a single tool call.
func (s *Server) handleReconcileMergedPRs(
	ctx context.Context,
	req mcp.CallToolRequest,
) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	if boolArg(args, "confirm") {
		return s.handleReconcileMergedPRsConfirm(ctx, args)
	}
	return s.handleReconcileMergedPRsPreview(ctx, args)
}

// handleReconcileMergedPRsPreview implements step 1: validate + persist
// observed PRs + compute matches/ambiguous/fuzzy-candidates, but NEVER call
// BatchCompleteTasksByPRMatch or WriteAutoApplied. Issues a one-time
// reconcile_token bound to the exact result computed here.
func (s *Server) handleReconcileMergedPRsPreview(
	ctx context.Context,
	args map[string]any,
) (*mcp.CallToolResult, error) {
	raw := stringArg(args, "payload")
	if raw == "" {
		return mcp.NewToolResultError("payload is required"), nil
	}

	var p reconcileMCPPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return inputErrorResult("invalid payload JSON", err), nil
	}
	// Short-circuit BEFORE the prune+cap+token-issuance block below. Without
	// this check, a completely empty merged_prs:[] payload still ran the full
	// preview flow and unconditionally issued a reconcile_token — an
	// attacker/prompt-injected agent could call this maxPendingReconciles
	// (256) times at zero real cost to fill s.reconcileTokens, causing every
	// legitimate preview call to fail with "too many pending reconciliations
	// in flight" (a rolling, near-zero-cost functional DoS). Rejecting empty
	// payloads here means they never consume token-cap budget.
	if len(p.MergedPRs) == 0 {
		return mcp.NewToolResultError("merged_prs must contain at least 1 entry"), nil
	}
	if len(p.MergedPRs) > reconcileMCPMaxEntries {
		return mcp.NewToolResultError(
			fmt.Sprintf("merged_prs exceeds %d entries per call", reconcileMCPMaxEntries),
		), nil
	}

	prs, msg := reconcileMCPValidate(p.MergedPRs)
	if msg != "" {
		return mcp.NewToolResultError(msg), nil
	}

	// Persist every observed PR into merged_prs_observed (Phase 2 audit
	// trail). Idempotent on the url UNIQUE INDEX. Persistence failure does
	// NOT fail the tool call — log + continue so the exact-match path still
	// runs end-to-end.
	reconcileMCPPersistObserved(ctx, s.mergedPRsStore, prs)

	result, err := gtd.MatchMergedPRs(ctx, s.gtd, prs)
	if err != nil {
		return storeErrorResult("match", err), nil
	}

	// BatchCompleteTasksByPRMatch and WriteAutoApplied are deliberately NOT
	// called here — that is the entire point of the split. They only run in
	// handleReconcileMergedPRsConfirm, against the token-bound result stored
	// below.
	candidateWrites := 0
	if cs := s.reconcileCandidateStore(); cs != nil {
		// Clarification 3: exclude every task that was just exactly matched
		// (result.Matches) from fuzzy-candidate consideration for this call.
		// Under the old (single-call) flow, BatchCompleteTasksByPRMatch ran
		// BEFORE the fuzzy scan, so an exactly-matched task was already
		// completed (and had pr_url set) by the time isFuzzyEligible ran,
		// naturally excluding it. Under this split, the fuzzy write now runs
		// BEFORE completion (during preview), so without this explicit
		// exclusion an exactly-matched task that also happens to be
		// fuzzy-eligible would spuriously get a completion_candidates row it
		// never would have gotten before. This keeps the observable
		// fuzzy-candidate behavior for exactly-matched tasks identical to
		// today regardless of the new call ordering.
		excludeExactMatches := make(map[uuid.UUID]bool, len(result.Matches))
		for _, m := range result.Matches {
			excludeExactMatches[m.TaskID] = true
		}
		candidateWrites = reconcileMCPWriteFuzzyCandidates(ctx, s.gtd, cs, prs, excludeExactMatches)
	}

	// Round-3 Finding 1: gate token issuance on the MATCH RESULT, not merely
	// on payload non-emptiness. The len(p.MergedPRs)==0 short-circuit above
	// only rejects a trivially-empty payload; it does nothing against a
	// syntactically-valid, non-empty, but semantically-bogus payload (e.g. a
	// well-formed GitHub PR URL / branch name that matches ZERO real tasks).
	// Such a payload previously still fell through to the prune+cap+token
	// block below and unconditionally consumed one of the maxPendingReconciles
	// (256) cap slots — looping 257 such minimal-cost, zero-real-knowledge
	// payloads filled the entire cap and denied every legitimate caller.
	// Crafting a payload that instead produces a real Match or Ambiguous hit
	// requires knowing an actual branch name / PR URL already linked to a
	// real task in this workspace — a materially higher bar than the
	// zero-knowledge bypass this closes. "Nothing to confirm" calls are
	// answered directly here (no reconcile_token, no cap-slot consumption,
	// no error — this is a normal, successful outcome) instead of falling
	// through to token issuance.
	if len(result.Matches) == 0 && len(result.Ambiguous) == 0 {
		return jsonText(map[string]any{
			"status":           "no_match",
			"matches":          []reconcileMCPMatch{},
			"ambiguous":        []reconcileMCPAmbiguous{},
			"applied":          0,
			"no_match":         result.NoMatch,
			"candidate_writes": candidateWrites,
			"message": "No merged_prs entry matched a pending/in_progress task; nothing to " +
				"confirm. No reconcile_token issued.",
		})
	}

	// Prune expired tokens and cap concurrent pending reconciliations (mirrors
	// tools_gtd.go:1128-1141's delete_task prune+cap logic). Reached only when
	// there is at least one Match or Ambiguous entry — see the gate above.
	var count int
	s.reconcileTokens.Range(func(k, v any) bool {
		rec, ok := v.(reconcileConfirmation)
		if !ok || s.now().After(rec.expiresAt) {
			s.reconcileTokens.Delete(k)
		} else {
			count++
		}
		return true
	})
	if count >= maxPendingReconciles {
		return mcp.NewToolResultError("too many pending reconciliations in flight; retry later"), nil
	}

	token := uuid.NewString()
	expires := s.now().Add(reconcileTokenTTL)
	s.reconcileTokens.Store(token, reconcileConfirmation{
		matches:   result.Matches,
		ambiguous: result.Ambiguous,
		noMatch:   result.NoMatch,
		expiresAt: expires,
	})

	return jsonText(map[string]any{
		"status":           "confirmation_required",
		"matches":          reconcileMatchesOut(result.Matches),
		"ambiguous":        reconcileAmbiguousOut(result.Ambiguous),
		"applied":          0,
		"no_match":         result.NoMatch,
		"candidate_writes": candidateWrites,
		"reconcile_token":  token,
		"expires_at":       expires.UTC().Format(time.RFC3339),
		"message": "Call reconcile_merged_prs again with confirm=true and reconcile_token " +
			"to apply these completions. Token expires in 60s.",
	})
}

// handleReconcileMergedPRsConfirm implements step 2: apply the EXACT
// match-set computed and stored during a prior preview call. Any payload
// argument resent alongside confirm=true is never parsed, validated, or read
// — this forecloses a bait-and-switch where a token obtained from a small
// legitimate preview is replayed against a confirm call carrying a
// different/larger fabricated payload.
func (s *Server) handleReconcileMergedPRsConfirm(
	ctx context.Context,
	args map[string]any,
) (*mcp.CallToolResult, error) {
	token := stringArg(args, "reconcile_token")
	if token == "" {
		return mcp.NewToolResultError("reconcile_token is required when confirm=true"), nil
	}
	stored, ok := s.reconcileTokens.LoadAndDelete(token)
	if !ok {
		return mcp.NewToolResultError(
			"no pending reconciliation for this reconcile_token; call without confirm first to obtain matches and a token",
		), nil
	}
	rec, ok := stored.(reconcileConfirmation)
	if !ok {
		return mcp.NewToolResultError("internal: reconcile token state corrupted"), nil
	}
	if s.now().After(rec.expiresAt) {
		return mcp.NewToolResultError("reconcile_token expired; call without confirm to obtain a new token"), nil
	}

	// appliedIDs is keyed by TaskID for exactly the matches whose guarded
	// UPDATE (status IN ('pending','in_progress') at apply time) actually
	// flipped a row this call — see gtd.StoreIface.BatchCompleteTasksByPRMatch
	// for the contract. A match whose task drifted away from
	// pending/in_progress in the preview->confirm window (e.g. cancelled) is
	// absent from this map even though it is still present in rec.matches.
	appliedIDs, err := s.gtd.BatchCompleteTasksByPRMatch(ctx, rec.matches)
	if err != nil {
		return storeErrorResult("batch complete", err), nil
	}

	// Round-3 Finding 2: only write a completion_candidates 'auto_applied'
	// audit row for a match that appliedIDs confirms was GENUINELY applied
	// this call. Before this fix, the loop below ran unconditionally over
	// every rec.matches entry regardless of whether that task's guarded
	// UPDATE affected a row — so a task correctly protected by the TOCTOU
	// guard (e.g. cancelled between preview and confirm, tasks.status stays
	// 'cancelled') still got a persisted, FALSE completion_candidates row
	// claiming status='auto_applied'. That corrupts any dashboard/operator/
	// future-automation trusting completion_candidates as ground truth, and
	// the ON CONFLICT (task_id, reason) upsert would overwrite/close any
	// prior pending candidate state for that task. Filtering on appliedIDs
	// here closes that audit-trail integrity gap.
	candidateWrites := 0
	if cs := s.reconcileCandidateStore(); cs != nil {
		for _, m := range rec.matches {
			if !appliedIDs[m.TaskID] {
				continue
			}
			if werr := cs.WriteAutoApplied(ctx, m.TaskID,
				[]string{m.PRUrl}, m.PRUrl); werr != nil {
				// Same policy as the original single-call path — log via tool
				// error trail only on the final result; mid-loop failures are
				// surfaced via candidate_writes < applied so callers can
				// detect drift.
				continue
			}
			candidateWrites++
		}
	}

	return jsonText(map[string]any{
		"status":           "applied",
		"applied":          len(appliedIDs),
		"matches":          reconcileMatchesOut(rec.matches),
		"ambiguous":        reconcileAmbiguousOut(rec.ambiguous),
		"no_match":         rec.noMatch,
		"candidate_writes": candidateWrites,
	})
}

// reconcileMCPValidate is the MCP twin of validateAndConvertMergedPRs in
// the HTTP handler. Kept separate so MCP error messages can evolve independently.
//
// Validation per entry — kept in sync with the HTTP handler:
//   - url MUST match GitHubPRURLRe
//   - head_ref MUST be non-empty AND not contain control characters
//   - repo MUST be non-empty AND match owner/repo slug pattern (slug flows
//     downstream into `gh -R <slug>` argv invocations)
//   - each string field MUST NOT exceed reconcileMCPMaxStringField runes
//   - merged_at MUST parse as RFC3339 when provided
func reconcileMCPValidate(in []reconcileMCPMergedPR) ([]gtd.MergedPR, string) {
	out := make([]gtd.MergedPR, 0, len(in))
	for i, e := range in {
		if e.URL == "" {
			return nil, fmt.Sprintf("merged_prs[%d].url is required", i)
		}
		if !validator.GitHubPRURLRe.MatchString(e.URL) {
			return nil, fmt.Sprintf("merged_prs[%d].url must be a valid GitHub PR URL", i)
		}
		if e.HeadRef == "" {
			return nil, fmt.Sprintf("merged_prs[%d].head_ref is required", i)
		}
		if reconcileMCPHasControlChars(e.HeadRef) {
			return nil, fmt.Sprintf("merged_prs[%d].head_ref must not contain control characters", i)
		}
		if e.Repo == "" {
			return nil, fmt.Sprintf("merged_prs[%d].repo is required", i)
		}
		if !validator.RepoSlugRe.MatchString(e.Repo) {
			return nil, fmt.Sprintf("merged_prs[%d].repo must match owner/repo slug pattern", i)
		}
		for _, p := range []struct{ name, val string }{
			{"url", e.URL},
			{"head_ref", e.HeadRef},
			{"title", e.Title},
			{"body", e.Body},
			{"repo", e.Repo},
		} {
			if len([]rune(p.val)) > reconcileMCPMaxStringField {
				return nil, fmt.Sprintf("merged_prs[%d].%s exceeds 4 KiB", i, p.name)
			}
		}

		var merged time.Time
		if mt := strings.TrimSpace(e.MergedAt); mt != "" {
			if t, perr := time.Parse(time.RFC3339Nano, mt); perr == nil {
				merged = t
			} else if t, perr2 := time.Parse(time.RFC3339, mt); perr2 == nil {
				merged = t
			} else {
				return nil, fmt.Sprintf("merged_prs[%d].merged_at must be RFC3339", i)
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

// reconcileMCPCapBodyExcerpt truncates s at mergedprs.AuditBodyExcerptMaxLen
// runes (UTF-8 safe). Both HTTP and MCP surfaces share the same cap so the
// dashboard sees a consistent length budget; see internal/mergedprs/store.go
// for the canonical definition.
func reconcileMCPCapBodyExcerpt(s string) string {
	if s == "" {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= mergedprs.AuditBodyExcerptMaxLen {
		return s
	}
	return string(runes[:mergedprs.AuditBodyExcerptMaxLen])
}

// reconcileMCPPersistObserved upserts every PR into the merged_prs_observed
// table. nil store → no-op. Mirrors the HTTP handler's persistObservedPRs.
func reconcileMCPPersistObserved(
	ctx context.Context,
	store mergedprs.Store,
	prs []gtd.MergedPR,
) {
	if store == nil || len(prs) == 0 {
		return
	}
	for _, pr := range prs {
		err := store.Upsert(ctx, mergedprs.UpsertParams{
			Repo:        pr.Repo,
			URL:         pr.URL,
			HeadRef:     pr.HeadRef,
			Title:       pr.Title,
			BodyExcerpt: reconcileMCPCapBodyExcerpt(pr.Body),
			MergedAt:    pr.MergedAt,
		})
		if err != nil {
			slog.Warn("reconcile_mcp persistObservedPRs",
				"err", err, "url", pr.URL, "repo", pr.Repo)
		}
	}
}

// reconcileMCPWriteFuzzyCandidates runs the Phase 2 fuzzy matcher and writes
// each hit as a 'medium'-confidence completion_candidate (reason=
// pr_merged_fuzzy). Returns the number of candidates persisted. Mirrors the
// HTTP handler's writeFuzzyCandidates.
//
// excludeTaskIDs (Clarification 3, see call site in
// handleReconcileMergedPRsPreview) is the set of task IDs that were just
// exactly matched by gtd.MatchMergedPRs in this same call — any fuzzy hit for
// one of these tasks is skipped. isFuzzyEligible already excludes tasks with
// branch_name or pr_url set (which is how an exact match occurs in the first
// place), so this exclusion is normally redundant with that filter; it is
// kept as an explicit belt-and-braces guard so the observable fuzzy-candidate
// behavior for exactly-matched tasks stays identical to today regardless of
// future isFuzzyEligible changes or new exact-match mechanisms.
func reconcileMCPWriteFuzzyCandidates(
	ctx context.Context,
	gtdStore gtd.StoreIface,
	candStore completioncandidate.Store,
	prs []gtd.MergedPR,
	excludeTaskIDs map[uuid.UUID]bool,
) int {
	if candStore == nil || len(prs) == 0 {
		return 0
	}
	tasks, err := gtdStore.Tasks(ctx, nil)
	if err != nil {
		slog.Warn("reconcile_mcp writeFuzzyCandidates: load tasks failed", "err", err)
		return 0
	}
	matches := gtd.MatchPendingTasksFuzzy(prs, tasks)
	if len(matches) == 0 {
		return 0
	}
	wrote := 0
	for _, m := range matches {
		if excludeTaskIDs[m.TaskID] {
			continue
		}
		slog.Info(
			"reconcile_mcp fuzzy match",
			"task_id", m.TaskID,
			"pr_url", m.PRURL,
			"score", m.Score,
			"reason", m.Reason,
		)
		_, upErr := candStore.UpsertCandidate(ctx, completioncandidate.UpsertParams{
			TaskID:            m.TaskID,
			Reason:            completioncandidate.ReasonPRMerged,
			Confidence:        completioncandidate.ConfidenceMedium,
			EvidenceRefs:      []string{m.Reason, m.PRURL},
			SuggestedArtifact: m.PRURL,
		})
		if upErr != nil {
			slog.Warn("reconcile_mcp writeFuzzyCandidates: upsert failed",
				"err", upErr, "task_id", m.TaskID, "pr_url", m.PRURL)
			continue
		}
		wrote++
	}
	return wrote
}
