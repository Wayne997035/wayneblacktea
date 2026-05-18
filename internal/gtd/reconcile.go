// Package gtd reconcile.go implements the server-side matcher that consumes
// Claude-supplied merged-PR lists and produces a list of (task, PR) matches.
// See sprint feature/gtd-enforce-server-side GTD-fix 9/12 — closes the
// "PR merged but task still pending" gap surfaced by today's 5 stale tasks.
//
// Matching rule (in priority order):
//  1. tasks.pr_url == merged_prs[i].url  (case-insensitive) → reason="pr_url_exact"
//  2. tasks.branch_name == merged_prs[i].head_ref  (exact case) → reason="branch_name_exact"
//
// Only EXACT matches auto-apply. If more than one pending task shares the same
// branch_name we pick the most recent (by updated_at fall-back created_at) and
// surface the rest as a completion_candidate queue entry with status='pending'
// so the operator can disambiguate manually.
//
// This file is dialect-agnostic. The matcher reads tasks via gtd.StoreIface
// and writes completion via the BatchCompleteTasksByPRMatch method on the
// same store interface; both Postgres and SQLite backends MUST implement that
// method (backend-security-design §6.3 dual-backend parity).
package gtd

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
)

// MergedPR is the input record for the reconcile matcher. Fields mirror the
// JSON payload Claude posts to /api/tasks/reconcile-merged-prs.
type MergedPR struct {
	URL      string    `json:"url"`
	HeadRef  string    `json:"head_ref"`
	MergedAt time.Time `json:"merged_at"`
	Title    string    `json:"title"`
	Body     string    `json:"body"`
	Repo     string    `json:"repo"`
}

// MatchReason identifies which linkage column drove the match.
type MatchReason string

const (
	// MatchReasonPRURLExact: tasks.pr_url already pointed at this PR URL.
	MatchReasonPRURLExact MatchReason = "pr_url_exact"
	// MatchReasonBranchNameExact: tasks.branch_name == MergedPR.HeadRef.
	MatchReasonBranchNameExact MatchReason = "branch_name_exact"
)

// Match is a (task, PR) pair the matcher decided is an exact, auto-applyable
// hit. BodyExcerpt is a sanitised, length-capped snippet of the PR body so the
// auto-close audit log records WHAT actually merged.
type Match struct {
	TaskID      uuid.UUID
	Reason      MatchReason
	PRUrl       string
	PRHeadRef   string
	MergedAt    time.Time
	BodyExcerpt string
}

// Ambiguous represents a branch_name shared by multiple pending tasks. The
// "winner" (most-recent task) is auto-applied via Match; remaining tasks are
// surfaced here as completion_candidates with status='pending' so the operator
// can manually pick the right one (a server-side decision would risk silently
// closing the wrong task).
type Ambiguous struct {
	TaskID    uuid.UUID
	Reason    MatchReason
	PRUrl     string
	PRHeadRef string
}

// MatchResult bundles the outputs of MatchMergedPRs.
type MatchResult struct {
	// Matches is the list of auto-apply (task, PR) pairs.
	Matches []Match
	// Ambiguous is the list of "also matched this branch, not picked" entries.
	Ambiguous []Ambiguous
	// NoMatch counts PRs with zero pending-task hit (logged but not actioned).
	NoMatch int
}

// bodyExcerptMaxLen caps the persisted body slice to keep the audit log
// readable. 500 chars accommodates a typical PR description summary while
// avoiding multi-megabyte rows.
const bodyExcerptMaxLen = 500

// sanitiseBodyExcerpt strips control characters (per backend-security-design
// §5.4) and caps the result at bodyExcerptMaxLen runes.
func sanitiseBodyExcerpt(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	count := 0
	for _, r := range s {
		if r == '\x00' || (r < 0x20 && r != '\t' && r != '\n') {
			continue
		}
		if count >= bodyExcerptMaxLen {
			break
		}
		b.WriteRune(r)
		count++
	}
	return strings.TrimSpace(b.String())
}

// MatchMergedPRs scans all pending/in_progress tasks once and matches them
// against the supplied PR list.
//
// Tasks are loaded ONCE via store.Tasks(nil); this is a personal-scale store
// (<10k rows) so a single read is acceptable and cheaper than per-PR queries.
//
// Returns a MatchResult; the caller is responsible for invoking
// BatchCompleteTasksByPRMatch + completioncandidate WriteAutoApplied with the
// outputs. The matcher is purely read-only.
func MatchMergedPRs(
	ctx context.Context,
	store StoreIface,
	prs []MergedPR,
) (MatchResult, error) {
	if len(prs) == 0 {
		return MatchResult{Matches: []Match{}, Ambiguous: []Ambiguous{}}, nil
	}

	tasks, err := store.Tasks(ctx, nil)
	if err != nil {
		return MatchResult{}, fmt.Errorf("MatchMergedPRs: load tasks: %w", err)
	}

	// Build lookup indices: lowercase pr_url → []task and branch_name → []task.
	prURLIndex := make(map[string][]db.Task, len(tasks))
	branchIndex := make(map[string][]db.Task, len(tasks))
	for i := range tasks {
		t := tasks[i]
		// We only auto-close OPEN tasks; completed tasks are already done.
		if t.Status != string(TaskStatusPending) && t.Status != string(TaskStatusInProgress) {
			continue
		}
		if t.PRUrl.Valid && t.PRUrl.String != "" {
			k := strings.ToLower(strings.TrimSpace(t.PRUrl.String))
			prURLIndex[k] = append(prURLIndex[k], t)
		}
		if t.BranchName.Valid && t.BranchName.String != "" {
			branchIndex[t.BranchName.String] = append(branchIndex[t.BranchName.String], t)
		}
	}

	result := MatchResult{
		Matches:   make([]Match, 0, len(prs)),
		Ambiguous: make([]Ambiguous, 0),
	}

	// Track which task IDs have been claimed already (to avoid double-counting
	// a single task that matches via BOTH pr_url and branch_name on different PRs
	// in the same batch).
	claimed := make(map[uuid.UUID]bool, len(prs))

	for _, pr := range prs {
		match, ambig, hit := matchSinglePR(pr, prURLIndex, branchIndex, claimed)
		if hit {
			result.Matches = append(result.Matches, match)
			claimed[match.TaskID] = true
		} else {
			result.NoMatch++
		}
		result.Ambiguous = append(result.Ambiguous, ambig...)
	}

	return result, nil
}

// matchSinglePR resolves one MergedPR against the prebuilt indices and returns
// the winner (if any) plus any siblings that need ambiguity handling.
//
// Priority: pr_url_exact beats branch_name_exact. The pr_url path cannot be
// ambiguous (a task can carry only one pr_url and pr_url is the more
// authoritative signal). branch_name CAN be ambiguous — multiple in-flight
// tasks may share the same branch slug (e.g. when a task was split mid-stream).
func matchSinglePR(
	pr MergedPR,
	prURLIndex, branchIndex map[string][]db.Task,
	claimed map[uuid.UUID]bool,
) (Match, []Ambiguous, bool) {
	// 1) pr_url_exact (case-insensitive)
	if pr.URL != "" {
		k := strings.ToLower(strings.TrimSpace(pr.URL))
		if hits := prURLIndex[k]; len(hits) > 0 {
			// pr_url should be uniquely owned by at most one task; if for any
			// reason it isn't, prefer the most-recent unclaimed task.
			pick := pickMostRecentUnclaimed(hits, claimed)
			if pick != nil {
				return Match{
					TaskID:      pick.ID,
					Reason:      MatchReasonPRURLExact,
					PRUrl:       pr.URL,
					PRHeadRef:   pr.HeadRef,
					MergedAt:    pr.MergedAt,
					BodyExcerpt: sanitiseBodyExcerpt(pr.Body),
				}, nil, true
			}
		}
	}

	// 2) branch_name_exact (case-sensitive — git branch names are case-sensitive)
	if pr.HeadRef == "" {
		return Match{}, nil, false
	}
	hits := branchIndex[pr.HeadRef]
	if len(hits) == 0 {
		return Match{}, nil, false
	}

	pick := pickMostRecentUnclaimed(hits, claimed)
	if pick == nil {
		return Match{}, nil, false
	}
	winner := Match{
		TaskID:      pick.ID,
		Reason:      MatchReasonBranchNameExact,
		PRUrl:       pr.URL,
		PRHeadRef:   pr.HeadRef,
		MergedAt:    pr.MergedAt,
		BodyExcerpt: sanitiseBodyExcerpt(pr.Body),
	}

	// Surface remaining unclaimed siblings as ambiguous → operator decides.
	var ambig []Ambiguous
	for _, t := range hits {
		if t.ID == pick.ID || claimed[t.ID] {
			continue
		}
		ambig = append(ambig, Ambiguous{
			TaskID:    t.ID,
			Reason:    MatchReasonBranchNameExact,
			PRUrl:     pr.URL,
			PRHeadRef: pr.HeadRef,
		})
	}
	return winner, ambig, true
}

// pickMostRecentUnclaimed returns a pointer to the most-recently-updated task
// in hits that is not already claimed by an earlier PR in this batch. Returns
// nil when all hits are claimed.
//
// "Most recent" prefers UpdatedAt over CreatedAt; both are pgtype.Timestamptz
// so invalid (zero) values sort to the end.
func pickMostRecentUnclaimed(hits []db.Task, claimed map[uuid.UUID]bool) *db.Task {
	// Stable sort: most recent first.
	sorted := make([]db.Task, 0, len(hits))
	for _, t := range hits {
		if !claimed[t.ID] {
			sorted = append(sorted, t)
		}
	}
	if len(sorted) == 0 {
		return nil
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		return taskRecency(sorted[i]).After(taskRecency(sorted[j]))
	})
	return &sorted[0]
}

// taskRecency returns the more recent of UpdatedAt and CreatedAt. Both invalid
// → zero time (sorted last).
func taskRecency(t db.Task) time.Time {
	switch {
	case t.UpdatedAt.Valid && t.CreatedAt.Valid:
		if t.UpdatedAt.Time.After(t.CreatedAt.Time) {
			return t.UpdatedAt.Time
		}
		return t.CreatedAt.Time
	case t.UpdatedAt.Valid:
		return t.UpdatedAt.Time
	case t.CreatedAt.Valid:
		return t.CreatedAt.Time
	default:
		return time.Time{}
	}
}
