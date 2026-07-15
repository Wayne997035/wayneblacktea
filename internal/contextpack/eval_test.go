package contextpack

// eval_test.go is the P1.3 deliverable: the deterministic full-chain eval
// suite for Assembler.Assemble() (real retrieve -> real score -> capPerType
// -> trim), exercising the assembled Pack end-to-end rather than the leaf
// funcs directly (those are covered by retrieval_test.go / scorer_test.go).
// This is the official Phase-1 eval fixture set, reused by Phase 6.
//
// All fixtures below reuse newFakes()/f.assembler()/findItem() from
// retrieval_test.go (same package) — no fakes are duplicated here.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Wayne997035/wayneblacktea/internal/behaviorrule"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// TestEvalScoreOrdering asserts a higher-relevance item (repo match + recent
// + keyword match + decision-type bonus) ranks ahead of an unrelated,
// stale one, through the full Assemble() pipeline.
func TestEvalScoreOrdering(t *testing.T) {
	f := newFakes()
	highID, lowID := uuid.New(), uuid.New()
	f.decision.byRepo = []db.Decision{
		{
			ID:        lowID,
			Title:     "unrelated topic",
			Decision:  "something else entirely",
			CreatedAt: pgtype.Timestamptz{Time: time.Now().Add(-200 * 24 * time.Hour), Valid: true},
		},
		{
			ID:        highID,
			Title:     "fix login bug decision",
			Decision:  "apply the fix",
			RepoName:  pgtype.Text{String: testRepoName, Valid: true},
			CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		},
	}

	pack, err := f.assembler().Assemble(context.Background(), Request{
		Objective: "fix login bug",
		RepoName:  testRepoName,
	})
	if err != nil {
		t.Fatalf("Assemble returned error: %v", err)
	}
	if len(pack.Items) != 2 {
		t.Fatalf("got %d items, want 2", len(pack.Items))
	}
	if pack.Items[0].ID != highID {
		t.Fatalf("Items[0].ID = %s, want high-relevance item %s (order: %+v)",
			pack.Items[0].ID, highID, pack.Items)
	}
	if pack.Items[0].Score <= pack.Items[1].Score {
		t.Errorf("Items[0].Score (%v) should be > Items[1].Score (%v)", pack.Items[0].Score, pack.Items[1].Score)
	}
}

// TestEvalBudgetTrimming asserts that when candidate items exceed the
// requested budget, UsedChars stays within it and the overflow is reported
// via Omitted, through the full Assemble() pipeline.
func TestEvalBudgetTrimming(t *testing.T) {
	f := newFakes()
	for i := 0; i < 3; i++ {
		f.knowledge.searchResults = append(f.knowledge.searchResults, db.KnowledgeItem{
			ID:        uuid.New(),
			Title:     "k",
			Content:   strings.Repeat("x", 20), // Summary = "k — xxxxxxxxxxxxxxxxxxxx" = 24 chars
			CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		})
	}

	pack, err := f.assembler().Assemble(context.Background(), Request{
		Objective:   "test",
		BudgetChars: 30,
	})
	if err != nil {
		t.Fatalf("Assemble returned error: %v", err)
	}
	if pack.UsedChars > 30 {
		t.Errorf("UsedChars = %d, want <= budget 30", pack.UsedChars)
	}
	if len(pack.Items) >= 3 {
		t.Errorf("expected budget trimming to drop at least one of the 3 candidates, got %d kept", len(pack.Items))
	}
	found := false
	for _, o := range pack.Omitted {
		if o.Type == TypeKnowledge && o.Reason == "budget" && o.Count > 0 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an Omitted entry {knowledge, budget, count>0}, got %+v", pack.Omitted)
	}
}

// TestEvalPerTypeCapEnforced asserts capPerType's per-type limit (knowledge:
// 5) survives the full pipeline even with a generous budget, i.e. it isn't
// accidentally only enforced by trimToBudget.
func TestEvalPerTypeCapEnforced(t *testing.T) {
	f := newFakes()
	for i := 0; i < 8; i++ {
		f.knowledge.searchResults = append(f.knowledge.searchResults, db.KnowledgeItem{
			ID:        uuid.New(),
			Title:     "k",
			Content:   "c",
			CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		})
	}

	pack, err := f.assembler().Assemble(context.Background(), Request{Objective: "test"})
	if err != nil {
		t.Fatalf("Assemble returned error: %v", err)
	}
	count := 0
	for _, it := range pack.Items {
		if it.Type == TypeKnowledge {
			count++
		}
	}
	if count != 5 {
		t.Errorf("knowledge items in pack = %d, want 5 (typeCap, from 8 candidates)", count)
	}
}

// TestEvalStaleAndProposedExcludedEndToEnd re-asserts retrieve()'s exclusion
// contract (archived knowledge, non-active behavior rules) survives all the
// way through Assemble(), not just retrieve() in isolation.
func TestEvalStaleAndProposedExcludedEndToEnd(t *testing.T) {
	f := newFakes()
	activeKnowledgeID, archivedKnowledgeID := uuid.New(), uuid.New()
	f.knowledge.searchResults = []db.KnowledgeItem{
		{ID: activeKnowledgeID, Title: "keep", Content: "c", CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}},
		{
			ID: archivedKnowledgeID, Title: "drop", Content: "c",
			CreatedAt:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
			ArchivedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		},
	}

	activeRuleID, proposedRuleID, deprecatedRuleID := uuid.New(), uuid.New(), uuid.New()
	f.rule.rules = []*behaviorrule.BehaviorRule{
		{ID: activeRuleID, Condition: "c", Action: "a", Status: "active", CreatedAt: time.Now()},
		{ID: proposedRuleID, Condition: "c", Action: "a", Status: "proposed", CreatedAt: time.Now()},
		{ID: deprecatedRuleID, Condition: "c", Action: "a", Status: "deprecated", CreatedAt: time.Now()},
	}

	pack, err := f.assembler().Assemble(context.Background(), Request{Objective: "test"})
	if err != nil {
		t.Fatalf("Assemble returned error: %v", err)
	}

	if findItem(pack.Items, TypeKnowledge, activeKnowledgeID) == nil {
		t.Errorf("expected active knowledge %s in final pack", activeKnowledgeID)
	}
	if findItem(pack.Items, TypeKnowledge, archivedKnowledgeID) != nil {
		t.Errorf("expected archived knowledge %s excluded from final pack", archivedKnowledgeID)
	}
	if findItem(pack.Items, "rule", activeRuleID) == nil {
		t.Errorf("expected active rule %s in final pack", activeRuleID)
	}
	if findItem(pack.Items, "rule", proposedRuleID) != nil {
		t.Errorf("expected proposed rule %s excluded from final pack", proposedRuleID)
	}
	if findItem(pack.Items, "rule", deprecatedRuleID) != nil {
		t.Errorf("expected deprecated rule %s excluded from final pack", deprecatedRuleID)
	}
}

// TestEvalEmptyAllSourcesNoError asserts that when every domain store
// returns nothing, Assemble() still returns non-nil empty slices and no
// error — the MCP handler's JSON response must never emit `null` for
// items/warnings/omitted.
func TestEvalEmptyAllSourcesNoError(t *testing.T) {
	f := newFakes()
	pack, err := f.assembler().Assemble(context.Background(), Request{Objective: "test"})
	if err != nil {
		t.Fatalf("Assemble returned error: %v", err)
	}
	if pack.Items == nil || len(pack.Items) != 0 {
		t.Errorf("Items = %#v, want non-nil empty slice", pack.Items)
	}
	if pack.Warnings == nil || len(pack.Warnings) != 0 {
		t.Errorf("Warnings = %#v, want non-nil empty slice", pack.Warnings)
	}
	if pack.Omitted == nil || len(pack.Omitted) != 0 {
		t.Errorf("Omitted = %#v, want non-nil empty slice", pack.Omitted)
	}
}

// TestEvalProvenanceSourceTableOnEveryItem asserts every Item returned by
// Assemble() — regardless of which domain store it came from — carries a
// non-empty SourceTable (both the struct field and Provenance["source_table"]).
func TestEvalProvenanceSourceTableOnEveryItem(t *testing.T) {
	f := newFakes()
	taskID := uuid.New()
	f.gtd.taskByID = &db.Task{ID: taskID, Title: "t", CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}}
	f.knowledge.searchResults = []db.KnowledgeItem{
		{ID: uuid.New(), Title: "k", Content: "c", CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}},
	}
	f.decision.byRepo = []db.Decision{
		{ID: uuid.New(), Title: "d", Decision: "do it", CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}},
	}
	f.rule.rules = []*behaviorrule.BehaviorRule{
		{ID: uuid.New(), Condition: "c", Action: "a", Status: "active", CreatedAt: time.Now()},
	}

	pack, err := f.assembler().Assemble(context.Background(), Request{
		Objective: "test", RepoName: testRepoName, TaskID: &taskID,
	})
	if err != nil {
		t.Fatalf("Assemble returned error: %v", err)
	}
	if len(pack.Items) == 0 {
		t.Fatal("expected at least one item in the pack")
	}
	for _, it := range pack.Items {
		if it.SourceTable == "" {
			t.Errorf("item %s (%s) has empty SourceTable", it.Type, it.ID)
		}
		if it.Provenance == nil || it.Provenance["source_table"] == "" {
			t.Errorf("item %s (%s) missing Provenance[source_table]", it.Type, it.ID)
		}
	}
}

// TestEvalDeterministic builds two independent Assembler instances from
// equivalent (but freshly-UUID'd) fixture data and asserts Assemble()
// produces the same Item ordering (by Type+Score), UsedChars, and Omitted
// shape both times — the pipeline must not depend on map iteration order or
// wall-clock jitter. Complements `go test ./internal/contextpack/... -run
// TestEval -count=3`, which reruns this whole file's tests 3x in the same
// process to catch any hidden nondeterminism across repeated runs.
func TestEvalDeterministic(t *testing.T) {
	buildFakes := func() *fakes {
		f := newFakes()
		f.knowledge.searchResults = []db.KnowledgeItem{
			{ID: uuid.New(), Title: "a", Content: "alpha", CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}},
			{
				ID: uuid.New(), Title: "b", Content: "beta",
				CreatedAt: pgtype.Timestamptz{Time: time.Now().Add(-300 * 24 * time.Hour), Valid: true},
			},
		}
		f.decision.byRepo = []db.Decision{
			{
				ID: uuid.New(), Title: "d", Decision: "do it",
				RepoName:  pgtype.Text{String: testRepoName, Valid: true},
				CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			},
		}
		return f
	}
	req := Request{Objective: "alpha beta", RepoName: testRepoName, BudgetChars: 200}

	pack1, err1 := buildFakes().assembler().Assemble(context.Background(), req)
	pack2, err2 := buildFakes().assembler().Assemble(context.Background(), req)
	if err1 != nil || err2 != nil {
		t.Fatalf("Assemble returned error: %v / %v", err1, err2)
	}

	if len(pack1.Items) != len(pack2.Items) {
		t.Fatalf("item count differs: %d vs %d", len(pack1.Items), len(pack2.Items))
	}
	for i := range pack1.Items {
		if pack1.Items[i].Type != pack2.Items[i].Type || pack1.Items[i].Score != pack2.Items[i].Score {
			t.Errorf("item[%d] differs: %+v vs %+v", i, pack1.Items[i], pack2.Items[i])
		}
	}
	if pack1.UsedChars != pack2.UsedChars {
		t.Errorf("UsedChars differs: %d vs %d", pack1.UsedChars, pack2.UsedChars)
	}
	if len(pack1.Omitted) != len(pack2.Omitted) {
		t.Errorf("Omitted differs: %+v vs %+v", pack1.Omitted, pack2.Omitted)
	}
}

// TestEvalIncludeTypesNarrowsOutput asserts req.IncludeTypes actually narrows
// the assembled pack (P1 review finding C: it used to be validated and set
// but never read).
func TestEvalIncludeTypesNarrowsOutput(t *testing.T) {
	f := newFakes()
	knowledgeID := uuid.New()
	f.knowledge.searchResults = []db.KnowledgeItem{
		{ID: knowledgeID, Title: "k", Content: "c", CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}},
	}
	ruleID := uuid.New()
	f.rule.rules = []*behaviorrule.BehaviorRule{
		{ID: ruleID, Condition: "c", Action: "a", Status: "active", CreatedAt: time.Now()},
	}

	pack, err := f.assembler().Assemble(context.Background(), Request{
		Objective:    "test",
		IncludeTypes: []string{"rules"},
	})
	if err != nil {
		t.Fatalf("Assemble returned error: %v", err)
	}
	if findItem(pack.Items, "rule", ruleID) == nil {
		t.Errorf("expected rule %s present when include_types=[rules]", ruleID)
	}
	if findItem(pack.Items, TypeKnowledge, knowledgeID) != nil {
		t.Errorf("expected knowledge %s excluded when include_types=[rules]", knowledgeID)
	}
}

// TestEvalStoreErrorSurfacedInPackWarnings asserts a non-fatal store failure
// during retrieve() shows up as a Warning on the final Pack (P1 review
// finding D: Pack.Warnings was hardcoded to []Warning{}).
func TestEvalStoreErrorSurfacedInPackWarnings(t *testing.T) {
	f := newFakes()
	f.atom.searchErr = errors.New("boom")

	pack, err := f.assembler().Assemble(context.Background(), Request{Objective: "test"})
	if err != nil {
		t.Fatalf("Assemble returned error: %v", err)
	}
	found := false
	for _, w := range pack.Warnings {
		if strings.Contains(w.Summary, "atom.Search") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a Warning mentioning atom.Search, got %+v", pack.Warnings)
	}
}

// TestEvalCurrentTaskProjectSurviveTightBudgetTrim asserts the current task
// still appears in the final Pack even when several higher-scored items
// would otherwise fill the entire budget before the trim ever reaches it
// (P1 review finding F).
func TestEvalCurrentTaskProjectSurviveTightBudgetTrim(t *testing.T) {
	f := newFakes()
	taskID := uuid.New()
	f.gtd.taskByID = &db.Task{
		ID: taskID, Title: "current task work in progress", Status: "in_progress",
		CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	// Each decision scores repo(0.30) + keyword(0.20) + recent(0.10) +
	// decision-type(0.15) = 0.75, well above the task's repo/task-match
	// (0.30, via the self-tagged task_id) + recent (0.10) = 0.40 — so every
	// decision sorts ahead of the task.
	for i := 0; i < 3; i++ {
		f.decision.byRepo = append(f.decision.byRepo, db.Decision{
			ID: uuid.New(), Title: "fix login bug", Decision: "apply the fix",
			RepoName:  pgtype.Text{String: testRepoName, Valid: true},
			CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		})
	}

	pack, err := f.assembler().Assemble(context.Background(), Request{
		Objective:   "fix login bug",
		RepoName:    testRepoName,
		TaskID:      &taskID,
		BudgetChars: 1, // too small for even one decision's Summary
	})
	if err != nil {
		t.Fatalf("Assemble returned error: %v", err)
	}
	if findItem(pack.Items, "task", taskID) == nil {
		t.Errorf("expected current task %s to survive a 1-char budget crowded out by higher-scored decisions, got %+v",
			taskID, pack.Items)
	}
	for _, it := range pack.Items {
		if it.Type == "decision" {
			t.Errorf("expected all 3 decisions dropped by the 1-char budget, found %+v", it)
		}
	}
}

// TestEvalBudgetCountsRunesNotBytesCJK asserts Pack.UsedChars counts runes,
// not bytes, so a multi-byte (Chinese) Summary doesn't silently consume 3x
// its apparent share of the character budget (P1 review finding G).
func TestEvalBudgetCountsRunesNotBytesCJK(t *testing.T) {
	f := newFakes()
	cjk := strings.Repeat("測", 50) // 50 runes, 150 UTF-8 bytes
	f.knowledge.searchResults = []db.KnowledgeItem{
		{ID: uuid.New(), Title: "k", Content: cjk, CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}},
	}

	pack, err := f.assembler().Assemble(context.Background(), Request{Objective: "test", BudgetChars: 1000})
	if err != nil {
		t.Fatalf("Assemble returned error: %v", err)
	}
	if len(pack.Items) != 1 {
		t.Fatalf("got %d items, want 1", len(pack.Items))
	}

	summary := pack.Items[0].Summary
	byteLen, runeLen := len(summary), utf8.RuneCountInString(summary)
	if byteLen <= runeLen {
		t.Fatalf("test fixture invalid: expected a multi-byte summary (bytes=%d, runes=%d)", byteLen, runeLen)
	}
	if pack.UsedChars != runeLen {
		t.Errorf("UsedChars = %d, want %d (rune count of Summary, byte count would have been %d)",
			pack.UsedChars, runeLen, byteLen)
	}
}
