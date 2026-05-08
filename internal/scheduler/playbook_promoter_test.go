package scheduler

import (
	"context"
	"strings"
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

func TestMarshalPlaybookPayload(t *testing.T) {
	ids := []uuid.UUID{uuid.New(), uuid.New()}
	payload, err := marshalPlaybookPayload("trigger", "action", ids)
	if err != nil {
		t.Fatalf("marshalPlaybookPayload: %v", err)
	}
	if len(payload) == 0 {
		t.Error("payload should not be empty")
	}

	// Verify nil IDs does not panic.
	payload2, err := marshalPlaybookPayload("trigger", "action", nil)
	if err != nil {
		t.Fatalf("marshalPlaybookPayload nil ids: %v", err)
	}
	if len(payload2) == 0 {
		t.Error("payload2 should not be empty")
	}
}

func TestBuildAdaptedPlaybookPrompt(t *testing.T) {
	prompt := buildAdaptedPlaybookPrompt(`[{"id":"abc","title":"T"}]`)
	if len(prompt) == 0 {
		t.Error("prompt should not be empty")
	}
	// Verify decision JSON is embedded in the prompt.
	if !strings.Contains(prompt, "abc") {
		t.Error("prompt should contain the decision JSON content")
	}
}
