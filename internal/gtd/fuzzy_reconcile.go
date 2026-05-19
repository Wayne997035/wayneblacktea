// Package gtd fuzzy_reconcile.go implements the Phase 2 fuzzy matcher used
// by reconcile_merged_prs (sprint feature/0519-gtd-reconcile-phase2,
// GTD-fix 10/12) for legacy backlog tasks whose branch_name / pr_url linkage
// is NULL — i.e. tasks the GTD-fix 9/12 exact-match path can never reach.
//
// Algorithm (per pending/in_progress task with null branch_name AND null
// pr_url):
//
//  1. If the PR body literally contains the task's UUID → score = 1.0,
//     reason = "task_id_in_body" (high signal but still surfaced as a
//     candidate, never auto-applied — see ReasonPRMerged doc).
//  2. Else compute Jaccard similarity between tokenised task.title and
//     pr.title. Tokens are lowercased, len>=3, with a small English-only
//     stopword set stripped.
//  3. If Jaccard >= matchThreshold (0.6) → score = jaccard, reason =
//     "jaccard_title". Best-score PR per task wins.
//
// Threshold note (0.6) is a deliberate first-cut guess; every match is
// emitted with slog.Info(score=...) so calibration can be done from logs
// before tuning. Stopword list is English-only — Chinese / mixed-locale
// titles will score lower than they should; documented as a known
// limitation, future work tracked via follow-up task.
//
// This matcher is purely read-only. It writes nothing — callers persist
// the chosen candidates into completion_candidates with confidence='medium'
// (NOT 'high') via completioncandidate.Store.UpsertCandidate so the operator
// must manually accept the row before any task closes.
package gtd

import (
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
)

// MergedPRForFuzzy mirrors MergedPR with the fields the fuzzy matcher uses.
// Kept as a distinct type only because in principle the fuzzy path may
// evolve to need extra context (labels, file diff stats) the exact-match
// path doesn't care about — currently they're identical.
type MergedPRForFuzzy = MergedPR

// FuzzyMatch is the (task, PR) pair the fuzzy matcher decided is a likely
// hit. Score is in [0, 1.0]. Reason is one of fuzzyReasonTaskIDInBody or
// fuzzyReasonJaccardTitle (see constants below). Callers MUST emit each
// FuzzyMatch as completion_candidates with confidence='medium' — never
// auto-apply.
type FuzzyMatch struct {
	TaskID uuid.UUID
	PRURL  string
	Score  float64
	Reason string
}

// fuzzyReasonTaskIDInBody / fuzzyReasonJaccardTitle are the values written
// into completion_candidates.evidence_refs[] so the manual-accept UI can
// show WHY a candidate was surfaced.
const (
	FuzzyReasonTaskIDInBody = "task_id_in_body"
	FuzzyReasonJaccardTitle = "jaccard_title"
)

// matchThreshold is the minimum Jaccard score that qualifies a (task, PR)
// pair as a fuzzy match. 0.6 is a first-cut guess; calibration is deferred
// to the follow-up that tunes from emitted slog scores. See package doc.
const matchThreshold = 0.6

// minTokenLen drops short tokens before Jaccard — len<3 tokens (e.g. "do",
// "of", "to") add noise without discriminative power.
const minTokenLen = 3

// stopwords is the small built-in English stopword set. Intentional that
// this is small (~15 entries): we don't want to over-prune real
// discriminators. Chinese / mixed-locale titles are not stop-listed; their
// tokens stay and the Jaccard still works (just over-counts particles).
//
//nolint:gochecknoglobals // compile-time constant set used for stopword check.
var stopwords = map[string]struct{}{
	"the":      {},
	"and":      {},
	"for":      {},
	"with":     {},
	"from":     {},
	"this":     {},
	"that":     {},
	"into":     {},
	"fix":      {},
	"feat":     {},
	"chore":    {},
	"refactor": {},
	"docs":     {},
	"test":     {},
	"ci":       {},
}

// tokenise lowercases s, splits on non-alphanumeric runs, drops tokens
// shorter than minTokenLen and stopwords, and returns the unique set.
// Returns a map for O(1) intersection / union in jaccard.
func tokenise(s string) map[string]struct{} {
	out := make(map[string]struct{}, 8)
	var cur strings.Builder
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		tok := cur.String()
		cur.Reset()
		if len(tok) < minTokenLen {
			return
		}
		if _, isStop := stopwords[tok]; isStop {
			return
		}
		out[tok] = struct{}{}
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			cur.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			cur.WriteRune(r + 32) // lowercase
		case r >= '0' && r <= '9':
			cur.WriteRune(r)
		case r == '_':
			cur.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return out
}

// jaccard returns |a ∩ b| / |a ∪ b| in [0, 1]. Returns 0 if either set is
// empty.
func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	// iterate the smaller set for fewer map lookups
	smaller, larger := a, b
	if len(b) < len(a) {
		smaller, larger = b, a
	}
	for k := range smaller {
		if _, ok := larger[k]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// MatchPendingTasksFuzzy iterates over `tasks` (pending/in_progress with
// null branch_name AND null pr_url) and `prs`, returning at most one
// FuzzyMatch per qualifying task: the highest-scoring PR above
// matchThreshold (or any PR whose body literally contains the task UUID).
//
// Caller filters out exact-match-eligible tasks before invoking — those go
// through the GTD-fix 9/12 path. The matcher does not duplicate that work.
//
// Tasks that have ANY non-null linkage column (branch_name or pr_url) are
// silently skipped here even if they appear in the input slice — this lets
// the caller pass `store.Tasks(ctx, nil)` directly without pre-filtering
// and the fuzzy matcher just does the right thing.
func MatchPendingTasksFuzzy(prs []MergedPR, tasks []db.Task) []FuzzyMatch {
	if len(prs) == 0 || len(tasks) == 0 {
		return nil
	}

	// Pre-tokenise PR titles once (PRs are reused across many tasks).
	prTokens := make([]map[string]struct{}, len(prs))
	for i := range prs {
		prTokens[i] = tokenise(prs[i].Title)
	}

	var out []FuzzyMatch
	for _, t := range tasks {
		if !isFuzzyEligible(t) {
			continue
		}
		taskTok := tokenise(t.Title)
		var best FuzzyMatch
		bestScore := 0.0
		bestFound := false
		taskIDStr := t.ID.String()

		for i, pr := range prs {
			// Rule 1: task UUID literally appears in body.
			if pr.Body != "" && strings.Contains(pr.Body, taskIDStr) {
				return appendForcedMatch(out, t.ID, pr.URL, 1.0, FuzzyReasonTaskIDInBody, prs, tasks)
			}

			// Rule 2: Jaccard title similarity.
			score := jaccard(taskTok, prTokens[i])
			if score >= matchThreshold && score > bestScore {
				bestScore = score
				best = FuzzyMatch{
					TaskID: t.ID,
					PRURL:  pr.URL,
					Score:  score,
					Reason: FuzzyReasonJaccardTitle,
				}
				bestFound = true
			}
		}
		if bestFound {
			out = append(out, best)
		}
	}
	return out
}

// appendForcedMatch is the early-return helper for rule 1 (task UUID in
// body). Returning at the first body match means a single task can be
// claimed by exactly one PR via the UUID path; subsequent PRs in the same
// batch with the same UUID-in-body would be the same hit anyway and the
// candidate uniqueness key (task_id, reason) handles the dedup at write
// time.
//
// We accept the unused prs/tasks parameters so the function signature can
// stay flexible if a future revision needs to continue past the first hit
// (currently it doesn't; we exit immediately for that single task).
func appendForcedMatch(
	out []FuzzyMatch,
	taskID uuid.UUID,
	prURL string,
	score float64,
	reason string,
	_ []MergedPR,
	_ []db.Task,
) []FuzzyMatch {
	return append(out, FuzzyMatch{
		TaskID: taskID,
		PRURL:  prURL,
		Score:  score,
		Reason: reason,
	})
}

// isFuzzyEligible returns true when the task is pending or in_progress AND
// has neither branch_name nor pr_url set. Tasks already linked to a branch
// or PR go through the exact-match path (GTD-fix 9/12); the fuzzy matcher
// MUST NOT shadow them.
func isFuzzyEligible(t db.Task) bool {
	if t.Status != string(TaskStatusPending) && t.Status != string(TaskStatusInProgress) {
		return false
	}
	if t.BranchName.Valid && t.BranchName.String != "" {
		return false
	}
	if t.PRUrl.Valid && t.PRUrl.String != "" {
		return false
	}
	return true
}
