package contextpack

import (
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// score computes a relevance score and the human-readable reasons behind it
// for a single candidate item, given the assembly request. Scoring is purely
// additive over independent signals — no time.Now, no randomness — so the
// same (item, req) pair always yields the same (score, reasons) pair.
//
// Weighted signals:
//   - +0.30 item's provenance matches the caller's current repo/project/task
//   - +0.20 item shares a file with req.FilesTouched
//   - +0.20 item's Summary contains a keyword from req.Objective
//   - +0.10 item is flagged recent by its store
//   - +0.10 item has a proven track record (success_count or confidence)
//   - +0.15 item is a past failure outcome (worth heeding)
//   - +0.15 item is a logged decision (decisions are inherently high-value)
func score(item Item, req Request) (float64, []string) {
	prov := item.Provenance
	var total float64
	var reasons []string

	add := func(weight float64, reason string) {
		total += weight
		reasons = append(reasons, reason)
	}

	if matchesRepoProjectTask(prov, req) {
		add(0.30, "matches current repo/project/task")
	}
	if sharesTouchedFile(prov["files"], req.FilesTouched) {
		add(0.20, "shares a touched file")
	}
	if keywordMatch(req.Objective, item.Summary) {
		add(0.20, "keyword match")
	}
	if prov["recent"] == provTrue {
		add(0.10, "recent")
	}
	if provenTrackRecord(prov) {
		add(0.10, "proven track record")
	}
	if item.Type == TypeOutcome && (prov["result"] == "failure" || prov["result"] == "regressed") {
		add(0.15, "past failure to heed")
	}
	if item.Type == TypeDecision {
		add(0.15, "logged decision")
	}

	return total, reasons
}

// matchesRepoProjectTask reports whether an item's provenance ties it to the
// caller's current repo, project, or task. A blank provenance value never
// matches a blank request field (both sides must be non-empty).
func matchesRepoProjectTask(prov map[string]string, req Request) bool {
	if prov["repo_name"] != "" && req.RepoName != "" && prov["repo_name"] == req.RepoName {
		return true
	}
	if req.ProjectID != nil && prov["project_id"] == req.ProjectID.String() {
		return true
	}
	if req.TaskID != nil && prov["task_id"] == req.TaskID.String() {
		return true
	}
	return false
}

// sharesTouchedFile reports whether any of touched appears in the
// comma-separated file list recorded in an item's provenance.
func sharesTouchedFile(filesCSV string, touched []string) bool {
	if filesCSV == "" || len(touched) == 0 {
		return false
	}
	provFiles := strings.Split(filesCSV, ",")
	for _, tf := range touched {
		for _, pf := range provFiles {
			if tf == strings.TrimSpace(pf) {
				return true
			}
		}
	}
	return false
}

// provTrue is the string value the domain stores write into Provenance
// boolean-style flags (e.g. "recent"). Provenance is map[string]string, not
// map[string]bool, so this constant is the single source of truth for the
// comparison instead of repeating the literal at every read site.
const provTrue = "true"

// keywordMatch reports whether any word (len >= 3) of objective is a
// substring of summary, case-insensitively.
func keywordMatch(objective, summary string) bool {
	if objective == "" || summary == "" {
		return false
	}
	summaryLower := strings.ToLower(summary)
	for _, word := range strings.Fields(strings.ToLower(objective)) {
		if len(word) < 3 {
			continue
		}
		if strings.Contains(summaryLower, word) {
			return true
		}
	}
	return false
}

// provenTrackRecord reports whether an item's provenance shows either a
// positive success_count or a confidence of at least 0.7. Missing or
// unparsable values are treated as absent (false), never as an error.
func provenTrackRecord(prov map[string]string) bool {
	if n, err := strconv.Atoi(prov["success_count"]); err == nil && n > 0 {
		return true
	}
	if f, err := strconv.ParseFloat(prov["confidence"], 64); err == nil && f >= 0.7 {
		return true
	}
	return false
}

// typeCap is the maximum number of items of a given Type that survive
// capPerType, so no single source table can crowd out the rest of the pack.
// Keys MUST be the Item.Type consts (contextpack.go) — retrieve() never
// emits "gtd"/"procedural"/"behaviorRule" as a Type, so those old keys were
// dead and every task/project/procedure/rule candidate silently fell through
// to defaultTypeCap (P1 review finding B).
var typeCap = map[string]int{
	TypeTask:       10,
	TypeProject:    10,
	TypeKnowledge:  5,
	TypeAtom:       5,
	TypeProcedure:  3,
	TypeSkill:      3,
	TypeDecision:   5,
	TypeOutcome:    3,
	TypeReflection: 2,
	TypeRule:       5,
	TypeSession:    2,
}

// defaultTypeCap applies to any item.Type not listed in typeCap.
const defaultTypeCap = 5

// capPerType enforces a per-type cap on the candidate set, keeping the
// highest-scored items of each type. Ties break on item.ID (string order)
// so the result is deterministic regardless of input order.
func capPerType(items []Item) []Item {
	byType := make(map[string][]Item)
	var order []string
	for _, it := range items {
		if _, seen := byType[it.Type]; !seen {
			order = append(order, it.Type)
		}
		byType[it.Type] = append(byType[it.Type], it)
	}

	var result []Item
	for _, t := range order {
		group := byType[t]
		sort.SliceStable(group, func(i, j int) bool {
			if group[i].Score != group[j].Score {
				return group[i].Score > group[j].Score
			}
			return group[i].ID.String() < group[j].ID.String()
		})

		limit := defaultTypeCap
		if n, ok := typeCap[t]; ok {
			limit = n
		}
		if limit > len(group) {
			limit = len(group)
		}
		result = append(result, group[:limit]...)
	}
	return result
}

// isPinned reports whether it is exempt from trimToBudget's budget cutoff.
// The spec requires "always include current task/project if available";
// retrieveCurrentTaskProject is the only source that emits TypeTask/
// TypeProject, so every item of those types is, by construction, a current
// task/project item.
func isPinned(it Item) bool {
	return it.Type == TypeTask || it.Type == TypeProject
}

// trimToBudget walks items (assumed already sorted by Score desc) and keeps
// a greedy prefix that fits within budgetChars of cumulative Summary length
// (rune-counted, not byte-counted — the API's "character budget" means
// runes, and a byte count would silently under-fit CJK/multi-byte summaries
// to a fraction of the stated budget): the first non-pinned item that would
// push the running total over budget stops non-pinned inclusion outright (no
// skip-and-continue past it). Pinned items (isPinned) are always kept —
// wherever they land in the score-ordered walk — and still count toward
// UsedChars; everything else that gets dropped is reported as omitted,
// grouped by Type.
func trimToBudget(items []Item, budgetChars int) (kept []Item, omitted []Omitted) {
	total := 0
	stopped := false
	kept = make([]Item, 0, len(items))
	var dropped []Item

	for _, it := range items {
		length := utf8.RuneCountInString(it.Summary)
		if isPinned(it) {
			kept = append(kept, it)
			total += length
			continue
		}
		if stopped {
			dropped = append(dropped, it)
			continue
		}
		next := total + length
		if next > budgetChars {
			stopped = true
			dropped = append(dropped, it)
			continue
		}
		total = next
		kept = append(kept, it)
	}

	counts := make(map[string]int)
	var order []string
	for _, it := range dropped {
		if _, seen := counts[it.Type]; !seen {
			order = append(order, it.Type)
		}
		counts[it.Type]++
	}

	omitted = make([]Omitted, 0, len(order))
	for _, t := range order {
		omitted = append(omitted, Omitted{Type: t, Count: counts[t], Reason: "budget"})
	}
	return kept, omitted
}
