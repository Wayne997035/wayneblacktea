package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/playbook"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ---------------------------------------------------------------------------
// Stubs for playbook domain
// ---------------------------------------------------------------------------

// stubPlaybookStore implements playbook.StoreIface for tests.
type stubPlaybookStore struct {
	created   []*playbook.Playbook
	createErr error
}

func (s *stubPlaybookStore) Create(_ context.Context, p playbook.CreateParams) (*playbook.Playbook, error) {
	if s.createErr != nil {
		return nil, s.createErr
	}
	pb := &playbook.Playbook{
		ID:                uuid.New(),
		TriggerPattern:    p.TriggerPattern,
		ActionTemplate:    p.ActionTemplate,
		SourceDecisionIDs: p.SourceDecisionIDs,
		Confidence:        p.Confidence,
	}
	s.created = append(s.created, pb)
	return pb, nil
}

func (s *stubPlaybookStore) List(_ context.Context, _ playbook.ListParams) ([]*playbook.Playbook, error) {
	return s.created, nil
}

func (s *stubPlaybookStore) IncrementHits(_ context.Context, _ uuid.UUID) error {
	return nil
}

// makeDecision is a test helper that builds a db.Decision with the given title
// and a recent created_at timestamp.
func makeDecision(title string) db.Decision {
	id, _ := uuid.NewRandom()
	return db.Decision{
		ID:        id,
		Title:     title,
		RepoName:  pgtype.Text{String: "wayneblacktea", Valid: true},
		CreatedAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}
}

// makeOldDecision creates a decision with a created_at older than 30 days.
func makeOldDecision(title string) db.Decision {
	id, _ := uuid.NewRandom()
	return db.Decision{
		ID:        id,
		Title:     title,
		RepoName:  pgtype.Text{String: "wayneblacktea", Valid: true},
		CreatedAt: pgtype.Timestamptz{Time: time.Now().Add(-40 * 24 * time.Hour).UTC(), Valid: true},
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestRunPlaybookPromoter_HappyPath(t *testing.T) {
	decisions := []db.Decision{
		makeDecision("Use pgx directly for new domains"),
		makeDecision("No FK constraints — referential integrity in code"),
		makeDecision("Always add workspace_id scoping to Store constructors"),
	}

	proposals := []ai.KnowledgeProposal{
		{
			Title:   "When adding a new domain: use pgx directly, no sqlc",
			Content: "Create Store with pgxpool; implement StoreIface by hand; skip sqlc for new packages.",
			Tags:    []string{decisions[0].ID.String(), decisions[1].ID.String()},
		},
		{
			Title:   "Scope every Store to workspace_id",
			Content: "Pass workspaceID to NewStore; all queries include ($N::uuid IS NULL OR workspace_id = $N).",
			Tags:    []string{decisions[2].ID.String()},
		},
	}

	propStore := &stubProposalStore{}
	pbStore := &stubPlaybookStore{}
	reflector := &stubReflector{proposals: proposals}
	decStore := &stubDecisionStore{decisions: decisions}

	deps := playbookDeps{
		decision:  decStore,
		playbook:  pbStore,
		proposal:  propStore,
		reflector: reflector,
	}
	runPlaybookPromoter(deps)

	if len(propStore.created) != 2 {
		t.Errorf("expected 2 proposals created, got %d", len(propStore.created))
	}
	for _, p := range propStore.created {
		if string(p.Type) != "playbook" {
			t.Errorf("proposal type: got %q, want playbook", p.Type)
		}
	}
}

func TestRunPlaybookPromoter_FewerThanMinDecisionsSkips(t *testing.T) {
	// Only 2 recent decisions — below the playbookPromoterMinDecisions threshold of 3.
	decisions := []db.Decision{
		makeDecision("Decision A"),
		makeDecision("Decision B"),
	}

	propStore := &stubProposalStore{}
	pbStore := &stubPlaybookStore{}
	reflector := &stubReflector{proposals: []ai.KnowledgeProposal{
		{Title: "should not be created", Content: "body"},
	}}
	decStore := &stubDecisionStore{decisions: decisions}

	deps := playbookDeps{
		decision:  decStore,
		playbook:  pbStore,
		proposal:  propStore,
		reflector: reflector,
	}
	runPlaybookPromoter(deps)

	if len(propStore.created) != 0 {
		t.Errorf("expected 0 proposals when fewer than min decisions, got %d", len(propStore.created))
	}
}

func TestRunPlaybookPromoter_OldDecisionsFilteredOut(t *testing.T) {
	// Mix of old (>30 days) and recent decisions — only 1 recent, below threshold.
	decisions := []db.Decision{
		makeOldDecision("Old decision 1"),
		makeOldDecision("Old decision 2"),
		makeDecision("Recent decision"),
	}

	propStore := &stubProposalStore{}
	pbStore := &stubPlaybookStore{}
	reflector := &stubReflector{proposals: []ai.KnowledgeProposal{
		{Title: "T", Content: "C"},
	}}
	decStore := &stubDecisionStore{decisions: decisions}

	deps := playbookDeps{
		decision:  decStore,
		playbook:  pbStore,
		proposal:  propStore,
		reflector: reflector,
	}
	runPlaybookPromoter(deps)

	// Only 1 recent decision (< 3), so should skip.
	if len(propStore.created) != 0 {
		t.Errorf("expected 0 proposals (only 1 recent decision), got %d", len(propStore.created))
	}
}

func TestRunPlaybookPromoter_EmptyAIResponseSkips(t *testing.T) {
	decisions := []db.Decision{
		makeDecision("D1"), makeDecision("D2"), makeDecision("D3"),
	}

	propStore := &stubProposalStore{}
	pbStore := &stubPlaybookStore{}
	reflector := &stubReflector{proposals: nil}
	decStore := &stubDecisionStore{decisions: decisions}

	deps := playbookDeps{
		decision:  decStore,
		playbook:  pbStore,
		proposal:  propStore,
		reflector: reflector,
	}
	runPlaybookPromoter(deps)

	if len(propStore.created) != 0 {
		t.Errorf("expected 0 proposals for empty AI response, got %d", len(propStore.created))
	}
}

func TestRunPlaybookPromoter_MalformedProposalSkipped(t *testing.T) {
	decisions := []db.Decision{
		makeDecision("D1"), makeDecision("D2"), makeDecision("D3"),
	}

	proposals := []ai.KnowledgeProposal{
		{Title: "", Content: "no title — should be skipped"},
		{Title: "Valid title", Content: "Valid content"},
	}

	propStore := &stubProposalStore{}
	pbStore := &stubPlaybookStore{}
	reflector := &stubReflector{proposals: proposals}
	decStore := &stubDecisionStore{decisions: decisions}

	deps := playbookDeps{
		decision:  decStore,
		playbook:  pbStore,
		proposal:  propStore,
		reflector: reflector,
	}
	runPlaybookPromoter(deps)

	// Only 1 valid proposal should be created.
	if len(propStore.created) != 1 {
		t.Errorf("expected 1 proposal, got %d", len(propStore.created))
	}
}

func TestRunPlaybookPromoter_DecisionListError(t *testing.T) {
	decStore := &stubDecisionStore{decErr: errReflectionStoreFailure}
	propStore := &stubProposalStore{}
	pbStore := &stubPlaybookStore{}
	reflector := &stubReflector{}

	deps := playbookDeps{
		decision:  decStore,
		playbook:  pbStore,
		proposal:  propStore,
		reflector: reflector,
	}
	// Should not panic; logs warn.
	runPlaybookPromoter(deps)
	if len(propStore.created) != 0 {
		t.Errorf("expected 0 proposals when decision list fails, got %d", len(propStore.created))
	}
}

// TestRunPlaybookPromoter_PayloadFieldMapping is a characterization test
// written BEFORE refactoring processPlaybookProposals's tail to use
// runProposalTail (F0929-71). Replaces TestMarshalPlaybookPayload (deleted
// alongside marshalPlaybookPayload/createPlaybookProposal — see proploop
// spec Target behaviour); also covers the srcIDs==nil -> []uuid.UUID{}
// nil-normalization case the deleted test covered.
func TestRunPlaybookPromoter_PayloadFieldMapping(t *testing.T) {
	decisions := []db.Decision{
		makeDecision("D1"), makeDecision("D2"), makeDecision("D3"),
	}

	t.Run("MixedValidAndInvalidTags", func(t *testing.T) {
		id1, id2 := uuid.New(), uuid.New()
		propStore := &stubProposalStore{}
		pbStore := &stubPlaybookStore{}
		reflector := &stubReflector{proposals: []ai.KnowledgeProposal{
			{Title: "Trigger1", Content: "Action1", Tags: []string{id1.String(), id2.String(), "not-a-uuid"}},
		}}
		decStore := &stubDecisionStore{decisions: decisions}

		deps := playbookDeps{decision: decStore, playbook: pbStore, proposal: propStore, reflector: reflector}
		runPlaybookPromoter(deps)

		if len(propStore.created) != 1 {
			t.Fatalf("expected 1 proposal created, got %d", len(propStore.created))
		}
		var payload PlaybookProposalPayload
		if err := json.Unmarshal(propStore.created[0].Payload, &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if payload.TriggerPattern != "Trigger1" {
			t.Errorf("payload.TriggerPattern = %q, want %q", payload.TriggerPattern, "Trigger1")
		}
		if payload.ActionTemplate != "Action1" {
			t.Errorf("payload.ActionTemplate = %q, want %q", payload.ActionTemplate, "Action1")
		}
		wantIDs := []uuid.UUID{id1, id2}
		if len(payload.SourceDecisionIDs) != len(wantIDs) ||
			payload.SourceDecisionIDs[0] != wantIDs[0] || payload.SourceDecisionIDs[1] != wantIDs[1] {
			t.Errorf("payload.SourceDecisionIDs = %v, want %v (the non-UUID tag must be silently dropped)", payload.SourceDecisionIDs, wantIDs)
		}
	})

	t.Run("AllTagsInvalid_EmptySliceNotNil", func(t *testing.T) {
		propStore := &stubProposalStore{}
		pbStore := &stubPlaybookStore{}
		reflector := &stubReflector{proposals: []ai.KnowledgeProposal{
			{Title: "Trigger2", Content: "Action2", Tags: []string{"not-a-uuid-1", "not-a-uuid-2"}},
		}}
		decStore := &stubDecisionStore{decisions: decisions}

		deps := playbookDeps{decision: decStore, playbook: pbStore, proposal: propStore, reflector: reflector}
		runPlaybookPromoter(deps)

		if len(propStore.created) != 1 {
			t.Fatalf("expected 1 proposal created, got %d", len(propStore.created))
		}
		var payload PlaybookProposalPayload
		if err := json.Unmarshal(propStore.created[0].Payload, &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		// A JSON `null` (Go nil slice) unmarshals back to nil; a JSON `[]`
		// (Go []uuid.UUID{}) unmarshals back to a non-nil empty slice — this
		// distinguishes the two on the wire, matching marshalPlaybookPayload's
		// pre-refactor nil-normalization.
		if payload.SourceDecisionIDs == nil {
			t.Error("payload.SourceDecisionIDs = nil, want non-nil []uuid.UUID{}")
		}
		if len(payload.SourceDecisionIDs) != 0 {
			t.Errorf("payload.SourceDecisionIDs = %v, want empty", payload.SourceDecisionIDs)
		}
	})
}

// TestRunPlaybookPromoter_WarnLogFieldsPreserved verifies that a Create
// failure logs processPlaybookProposals's own pre-refactor message/field
// shape ("trigger_pattern", not "item_id") — no existing Create-error test
// covers this file (proploop spec Acceptance criteria). marshal-fail and
// Create-fail intentionally share one message/hook (Target behaviour), so
// the negative case is the same as reflection.go's row: item_id= absent.
func TestRunPlaybookPromoter_WarnLogFieldsPreserved(t *testing.T) {
	buf := captureSlogWarn(t)

	decisions := []db.Decision{
		makeDecision("D1"), makeDecision("D2"), makeDecision("D3"),
	}
	propStore := &stubProposalStore{createErr: errors.New("db write failed")}
	pbStore := &stubPlaybookStore{}
	reflector := &stubReflector{proposals: []ai.KnowledgeProposal{
		{Title: "Trigger1", Content: "Action1"},
	}}
	decStore := &stubDecisionStore{decisions: decisions}

	deps := playbookDeps{decision: decStore, playbook: pbStore, proposal: propStore, reflector: reflector}
	runPlaybookPromoter(deps)

	out := buf.String()
	if !containsStr(out, "playbook promoter: creating pending proposal failed") {
		t.Errorf("warn output missing expected message, got: %s", out)
	}
	if !containsStr(out, "trigger_pattern=Trigger1") {
		t.Errorf("warn output missing trigger_pattern field, got: %s", out)
	}
	if containsStr(out, "item_id=") {
		t.Errorf("warn output must NOT contain item_id= (runProposalLoop's template), got: %s", out)
	}
}
