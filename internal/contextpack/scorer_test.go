package contextpack

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// testRepoName is the shared repo-name fixture used across this package's
// tests (scorer_test.go/eval_test.go/retrieval_test.go); factored into one
// constant so goconst doesn't flag the literal repeated 3+ times.
const testRepoName = "wayneblacktea"

func TestScore(t *testing.T) {
	repoName := testRepoName
	projectID := uuid.New()
	taskID := uuid.New()

	baseReq := Request{
		Objective:    "fix the login timeout bug",
		RepoName:     repoName,
		ProjectID:    &projectID,
		TaskID:       &taskID,
		FilesTouched: []string{"internal/auth/login.go"},
	}

	tests := []struct {
		name        string
		item        Item
		req         Request
		wantScore   float64
		wantReasons []string
	}{
		{
			name: "repo name match",
			item: Item{
				Type:       TypeKnowledge,
				Summary:    "unrelated",
				Provenance: map[string]string{"repo_name": repoName},
			},
			req:         baseReq,
			wantScore:   0.30,
			wantReasons: []string{"matches current repo/project/task"},
		},
		{
			name: "project id match",
			item: Item{
				Type:       TypeKnowledge,
				Summary:    "unrelated",
				Provenance: map[string]string{"project_id": projectID.String()},
			},
			req:         baseReq,
			wantScore:   0.30,
			wantReasons: []string{"matches current repo/project/task"},
		},
		{
			name: "task id match",
			item: Item{
				Type:       TypeKnowledge,
				Summary:    "unrelated",
				Provenance: map[string]string{"task_id": taskID.String()},
			},
			req:         baseReq,
			wantScore:   0.30,
			wantReasons: []string{"matches current repo/project/task"},
		},
		{
			name: "blank repo_name on both sides does not match",
			item: Item{
				Type:       TypeKnowledge,
				Summary:    "unrelated",
				Provenance: map[string]string{"repo_name": ""},
			},
			req:         Request{Objective: "x", RepoName: ""},
			wantScore:   0,
			wantReasons: nil,
		},
		{
			name: "file match",
			item: Item{
				Type:       TypeKnowledge,
				Summary:    "unrelated",
				Provenance: map[string]string{"files": "internal/other.go,internal/auth/login.go"},
			},
			req:         baseReq,
			wantScore:   0.20,
			wantReasons: []string{"shares a touched file"},
		},
		{
			name: "keyword match",
			item: Item{
				Type:       TypeKnowledge,
				Summary:    "notes about the login timeout issue in prod",
				Provenance: map[string]string{},
			},
			req:         baseReq,
			wantScore:   0.20,
			wantReasons: []string{"keyword match"},
		},
		{
			name: "recent",
			item: Item{
				Type:       TypeKnowledge,
				Summary:    "unrelated",
				Provenance: map[string]string{"recent": "true"},
			},
			req:         baseReq,
			wantScore:   0.10,
			wantReasons: []string{"recent"},
		},
		{
			name: "proven via success_count",
			item: Item{
				Type:       TypeKnowledge,
				Summary:    "unrelated",
				Provenance: map[string]string{"success_count": "3"},
			},
			req:         baseReq,
			wantScore:   0.10,
			wantReasons: []string{"proven track record"},
		},
		{
			name: "proven via confidence",
			item: Item{
				Type:       TypeKnowledge,
				Summary:    "unrelated",
				Provenance: map[string]string{"confidence": "0.85"},
			},
			req:         baseReq,
			wantScore:   0.10,
			wantReasons: []string{"proven track record"},
		},
		{
			name: "confidence below threshold does not score",
			item: Item{
				Type:       TypeKnowledge,
				Summary:    "unrelated",
				Provenance: map[string]string{"confidence": "0.5"},
			},
			req:         baseReq,
			wantScore:   0,
			wantReasons: nil,
		},
		{
			name: "failed outcome",
			item: Item{
				Type:       "outcome",
				Summary:    "unrelated",
				Provenance: map[string]string{"result": "failure"},
			},
			req:         baseReq,
			wantScore:   0.15,
			wantReasons: []string{"past failure to heed"},
		},
		{
			name: "regressed outcome",
			item: Item{
				Type:       "outcome",
				Summary:    "unrelated",
				Provenance: map[string]string{"result": "regressed"},
			},
			req:         baseReq,
			wantScore:   0.15,
			wantReasons: []string{"past failure to heed"},
		},
		{
			name: "successful outcome does not score the failure signal",
			item: Item{
				Type:       "outcome",
				Summary:    "unrelated",
				Provenance: map[string]string{"result": "success"},
			},
			req:         baseReq,
			wantScore:   0,
			wantReasons: nil,
		},
		{
			name: "decision type",
			item: Item{
				Type:       "decision",
				Summary:    "unrelated",
				Provenance: map[string]string{},
			},
			req:         baseReq,
			wantScore:   0.15,
			wantReasons: []string{"logged decision"},
		},
		{
			name: "no signal at all",
			item: Item{
				Type:       TypeKnowledge,
				Summary:    "totally unrelated summary text",
				Provenance: nil,
			},
			req:         baseReq,
			wantScore:   0,
			wantReasons: nil,
		},
		{
			name: "malformed provenance values are tolerated, not errors",
			item: Item{
				Type:    TypeKnowledge,
				Summary: "unrelated",
				Provenance: map[string]string{
					"success_count": "not-a-number",
					"confidence":    "not-a-float",
					"recent":        "maybe",
				},
			},
			req:         baseReq,
			wantScore:   0,
			wantReasons: nil,
		},
		{
			name: "all signals stack additively",
			item: Item{
				Type:    "decision",
				Summary: "plan to fix the login timeout regression",
				Provenance: map[string]string{
					"repo_name":     repoName,
					"files":         "internal/auth/login.go",
					"recent":        "true",
					"success_count": "5",
				},
			},
			req:       baseReq,
			wantScore: 0.30 + 0.20 + 0.20 + 0.10 + 0.10 + 0.15,
			wantReasons: []string{
				"matches current repo/project/task",
				"shares a touched file",
				"keyword match",
				"recent",
				"proven track record",
				"logged decision",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotScore, gotReasons := score(tc.item, tc.req)
			if math.Abs(gotScore-tc.wantScore) > 1e-9 {
				t.Errorf("score = %v, want %v", gotScore, tc.wantScore)
			}
			if !reflect.DeepEqual(gotReasons, tc.wantReasons) {
				t.Errorf("reasons = %v, want %v", gotReasons, tc.wantReasons)
			}
		})
	}
}

func TestCapPerType_KnowledgeCappedAtFive(t *testing.T) {
	items := make([]Item, 6)
	for i := range items {
		items[i] = Item{Type: TypeKnowledge, ID: uuid.New(), Score: float64(i)}
	}
	got := capPerType(items)
	if len(got) != 5 {
		t.Fatalf("got %d items, want 5", len(got))
	}
	for _, it := range got {
		if it.Score == 0 {
			t.Errorf("lowest-scored item should have been dropped, found it in result: %+v", it)
		}
	}
}

func TestCapPerType_UnknownTypeUsesDefaultCap(t *testing.T) {
	items := make([]Item, 10)
	for i := range items {
		items[i] = Item{Type: "mystery-type", ID: uuid.New(), Score: float64(i)}
	}
	got := capPerType(items)
	if len(got) == 0 || len(got) >= len(items) {
		t.Errorf("expected unknown type to be capped below input size, got %d of %d", len(got), len(items))
	}
}

// TestCapPerType_TaskProjectProcedureRuleCaps exercises the typeCap keys
// that P1 review finding B found dead ("gtd"/"procedural"/"behaviorRule"
// never matched an emitted Item.Type). Regression guard: candidates of these
// four types must actually be capped, not silently fall through to
// defaultTypeCap (5) or, worse, typeCap's absence entirely.
func TestCapPerType_TaskProjectProcedureRuleCaps(t *testing.T) {
	var items []Item
	for i := 0; i < 12; i++ {
		items = append(items, Item{Type: TypeTask, ID: uuid.New(), Score: float64(i)})
	}
	for i := 0; i < 12; i++ {
		items = append(items, Item{Type: TypeProject, ID: uuid.New(), Score: float64(i)})
	}
	for i := 0; i < 5; i++ {
		items = append(items, Item{Type: TypeProcedure, ID: uuid.New(), Score: float64(i)})
	}
	for i := 0; i < 8; i++ {
		items = append(items, Item{Type: TypeRule, ID: uuid.New(), Score: float64(i)})
	}

	got := capPerType(items)

	counts := map[string]int{}
	for _, it := range got {
		counts[it.Type]++
	}
	if counts[TypeTask] != 10 {
		t.Errorf("task count = %d, want 10 (typeCap[task], from 12 candidates)", counts[TypeTask])
	}
	if counts[TypeProject] != 10 {
		t.Errorf("project count = %d, want 10 (typeCap[project], from 12 candidates)", counts[TypeProject])
	}
	if counts[TypeProcedure] != 3 {
		t.Errorf("procedure count = %d, want 3 (typeCap[procedure], from 5 candidates)", counts[TypeProcedure])
	}
	if counts[TypeRule] != 5 {
		t.Errorf("rule count = %d, want 5 (typeCap[rule], from 8 candidates)", counts[TypeRule])
	}
}

func TestCapPerType_TypesCappedIndependently(t *testing.T) {
	var items []Item
	for i := 0; i < 3; i++ {
		items = append(items, Item{Type: "decision", ID: uuid.New(), Score: float64(i)})
	}
	for i := 0; i < 2; i++ {
		items = append(items, Item{Type: "reflection", ID: uuid.New(), Score: float64(i)})
	}
	got := capPerType(items)
	// decision cap=5 (3 items, all under cap) + reflection cap=2 (2 items, all under cap)
	if len(got) != 5 {
		t.Fatalf("got %d items, want 5 (both groups entirely under their caps)", len(got))
	}
}

func TestCapPerType_TieAtCapBoundaryIsDeterministic(t *testing.T) {
	lowID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	highID := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	clearWinner := uuid.MustParse("00000000-0000-0000-0000-000000000003")

	makeItems := func() []Item {
		// reflection cap is 2; highID and lowID tie at 0.5, clearWinner is
		// unambiguously first. Only one of {lowID, highID} survives.
		return []Item{
			{Type: "reflection", ID: highID, Score: 0.5},
			{Type: "reflection", ID: clearWinner, Score: 1.0},
			{Type: "reflection", ID: lowID, Score: 0.5},
		}
	}

	first := capPerType(makeItems())
	second := capPerType(makeItems())
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("got %d and %d items, want 2 (reflection cap)", len(first), len(second))
	}
	if first[0].ID != second[0].ID || first[1].ID != second[1].ID {
		t.Fatalf("capPerType is not deterministic across repeated calls: first=%+v second=%+v", first, second)
	}
	kept := map[uuid.UUID]bool{first[0].ID: true, first[1].ID: true}
	if !kept[clearWinner] {
		t.Errorf("expected clear winner %s to survive the cap, kept=%v", clearWinner, kept)
	}
	if !kept[lowID] {
		t.Errorf("expected lower-ID item %s to win the tiebreak over %s, kept=%v", lowID, highID, kept)
	}
}

// TestTrimToBudget is split into one top-level func per case (rather than
// t.Run subtests sharing one body) so golangci-lint's gocyclo — which sums
// nested closures into the enclosing function's complexity — doesn't flag a
// single monolithic test function.

func TestTrimToBudget_AllItemsFitWithinBudget(t *testing.T) {
	items := []Item{
		{Type: TypeKnowledge, ID: uuid.New(), Summary: "12345"},
		{Type: TypeKnowledge, ID: uuid.New(), Summary: "678"},
	}
	kept, omitted := trimToBudget(items, 100)
	if len(kept) != 2 {
		t.Fatalf("got %d kept, want 2", len(kept))
	}
	if len(omitted) != 0 {
		t.Errorf("got %d omitted groups, want 0: %+v", len(omitted), omitted)
	}
}

func TestTrimToBudget_GreedyPrefixStopsAtFirstOverflow(t *testing.T) {
	items := []Item{
		{Type: "decision", ID: uuid.New(), Summary: strings.Repeat("a", 10)},
		{Type: "decision", ID: uuid.New(), Summary: strings.Repeat("b", 10)},
		{Type: TypeKnowledge, ID: uuid.New(), Summary: "x"}, // would fit if we skipped-and-continued
	}
	kept, omitted := trimToBudget(items, 15)
	if len(kept) != 1 {
		t.Fatalf("got %d kept, want 1 (greedy prefix must stop, not skip-and-continue)", len(kept))
	}
	if len(omitted) != 2 {
		t.Fatalf("got %d omitted groups, want 2 (decision + knowledge)", len(omitted))
	}
	counts := map[string]int{}
	for _, o := range omitted {
		counts[o.Type] = o.Count
		if o.Reason != "budget" {
			t.Errorf("omitted[%s].Reason = %q, want %q", o.Type, o.Reason, "budget")
		}
	}
	if counts["decision"] != 1 || counts[TypeKnowledge] != 1 {
		t.Errorf("omitted counts = %v, want decision:1 knowledge:1", counts)
	}
}

func TestTrimToBudget_ZeroBudgetOmitsEverything(t *testing.T) {
	items := []Item{
		{Type: TypeKnowledge, ID: uuid.New(), Summary: "nonempty"},
	}
	kept, omitted := trimToBudget(items, 0)
	if len(kept) != 0 {
		t.Fatalf("got %d kept, want 0", len(kept))
	}
	if len(omitted) != 1 || omitted[0].Count != 1 {
		t.Fatalf("omitted = %+v, want one group with count 1", omitted)
	}
}

func TestTrimToBudget_NoOmissionsReturnsNonNilEmptySlice(t *testing.T) {
	kept, omitted := trimToBudget(nil, 100)
	if len(kept) != 0 {
		t.Fatalf("got %d kept, want 0", len(kept))
	}
	if omitted == nil {
		t.Fatalf("omitted must be non-nil even when there is nothing to omit")
	}
	if len(omitted) != 0 {
		t.Errorf("got %d omitted, want 0", len(omitted))
	}
}

// TestTrimToBudget_PinnedTaskSurvivesEvenThoughItAloneExceedsBudget covers
// P1 review finding F: current task/project must always survive the budget
// trim, even positioned after (or entirely disqualified by) the budget walk.
func TestTrimToBudget_PinnedTaskSurvivesEvenThoughItAloneExceedsBudget(t *testing.T) {
	taskID := uuid.New()
	items := []Item{
		{Type: TypeKnowledge, ID: uuid.New(), Score: 0.9, Summary: strings.Repeat("a", 20)},
		{Type: TypeTask, ID: taskID, Score: 0.0, Summary: strings.Repeat("b", 20)},
	}
	kept, _ := trimToBudget(items, 25)
	found := false
	for _, it := range kept {
		if it.Type == TypeTask && it.ID == taskID {
			found = true
		}
	}
	if !found {
		t.Errorf("expected pinned task %s to survive despite pushing the total over budget, kept=%+v", taskID, kept)
	}
}

func TestTrimToBudget_PinnedProjectSurvivesWhenBudgetFitsNothingElse(t *testing.T) {
	projectID := uuid.New()
	items := []Item{
		{Type: TypeKnowledge, ID: uuid.New(), Score: 0.9, Summary: strings.Repeat("a", 100)},
		{Type: TypeProject, ID: projectID, Score: 0.1, Summary: "p"},
	}
	kept, omitted := trimToBudget(items, 5)
	if len(kept) != 1 || kept[0].Type != TypeProject || kept[0].ID != projectID {
		t.Fatalf("kept = %+v, want only the pinned project item", kept)
	}
	if len(omitted) != 1 || omitted[0].Type != TypeKnowledge {
		t.Errorf("omitted = %+v, want knowledge omitted", omitted)
	}
}

// TestTrimToBudget_BudgetCountsRunesNotBytesCJK covers P1 review finding G:
// budget is a rune (character) count, not a byte count. Each "測" is 1 rune /
// 3 UTF-8 bytes, so a 10-rune summary is 30 bytes — it must exactly fit a
// budget of 10 and just barely not fit 9.
func TestTrimToBudget_BudgetCountsRunesNotBytesCJK(t *testing.T) {
	cjk := strings.Repeat("測", 10)
	items := []Item{{Type: TypeKnowledge, ID: uuid.New(), Summary: cjk}}

	kept, omitted := trimToBudget(items, 10)
	if len(kept) != 1 {
		t.Fatalf("budget=10 runes: got %d kept, want 1 (10-rune/30-byte summary should exactly fit)", len(kept))
	}
	if len(omitted) != 0 {
		t.Errorf("budget=10 runes: got omitted %+v, want none", omitted)
	}

	kept, omitted = trimToBudget(items, 9)
	if len(kept) != 0 {
		t.Fatalf("budget=9 runes: got %d kept, want 0 (a 10-rune summary must not fit a 9-rune budget)", len(kept))
	}
	if len(omitted) != 1 {
		t.Errorf("budget=9 runes: got omitted %+v, want one group", omitted)
	}
}
