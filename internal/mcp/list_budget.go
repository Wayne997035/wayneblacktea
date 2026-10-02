package mcp

import (
	"encoding/json"
	"log/slog"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
)

// listRuneBudget is the shared response-size ceiling (in runes of the
// marshaled JSON array) applied to list_tasks/list_goals/list_projects/
// list_decisions — [F0930-11]. It caps the LIST ARRAY only, not the response
// envelope (returned/limit/offset/has_more/truncated_by_budget), matching
// appendNextActionsWithinByteBudget's (resources.go) existing scope: the
// envelope fields are a few dozen runes, negligible either way.
//
// Row-count limits (listPageMaxLimit, list_tasks' inline 100, and
// maxListDecisionsLimit) stay the FIRST-applied cap; this budget is an
// ADDITIONAL ceiling on top — a caller that requests few rows can still hit
// it if those rows are large (CJK content, long descriptions), which is
// exactly the failure mode this exists to catch: a page of large rows blew
// past Claude Code's ~25k-token ingestion ceiling and was silently dropped
// whole (measured this session: list_tasks limit=200 returned 51,765 chars
// and was rejected outright).
const listRuneBudget = 18000

// truncateListByRuneBudget stops before any row whose JSON-encoded rune
// count would push the running total over maxRunes. Mirrors
// appendNextActionsWithinByteBudget's (resources.go) accounting — 2 runes for
// the array's own "[" and "]", +1 per joining comma — but counts
// utf8.RuneCountInString of the marshaled JSON, not len() bytes: CJK content
// is 1 rune but up to 3 UTF-8 bytes, so reusing the byte-budget helper
// directly would let a CJK-heavy page run up to 3x over the stated rune
// budget. Only whole trailing rows are ever dropped, never a partial field —
// same "successful, smaller page" contract as appendNextActionsWithinByteBudget,
// including the zero-row edge case (row 0 alone already exceeds maxRunes ->
// kept is empty, truncated is true, still a successful result upstream).
func truncateListByRuneBudget[T any](rows []T, maxRunes int) (kept []T, truncated bool) {
	kept = make([]T, 0, len(rows))
	totalRunes := 2 // "[" + "]"
	for i := range rows {
		encoded, err := json.Marshal(rows[i])
		if err != nil {
			// A row that cannot be marshaled cannot be sized against the
			// budget — dropping it (and every row after, since the caller's
			// has_more/offset paging already covers "there's more") is the
			// only budget-honest choice. This cannot happen in practice for
			// the flat wire structs these four tools build (see toTaskSummary/
			// wrapUntrustedTask/toProjectListItem/toGoalListItem/
			// wrapUntrustedDecision), but a marshal error must never corrupt
			// the running rune count for the rows already accepted.
			slog.Warn("truncateListByRuneBudget: marshaling row for rune budget", "index", i, "err", err)
			truncated = true
			break
		}
		itemRunes := utf8.RuneCountInString(string(encoded))
		if len(kept) > 0 {
			itemRunes++ // joining comma before this element
		}
		if totalRunes+itemRunes > maxRunes {
			truncated = true
			break
		}
		totalRunes += itemRunes
		kept = append(kept, rows[i])
	}
	return kept, truncated
}

// listDescriptionMaxRunes bounds a project/goal's Description field on the
// LIST view only (get_project/get_goal-class single-record reads keep the
// larger gtdBodyMaxRunes cap) — [F0930-14]. list_projects/list_goals return
// every active row's FULL record with no summary mode (unlike list_tasks'
// compact taskSummary), so a single ordinary-length Description can consume
// most of listRuneBudget by itself; description_truncated makes the cut
// visible instead of silently shrinking the page.
const listDescriptionMaxRunes = 500

// clipListDescription bounds an already-wrapUntrusted*'d description column
// (wrapUntrustedProject/wrapUntrustedGoal have already neutralised any
// boundary marker at the larger gtdBodyMaxRunes cap by the time this runs)
// to listDescriptionMaxRunes, reporting whether the value changed. SQL NULL
// is left as NULL and never reported as truncated — mirrors
// clipRepoListText's (tools_context.go) output-vs-input comparison.
func clipListDescription(d pgtype.Text) (pgtype.Text, bool) {
	if !d.Valid {
		return d, false
	}
	clipped := clipSafe(d.String, listDescriptionMaxRunes)
	truncated := clipped != d.String
	d.String = clipped
	return d, truncated
}
