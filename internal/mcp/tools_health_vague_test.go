package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/discipline"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/watchdog"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// makeVagueTask is a local helper that mirrors db.Task usage in the vague-task
// flow. Renamed to avoid collision with makeTask in tools_closeout_test.go.
func makeVagueTask(status, desc, kind string) db.Task {
	return db.Task{
		ID:     uuid.New(),
		Title:  "task",
		Status: status,
		Kind:   kind,
		Description: pgtype.Text{
			String: desc,
			Valid:  desc != "",
		},
	}
}

// TestCountVaguePending verifies the helper that powers
// snap.VaguePendingTaskCount + snap.VagueSampleIDs.
func TestCountVaguePending(t *testing.T) {
	t.Parallel()

	pendingVagueA := makeVagueTask("pending", "TBD", "feature")
	pendingVagueB := makeVagueTask("pending", "auto-captured from MCP: complete_task", "fix-pr")
	inProgressVague := makeVagueTask("in_progress", "WIP", "feature")
	pendingHealthy := makeVagueTask(
		"pending",
		"Refactor internal/storage/factory.go:42 to inject *pgxpool.Pool via constructor",
		"refactor",
	)
	pendingChoreShort := makeVagueTask("pending", "TBD", "chore") // chore exempt
	completedVague := makeVagueTask("completed", "TBD", "feature")
	cancelledVague := makeVagueTask("cancelled", "TBD", "feature")
	emptyDescription := db.Task{
		ID:     uuid.New(),
		Title:  "no description",
		Status: "pending",
		Kind:   "feature",
	}

	tasks := []db.Task{
		pendingVagueA,
		pendingVagueB,
		inProgressVague,
		pendingHealthy,
		pendingChoreShort,
		completedVague,
		cancelledVague,
		emptyDescription,
	}

	count, sampleIDs := countVaguePending(tasks)

	// Expected vague: pendingVagueA, pendingVagueB, inProgressVague → 3.
	const wantCount = 3
	if count != wantCount {
		t.Errorf("count = %d, want %d", count, wantCount)
	}

	wantIDs := map[string]bool{
		pendingVagueA.ID.String():   true,
		pendingVagueB.ID.String():   true,
		inProgressVague.ID.String(): true,
	}
	if len(sampleIDs) != wantCount {
		t.Fatalf("sampleIDs len = %d (%v), want %d", len(sampleIDs), sampleIDs, wantCount)
	}
	for _, id := range sampleIDs {
		if !wantIDs[id] {
			t.Errorf("sampleIDs contained unexpected ID %q (want any of %v)", id, wantIDs)
		}
	}
}

// TestCountVaguePending_CapsSampleIDsAt5 verifies that the sample list is
// capped at maxVagueSampleIDs even when many vague tasks exist; count
// reflects the true total.
func TestCountVaguePending_CapsSampleIDsAt5(t *testing.T) {
	t.Parallel()

	tasks := make([]db.Task, 0, 8)
	for i := 0; i < 8; i++ {
		tasks = append(tasks, makeVagueTask("pending", "TBD", "feature"))
	}

	count, sampleIDs := countVaguePending(tasks)
	if count != 8 {
		t.Errorf("count = %d, want 8", count)
	}
	if len(sampleIDs) != maxVagueSampleIDs {
		t.Errorf("sampleIDs len = %d, want %d (cap)", len(sampleIDs), maxVagueSampleIDs)
	}
}

// TestCountVaguePending_EmptyInput verifies the helper returns 0, nil for
// no tasks — the system_health response then encodes vague_pending_task_count
// as 0 and omits vague_sample_ids.
func TestCountVaguePending_EmptyInput(t *testing.T) {
	t.Parallel()
	count, sampleIDs := countVaguePending(nil)
	if count != 0 || sampleIDs != nil {
		t.Errorf("countVaguePending(nil) = %d, %v; want 0, nil", count, sampleIDs)
	}
}

// ---- TestSystemHealth_CountsVagueTasks: end-to-end via handleSystemHealth ----

// fakeHealthGTDStore embeds mockGTDStore so the gtd.StoreIface surface is
// satisfied; only Tasks() is overridden to return the seeded slice.
type fakeHealthGTDStore struct {
	mockGTDStore
	tasks []db.Task
}

func (f *fakeHealthGTDStore) Tasks(_ context.Context, _ *uuid.UUID) ([]db.Task, error) {
	return f.tasks, nil
}

// fakeHealthProposalStore is a no-op proposal.StoreIface; ListPending returns
// empty so handleSystemHealth records 0 pending proposals.
type fakeHealthProposalStore struct{}

func (f *fakeHealthProposalStore) Create(_ context.Context, _ proposal.CreateParams) (*db.PendingProposal, error) {
	return nil, nil
}

func (f *fakeHealthProposalStore) Get(_ context.Context, _ uuid.UUID) (*db.PendingProposal, error) {
	return nil, nil
}

func (f *fakeHealthProposalStore) ListPending(_ context.Context) ([]db.PendingProposal, error) {
	return nil, nil
}

func (f *fakeHealthProposalStore) ListAll(_ context.Context, _ string, _ int32) ([]db.PendingProposal, error) {
	return nil, nil
}

func (f *fakeHealthProposalStore) Resolve(_ context.Context, _ uuid.UUID, _ proposal.Status) (*db.PendingProposal, error) {
	return nil, nil
}

func (f *fakeHealthProposalStore) BatchConfirm(_ context.Context, _ []uuid.UUID, _ proposal.Status) (proposal.BatchConfirmResult, error) {
	return proposal.BatchConfirmResult{}, nil
}

func (f *fakeHealthProposalStore) AutoProposeConceptFromKnowledge(
	_ context.Context, _ *db.KnowledgeItem, _ string,
) (*db.PendingProposal, error) {
	return nil, nil
}

// fakeHealthLearningStore is a no-op learning.StoreIface so handleSystemHealth
// can call CountDueReviews without nil-panicking.
type fakeHealthLearningStore struct{}

func (f *fakeHealthLearningStore) CreateConcept(_ context.Context, _, _ string, _ []string) (*db.Concept, error) {
	return nil, nil
}

func (f *fakeHealthLearningStore) DueReviews(_ context.Context, _ int) ([]learning.DueReview, error) {
	return nil, nil
}

func (f *fakeHealthLearningStore) SubmitReview(_ context.Context, _ uuid.UUID, _ learning.CardState, _ learning.Rating) error {
	return nil
}

func (f *fakeHealthLearningStore) CountDueReviews(_ context.Context) (int, error) {
	return 0, nil
}

func (f *fakeHealthLearningStore) ListForAIReview(_ context.Context, _ int) ([]learning.ConceptForReview, error) {
	return nil, nil
}

func (f *fakeHealthLearningStore) UpdateConceptStatus(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}

func (f *fakeHealthLearningStore) ReviewHistory(_ context.Context) ([]learning.ConceptHistoryRow, error) {
	return nil, nil
}

func (f *fakeHealthLearningStore) LearningStats(_ context.Context) (*learning.LearningStatsResult, error) {
	return nil, nil
}

func (f *fakeHealthLearningStore) ListConcepts(_ context.Context, _ int) ([]db.Concept, error) {
	return nil, nil
}

func (f *fakeHealthLearningStore) ReviewedSince(_ context.Context, _ time.Time, _ int) ([]learning.DueReview, error) {
	return nil, nil
}

// stubHealthDisciplineStore returns empty mutating events and empty decisions.
type stubHealthDisciplineStore struct{}

func (s *stubHealthDisciplineStore) Insert(_ context.Context, _ discipline.InsertParams) error {
	return nil
}

func (s *stubHealthDisciplineStore) RecentMutating(_ context.Context, _ time.Time, _ int) ([]discipline.Event, error) {
	return nil, nil
}

func (s *stubHealthDisciplineStore) RecentDecisionTimes(_ context.Context, _ string, _ time.Time) ([]time.Time, error) {
	return nil, nil
}

// TestSystemHealth_CountsVagueTasks builds a minimal Server with stub stores,
// seeds vague + healthy tasks, invokes handleSystemHealth, and asserts the
// vague_pending_task_count + vague_sample_ids fields in the JSON response.
//
// Two vague tasks (one with the TBD marker, one with the auto-captured
// placeholder literal) plus one healthy task should yield count=2 with the
// two vague IDs in the sample.
func TestSystemHealth_CountsVagueTasks(t *testing.T) {
	vagueA := makeVagueTask("pending", "TBD", "feature")
	vagueB := makeVagueTask("pending", "auto-captured from MCP: complete_task", "fix-pr")
	healthy := makeVagueTask(
		"pending",
		"Refactor internal/storage/factory.go:42 to inject *pgxpool.Pool via constructor",
		"refactor",
	)

	srv := &Server{
		gtd:        &fakeHealthGTDStore{tasks: []db.Task{vagueA, vagueB, healthy}},
		proposal:   &fakeHealthProposalStore{},
		learning:   &fakeHealthLearningStore{},
		watchdog:   watchdog.New(10),
		discipline: &stubHealthDisciplineStore{},
		sessionID:  "test-health-vague",
	}

	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	res, err := srv.handleSystemHealth(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSystemHealth: %v", err)
	}
	if res == nil || len(res.Content) == 0 {
		t.Fatal("nil or empty result")
	}

	textContent, ok := res.Content[0].(mcpmsg.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want TextContent", res.Content[0])
	}

	var snap struct {
		VaguePendingTaskCount int      `json:"vague_pending_task_count"`
		VagueSampleIDs        []string `json:"vague_sample_ids"`
		ForgottenSignals      []string `json:"forgotten_signals"`
	}
	if err := json.Unmarshal([]byte(textContent.Text), &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if snap.VaguePendingTaskCount != 2 {
		t.Errorf("VaguePendingTaskCount = %d, want 2", snap.VaguePendingTaskCount)
	}
	if len(snap.VagueSampleIDs) != 2 {
		t.Fatalf("VagueSampleIDs len = %d, want 2 (got %v)", len(snap.VagueSampleIDs), snap.VagueSampleIDs)
	}

	gotIDs := map[string]bool{}
	for _, id := range snap.VagueSampleIDs {
		gotIDs[id] = true
	}
	if !gotIDs[vagueA.ID.String()] {
		t.Errorf("VagueSampleIDs missing vagueA %s; got %v", vagueA.ID.String(), snap.VagueSampleIDs)
	}
	if !gotIDs[vagueB.ID.String()] {
		t.Errorf("VagueSampleIDs missing vagueB %s; got %v", vagueB.ID.String(), snap.VagueSampleIDs)
	}
	if gotIDs[healthy.ID.String()] {
		t.Errorf("VagueSampleIDs unexpectedly contains healthy %s", healthy.ID.String())
	}

	// Forgotten signal MUST surface a vague-task warning.
	foundSignal := false
	for _, sig := range snap.ForgottenSignals {
		if strings.Contains(sig, "flagged by validator.CheckVagueness") {
			foundSignal = true
			break
		}
	}
	if !foundSignal {
		t.Errorf("expected ForgottenSignals to mention vague tasks; got %v", snap.ForgottenSignals)
	}
}
