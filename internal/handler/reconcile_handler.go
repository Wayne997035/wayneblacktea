// Package handler — reconcile_handler.go exposes POST /api/tasks/reconcile-merged-prs.
// Closes the "PR merged but task still pending" gap (sprint
// feature/gtd-enforce-server-side GTD-fix 9/12).
//
// Wire contract:
//
//	POST /api/tasks/reconcile-merged-prs
//	Content-Type: application/json
//	Body: {
//	  "merged_prs": [
//	    {
//	      "url": "https://github.com/owner/repo/pull/123",
//	      "head_ref": "feature/x",
//	      "merged_at": "2026-05-18T12:00:00Z",
//	      "title": "feat: x",
//	      "body": "Closes #42",
//	      "repo": "Wayne997035/wayneblacktea"
//	    }
//	  ]
//	}
//
//	200 OK:
//	{
//	  "matches":     [{"task_id":"...", "reason":"...", "pr_url":"..."}],
//	  "ambiguous":   [{"task_id":"...", "reason":"...", "pr_url":"..."}],
//	  "applied":    N,
//	  "no_match":   N,
//	  "candidate_writes": N
//	}
package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/completioncandidate"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/mergedprs"
	"github.com/Wayne997035/wayneblacktea/internal/sanitize"
	"github.com/Wayne997035/wayneblacktea/internal/validator"
	"github.com/labstack/echo/v4"
)

// reconcileMaxEntries caps the number of PRs accepted per call. Keeps DoS risk
// low while still allowing a "weekly catch-up" reconcile to land in one shot.
const reconcileMaxEntries = 200

// reconcileMaxBodyBytes caps the entire request body. 4 MiB matches the Echo
// global limiter but applied explicitly here so any future relaxation of the
// global doesn't accidentally widen this endpoint.
const reconcileMaxBodyBytes = 4 * 1024 * 1024

// reconcileMaxStringField caps individual string fields (title, body, head_ref,
// repo). 4 KiB per field is generous for normal PR bodies and prevents a single
// "novel-length" PR from blowing past the request budget.
const reconcileMaxStringField = 4 * 1024

// ReconcileHandler exposes POST /api/tasks/reconcile-merged-prs.
type ReconcileHandler struct {
	gtd       gtd.StoreIface
	candidate completioncandidate.Store
	mergedPRs mergedprs.Store
}

// NewReconcileHandler wires the GTD store + completion-candidate store into
// the handler. candidate MAY be nil — in that case auto-close still happens
// and the candidate-write step is a no-op (the GTD store carries the audit
// via activity_log, so the candidate row is supplementary).
//
// The merged_prs_observed persistence (Phase 2 fuzzy-match audit trail) is
// wired separately via WithMergedPRsStore so existing tests that only need
// the exact-match path don't have to construct that store. Both stores can
// be nil — exact-match continues to work; only fuzzy candidates require the
// candidate store, and the audit trail requires the merged_prs store.
func NewReconcileHandler(g gtd.StoreIface, c completioncandidate.Store) *ReconcileHandler {
	return &ReconcileHandler{gtd: g, candidate: c}
}

// WithMergedPRsStore wires the merged_prs_observed store into the handler so
// every incoming PR is persisted (idempotent on URL conflict) and so the
// Phase 2 fuzzy candidate detection can surface null-linkage matches.
// Returns the receiver to enable fluent wiring at construction time.
func (h *ReconcileHandler) WithMergedPRsStore(m mergedprs.Store) *ReconcileHandler {
	h.mergedPRs = m
	return h
}

// reconcileRequest is the JSON body.
type reconcileRequest struct {
	MergedPRs []reconcileMergedPR `json:"merged_prs"`
}

// reconcileMergedPR mirrors gtd.MergedPR but uses string for merged_at so we
// can return a clean 400 on parse failure (vs Echo's default opaque error).
type reconcileMergedPR struct {
	URL      string `json:"url"`
	HeadRef  string `json:"head_ref"`
	MergedAt string `json:"merged_at"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Repo     string `json:"repo"`
}

// reconcileResponse is the JSON response.
type reconcileResponse struct {
	Matches         []matchSummary     `json:"matches"`
	Ambiguous       []ambiguousSummary `json:"ambiguous"`
	Applied         int                `json:"applied"`
	NoMatch         int                `json:"no_match"`
	CandidateWrites int                `json:"candidate_writes"`
}

type matchSummary struct {
	TaskID    string `json:"task_id"`
	Reason    string `json:"reason"`
	PRUrl     string `json:"pr_url"`
	PRHeadRef string `json:"pr_head_ref"`
}

type ambiguousSummary struct {
	TaskID    string `json:"task_id"`
	Reason    string `json:"reason"`
	PRUrl     string `json:"pr_url"`
	PRHeadRef string `json:"pr_head_ref"`
}

// Reconcile handles POST /api/tasks/reconcile-merged-prs.
func (h *ReconcileHandler) Reconcile(c echo.Context) error {
	body, err := io.ReadAll(io.LimitReader(c.Request().Body, reconcileMaxBodyBytes+1))
	if err != nil {
		return c.JSON(http.StatusBadRequest, errResp("failed to read request body"))
	}
	if len(body) > reconcileMaxBodyBytes {
		return c.JSON(http.StatusRequestEntityTooLarge,
			errResp("request body exceeds 4 MiB"))
	}

	var req reconcileRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid request body: "+err.Error()))
	}

	if len(req.MergedPRs) == 0 {
		return c.JSON(http.StatusOK, reconcileResponse{
			Matches:   []matchSummary{},
			Ambiguous: []ambiguousSummary{},
		})
	}
	if len(req.MergedPRs) > reconcileMaxEntries {
		return c.JSON(http.StatusRequestEntityTooLarge,
			errResp("merged_prs exceeds 200 entries per call"))
	}

	prs, msg := validateAndConvertMergedPRs(req.MergedPRs)
	if msg != "" {
		return c.JSON(http.StatusBadRequest, errResp(msg))
	}

	// Persist every observed PR into merged_prs_observed so the Phase 2
	// fuzzy detector + later replays can correlate them against null-linkage
	// tasks. Upsert is idempotent on the `url` UNIQUE INDEX (ON CONFLICT
	// refreshes observed_at). Persistence failure MUST NOT fail the request:
	// the exact-match path (and any in-batch fuzzy path below) still runs.
	persistObservedPRs(c.Request().Context(), h.mergedPRs, prs)

	result, err := gtd.MatchMergedPRs(c.Request().Context(), h.gtd, prs)
	if err != nil {
		c.Logger().Errorf("Reconcile match: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	applied, err := h.gtd.BatchCompleteTasksByPRMatch(c.Request().Context(), result.Matches)
	if err != nil {
		c.Logger().Errorf("Reconcile batch complete: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	// Audit-log the auto-close into completion_candidates (status=auto_applied).
	// nil candidate store → graceful skip (activity_log still carries audit).
	candidateWrites := 0
	if h.candidate != nil {
		for _, m := range result.Matches {
			if err := h.candidate.WriteAutoApplied(
				c.Request().Context(), m.TaskID,
				[]string{m.PRUrl}, m.PRUrl,
			); err != nil {
				// Log but do not fail the whole reconcile — the task is already
				// closed; the candidate row is bookkeeping.
				c.Logger().Errorf("Reconcile WriteAutoApplied task=%s: %v", m.TaskID, err)
				continue
			}
			candidateWrites++
		}
	}

	// Phase 2 (GTD-fix 10/12): fuzzy-match the input PRs against pending
	// tasks with NULL linkage. Each fuzzy hit becomes a 'medium'-confidence
	// completion_candidate — never auto-applied.
	candidateWrites += h.writeFuzzyCandidates(c.Request().Context(), prs)

	// Build response.
	matchOut := make([]matchSummary, 0, len(result.Matches))
	for _, m := range result.Matches {
		matchOut = append(matchOut, matchSummary{
			TaskID:    m.TaskID.String(),
			Reason:    string(m.Reason),
			PRUrl:     m.PRUrl,
			PRHeadRef: m.PRHeadRef,
		})
	}
	ambigOut := make([]ambiguousSummary, 0, len(result.Ambiguous))
	for _, a := range result.Ambiguous {
		ambigOut = append(ambigOut, ambiguousSummary{
			TaskID:    a.TaskID.String(),
			Reason:    string(a.Reason),
			PRUrl:     a.PRUrl,
			PRHeadRef: a.PRHeadRef,
		})
	}

	return c.JSON(http.StatusOK, reconcileResponse{
		Matches:         matchOut,
		Ambiguous:       ambigOut,
		Applied:         applied,
		NoMatch:         result.NoMatch,
		CandidateWrites: candidateWrites,
	})
}

// validateAndConvertMergedPRs runs the per-entry validation pipeline and
// returns either a clean []gtd.MergedPR or a user-facing error message.
//
// Validation per entry:
//   - url MUST match GitHubPRURLRe
//   - head_ref MUST be non-empty AND not contain control characters
//   - repo MUST be non-empty AND match owner/repo slug pattern (the slug
//     downstream flows into `gh -R <slug>` argv invocations so we reject
//     shell metas / whitespace / path traversal here at the boundary)
//   - each string field MUST NOT exceed reconcileMaxStringField runes
//   - merged_at MUST parse as RFC3339 when provided (empty allowed for fixtures)
func validateAndConvertMergedPRs(in []reconcileMergedPR) ([]gtd.MergedPR, string) {
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
		if hasReconcileControlChars(e.HeadRef) {
			return nil, fmt.Sprintf("merged_prs[%d].head_ref must not contain control characters", i)
		}
		if e.Repo == "" {
			return nil, fmt.Sprintf("merged_prs[%d].repo is required", i)
		}
		if !validator.RepoSlugRe.MatchString(e.Repo) {
			return nil, fmt.Sprintf("merged_prs[%d].repo must match owner/repo slug pattern", i)
		}
		if msg := checkReconcileFieldLen(e); msg != "" {
			return nil, msg
		}

		merged, msg := parseReconcileMergedAt(e.MergedAt)
		if msg != "" {
			return nil, msg
		}

		out = append(out, gtd.MergedPR{
			URL:      strings.TrimSpace(e.URL),
			HeadRef:  e.HeadRef, // exact case
			MergedAt: merged,
			Title:    sanitize.Notes(e.Title),
			Body:     sanitize.Notes(e.Body),
			Repo:     sanitize.Notes(e.Repo),
		})
	}
	return out, ""
}

// checkReconcileFieldLen returns a non-empty error message if any string field
// exceeds reconcileMaxStringField runes.
func checkReconcileFieldLen(e reconcileMergedPR) string {
	for _, p := range []struct{ name, val string }{
		{"url", e.URL},
		{"head_ref", e.HeadRef},
		{"title", e.Title},
		{"body", e.Body},
		{"repo", e.Repo},
	} {
		if len([]rune(p.val)) > reconcileMaxStringField {
			return "merged_prs[i]." + p.name + " exceeds 4 KiB"
		}
	}
	return ""
}

// parseReconcileMergedAt returns (parsedTime, errMsg). Empty input → zero time
// (allowed for fixtures that don't carry timing).
func parseReconcileMergedAt(s string) (time.Time, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, ""
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, ""
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, "merged_prs[i].merged_at must be RFC3339"
	}
	return t, ""
}

// hasReconcileControlChars returns true if s contains any character < 0x20
// (newlines, tabs, NUL) — branch refs must be single-line slugs.
func hasReconcileControlChars(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7F {
			return true
		}
	}
	return false
}

// persistObservedPRs upserts every PR into the merged_prs_observed table.
// nil store → no-op (handler is wired without the Phase 2 store). Each Upsert
// failure is logged at warn level; the loop continues so a single transient
// error doesn't block the remaining inserts. The body excerpt was already
// sanitised + length-capped by validateAndConvertMergedPRs/sanitiseBodyExcerpt
// upstream, but we re-cap defensively to keep the audit row size predictable.
func persistObservedPRs(ctx context.Context, store mergedprs.Store, prs []gtd.MergedPR) {
	if store == nil || len(prs) == 0 {
		return
	}
	for _, pr := range prs {
		err := store.Upsert(ctx, mergedprs.UpsertParams{
			Repo:        pr.Repo,
			URL:         pr.URL,
			HeadRef:     pr.HeadRef,
			Title:       pr.Title,
			BodyExcerpt: capBodyExcerptForAudit(pr.Body),
			MergedAt:    pr.MergedAt,
		})
		if err != nil {
			slog.Warn("reconcile persistObservedPRs",
				"err", err, "url", pr.URL, "repo", pr.Repo)
		}
	}
}

// capBodyExcerptForAudit truncates s at mergedprs.AuditBodyExcerptMaxLen runes
// (UTF-8 safe). Used by the merged_prs_observed audit-write path. The cap is
// owned by the persistence package (internal/mergedprs) so the HTTP and MCP
// surfaces share a single source of truth — the matcher's sanitiseBodyExcerpt
// cap in internal/gtd/reconcile.go intentionally mirrors the same number so
// the dashboard sees a consistent length budget.
func capBodyExcerptForAudit(s string) string {
	if s == "" {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= mergedprs.AuditBodyExcerptMaxLen {
		return s
	}
	return string(runes[:mergedprs.AuditBodyExcerptMaxLen])
}

// writeFuzzyCandidates runs the Phase 2 fuzzy matcher against all tasks and
// upserts any hits into completion_candidates with confidence='medium' and
// reason='pr_merged_fuzzy'. Returns the number of candidates successfully
// written. Skips silently when either candidate store or input PR list is
// empty. Every match is also slog.Info-logged with score so the threshold
// (matchThreshold=0.6) can be calibrated from real-world data later.
func (h *ReconcileHandler) writeFuzzyCandidates(
	ctx context.Context,
	prs []gtd.MergedPR,
) int {
	if h.candidate == nil || len(prs) == 0 {
		return 0
	}
	tasks, err := h.gtd.Tasks(ctx, nil)
	if err != nil {
		slog.Warn("reconcile writeFuzzyCandidates: load tasks failed", "err", err)
		return 0
	}
	matches := gtd.MatchPendingTasksFuzzy(prs, tasks)
	if len(matches) == 0 {
		return 0
	}
	wrote := 0
	for _, m := range matches {
		slog.Info("reconcile fuzzy match",
			"task_id", m.TaskID,
			"pr_url", m.PRURL,
			"score", m.Score,
			"reason", m.Reason,
		)
		_, upErr := h.candidate.UpsertCandidate(ctx, completioncandidate.UpsertParams{
			TaskID:            m.TaskID,
			Reason:            completioncandidate.ReasonPRMerged,
			Confidence:        completioncandidate.ConfidenceMedium,
			EvidenceRefs:      []string{m.Reason, m.PRURL},
			SuggestedArtifact: m.PRURL,
		})
		if upErr != nil {
			slog.Warn("reconcile writeFuzzyCandidates: upsert failed",
				"err", upErr, "task_id", m.TaskID, "pr_url", m.PRURL)
			continue
		}
		wrote++
	}
	return wrote
}
