// Package contextpack assembles a bounded, budget-capped "context pack" of
// the most relevant GTD/decision/knowledge/memory items for a given
// objective, so an agent (or a human) can be handed a compact brief instead
// of re-reading the whole workspace.
package contextpack

import (
	"context"
	"errors"
	"sort"
	"unicode/utf8"

	"github.com/google/uuid"
)

// defaultBudgetChars is used when the caller does not specify a positive
// BudgetChars.
const defaultBudgetChars = 12000

// Item.Type vocabulary. These are the only values retrieve()'s itemFromX
// helpers ever emit; scorer.go's typeCap and retrieval.go's IncludeTypes
// mapping key off these consts (not string literals) so the two can never
// silently drift apart again (P1 review finding B: typeCap used to key on
// "gtd"/"procedural"/"behaviorRule", none of which matched what retrieve()
// actually emitted).
const (
	TypeTask       = "task"
	TypeProject    = "project"
	TypeDecision   = "decision"
	TypeKnowledge  = "knowledge"
	TypeAtom       = "atom"
	TypeProcedure  = "procedure"
	TypeSkill      = "skill"
	TypeOutcome    = "outcome"
	TypeReflection = "reflection"
	TypeRule       = "rule"
	TypeSession    = "session"
)

// Request describes what the caller wants a context pack assembled for.
type Request struct {
	Objective    string
	RepoName     string
	ProjectID    *uuid.UUID
	TaskID       *uuid.UUID
	BranchName   string
	FilesTouched []string
	BudgetChars  int
	IncludeTypes []string
	Persist      bool
}

// Item is a single candidate (task, decision, knowledge row, memory atom,
// etc.) pulled in from one of the domain stores, scored and annotated for
// inclusion in a Pack.
type Item struct {
	Type        string            `json:"type"`
	ID          uuid.UUID         `json:"id"`
	SourceTable string            `json:"source_table"`
	Summary     string            `json:"summary"`
	Score       float64           `json:"score"`
	Reasons     []string          `json:"reasons"`
	Provenance  map[string]string `json:"provenance"`
}

// Warning surfaces a non-fatal issue encountered while assembling the pack
// (e.g. a store call failed and was skipped).
type Warning struct {
	Type    string `json:"type"`
	Summary string `json:"summary"`
}

// Omitted records that items of a given Type were dropped (and why) while
// trimming the candidate set to fit the budget.
type Omitted struct {
	Type   string `json:"type"`
	Count  int    `json:"count"`
	Reason string `json:"reason"`
}

// Pack is the assembled, budget-capped result handed back to the caller. The
// json tags here are the wire contract documented at
// docs/wayneblacktea-2.0-development-prompt.md:265-291 — snake_case at every
// level, including nested Item/Warning/Omitted.
type Pack struct {
	PackID      *uuid.UUID `json:"pack_id"`
	Objective   string     `json:"objective"`
	BudgetChars int        `json:"budget_chars"`
	UsedChars   int        `json:"used_chars"`
	Items       []Item     `json:"items"`
	Warnings    []Warning  `json:"warnings"`
	Omitted     []Omitted  `json:"omitted"`
}

// Assembler holds the domain read ports needed to retrieve, score, and trim
// context pack candidates. It is backend-agnostic: every field is a narrow,
// consumer-owned read port (see ports.go) implemented structurally by the
// same Postgres/SQLite stores that satisfy the full domain StoreIface — no
// adapter type is needed to narrow them.
type Assembler struct {
	gtd          TaskProjectReadPort
	decision     DecisionReadPort
	knowledge    KnowledgeReadPort
	atom         AtomReadPort
	procedural   ProceduralReadPort
	skill        SkillReadPort
	outcome      OutcomeReadPort
	reflection   ReflectionReadPort
	behaviorRule BehaviorRuleReadPort
	session      SessionReadPort
	workSession  WorkSessionReadPort
}

// NewAssembler wires the domain read ports an Assembler needs. All arguments
// are required — a nil store would panic on first use deep inside retrieve()
// (internal/contextpack/retrieval.go), so NewAssembler rejects nil up front
// instead of leaving the invariant unenforced.
func NewAssembler(
	gtdStore TaskProjectReadPort,
	decisionStore DecisionReadPort,
	knowledgeStore KnowledgeReadPort,
	atomStore AtomReadPort,
	proceduralStore ProceduralReadPort,
	skillStore SkillReadPort,
	outcomeStore OutcomeReadPort,
	reflectionStore ReflectionReadPort,
	behaviorRuleStore BehaviorRuleReadPort,
	sessionStore SessionReadPort,
	workSessionStore WorkSessionReadPort,
) (*Assembler, error) {
	switch {
	case gtdStore == nil:
		return nil, errors.New("contextpack.NewAssembler: gtdStore is nil")
	case decisionStore == nil:
		return nil, errors.New("contextpack.NewAssembler: decisionStore is nil")
	case knowledgeStore == nil:
		return nil, errors.New("contextpack.NewAssembler: knowledgeStore is nil")
	case atomStore == nil:
		return nil, errors.New("contextpack.NewAssembler: atomStore is nil")
	case proceduralStore == nil:
		return nil, errors.New("contextpack.NewAssembler: proceduralStore is nil")
	case skillStore == nil:
		return nil, errors.New("contextpack.NewAssembler: skillStore is nil")
	case outcomeStore == nil:
		return nil, errors.New("contextpack.NewAssembler: outcomeStore is nil")
	case reflectionStore == nil:
		return nil, errors.New("contextpack.NewAssembler: reflectionStore is nil")
	case behaviorRuleStore == nil:
		return nil, errors.New("contextpack.NewAssembler: behaviorRuleStore is nil")
	case sessionStore == nil:
		return nil, errors.New("contextpack.NewAssembler: sessionStore is nil")
	case workSessionStore == nil:
		return nil, errors.New("contextpack.NewAssembler: workSessionStore is nil")
	}
	return &Assembler{
		gtd:          gtdStore,
		decision:     decisionStore,
		knowledge:    knowledgeStore,
		atom:         atomStore,
		procedural:   proceduralStore,
		skill:        skillStore,
		outcome:      outcomeStore,
		reflection:   reflectionStore,
		behaviorRule: behaviorRuleStore,
		session:      sessionStore,
		workSession:  workSessionStore,
	}, nil
}

// Assemble retrieves candidate items, scores and caps them, trims to the
// requested character budget, and returns the resulting Pack.
//
// Stale/proposed exclusion is retrieve()'s responsibility, not this
// orchestration's.
func (a *Assembler) Assemble(ctx context.Context, req Request) (*Pack, error) {
	budget := req.BudgetChars
	if budget <= 0 {
		budget = defaultBudgetChars
	}

	items, warnings := a.retrieve(ctx, req)
	if warnings == nil {
		warnings = []Warning{}
	}

	for i := range items {
		items[i].Score, items[i].Reasons = score(items[i], req)
	}

	items = capPerType(items)

	sort.SliceStable(items, func(i, j int) bool {
		return items[i].Score > items[j].Score
	})

	kept, omitted := trimToBudget(items, budget)
	if kept == nil {
		kept = []Item{}
	}
	if omitted == nil {
		omitted = []Omitted{}
	}

	// Character budget is rune-counted, not byte-counted — a CJK Summary is
	// ~3 bytes/rune, so len(string) would silently under-fit a 12000-char
	// budget to ~4000 actual characters.
	usedChars := 0
	for _, it := range kept {
		usedChars += utf8.RuneCountInString(it.Summary)
	}

	return &Pack{
		PackID:      nil,
		Objective:   req.Objective,
		BudgetChars: budget,
		UsedChars:   usedChars,
		Items:       kept,
		Warnings:    warnings,
		Omitted:     omitted,
	}, nil
}
