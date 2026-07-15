package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ---------------------------------------------------------------------------
// stubKnowledgeStore
// ---------------------------------------------------------------------------

type stubKnowledgeStore struct {
	items   []db.KnowledgeItem
	listErr error
}

func (s *stubKnowledgeStore) List(_ context.Context, _, _ int) ([]db.KnowledgeItem, error) {
	return s.items, s.listErr
}

func (s *stubKnowledgeStore) AddItem(_ context.Context, _ knowledge.AddItemParams) (*db.KnowledgeItem, error) {
	return nil, nil
}

func (s *stubKnowledgeStore) Search(_ context.Context, _ string, _ int) ([]db.KnowledgeItem, error) {
	return nil, nil
}

func (s *stubKnowledgeStore) SearchReadOnly(_ context.Context, _ string, _ int) ([]db.KnowledgeItem, error) {
	return nil, nil
}

func (s *stubKnowledgeStore) SearchCoarse(_ context.Context, _ string, _ int) ([]db.KnowledgeItem, error) {
	return nil, nil
}

func (s *stubKnowledgeStore) GetByID(_ context.Context, _ uuid.UUID) (*db.KnowledgeItem, error) {
	return nil, nil
}

func (s *stubKnowledgeStore) UpdateLearningValue(_ context.Context, _ uuid.UUID, _ int) error {
	return nil
}

func (s *stubKnowledgeStore) SearchByCosine(_ context.Context, _ []float32, _ int) ([]db.KnowledgeItem, error) {
	return nil, nil
}

func (s *stubKnowledgeStore) ListChildren(_ context.Context, _ uuid.UUID) ([]*db.KnowledgeItem, error) {
	return nil, nil
}

func (s *stubKnowledgeStore) ListRoots(_ context.Context) ([]*db.KnowledgeItem, error) {
	return nil, nil
}

func (s *stubKnowledgeStore) ListByProjectID(_ context.Context, _ uuid.UUID, _ int) ([]db.KnowledgeItem, error) {
	return nil, nil
}

func (s *stubKnowledgeStore) ListByTaskID(_ context.Context, _ uuid.UUID, _ int) ([]db.KnowledgeItem, error) {
	return nil, nil
}

// Ensure stubKnowledgeStore satisfies the interface at compile time.
var _ knowledge.StoreIface = (*stubKnowledgeStore)(nil)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// makeKnowledgeItems builds N knowledge items sharing the given tags.
func makeKnowledgeItems(tags []string, n int) []db.KnowledgeItem {
	out := make([]db.KnowledgeItem, n)
	now := time.Now()
	for i := range out {
		out[i] = db.KnowledgeItem{
			ID:        uuid.New(),
			Title:     "item",
			Tags:      tags,
			CreatedAt: pgtype.Timestamptz{Time: now, Valid: true},
		}
	}
	return out
}

func makeKnowledgeConsolidationDeps(
	ks *stubKnowledgeStore,
	ps *stubProposalStore,
	reflector *stubReflector,
) knowledgeConsolidationDeps {
	return knowledgeConsolidationDeps{
		knowledge: ks,
		proposal:  ps,
		reflector: reflector,
	}
}

var errKCStoreFailure = errors.New("simulated knowledge consolidation store failure")

// ---------------------------------------------------------------------------
// Tests: runKnowledgeConsolidation
// ---------------------------------------------------------------------------

// TestRunKnowledgeConsolidation_HappyPath verifies that when a qualifying
// cluster exists and the reflector returns a proposal, at least one
// pending_proposals row of type='knowledge' is created.
//
// The greedy seed-based clustering algorithm generates one cluster per item
// that qualifies (each item seeds its own cluster). With N items all sharing
// the same tags, N clusters are produced — each leading to one proposal from
// the reflector.
func TestRunKnowledgeConsolidation_HappyPath(t *testing.T) {
	items := makeKnowledgeItems([]string{"go", "testing", "testcontainers"}, 4)
	ks := &stubKnowledgeStore{items: items}
	ps := &stubProposalStore{}
	reflector := &stubReflector{
		proposals: []ai.KnowledgeProposal{
			{Title: "Go testing patterns", Content: "merged takeaway from 4 items"},
		},
	}

	deps := makeKnowledgeConsolidationDeps(ks, ps, reflector)
	runKnowledgeConsolidation(deps)

	if len(ps.created) == 0 {
		t.Fatal("expected at least 1 proposal created, got 0")
	}
	if ps.created[0].Type != string(proposal.TypeKnowledge) {
		t.Errorf("proposal type = %q, want %q", ps.created[0].Type, proposal.TypeKnowledge)
	}
}

// TestRunKnowledgeConsolidation_EmptyStore verifies that when there are no
// knowledge items the job exits early without calling the reflector.
func TestRunKnowledgeConsolidation_EmptyStore(t *testing.T) {
	ks := &stubKnowledgeStore{} // no items
	ps := &stubProposalStore{}
	reflector := &stubReflector{propErr: errors.New("should not be called")}

	deps := makeKnowledgeConsolidationDeps(ks, ps, reflector)
	runKnowledgeConsolidation(deps)

	if len(ps.created) != 0 {
		t.Errorf("expected 0 proposals when store is empty, got %d", len(ps.created))
	}
}

// TestRunKnowledgeConsolidation_AllItemsBeyondLookback verifies that items
// older than the 30-day lookback window are filtered out and no proposals
// are created.
func TestRunKnowledgeConsolidation_AllItemsBeyondLookback(t *testing.T) {
	old := time.Now().Add(-31 * 24 * time.Hour)
	items := []db.KnowledgeItem{
		{ID: uuid.New(), Title: "old", Tags: []string{"go", "test"}, CreatedAt: pgtype.Timestamptz{Time: old, Valid: true}},
		{ID: uuid.New(), Title: "old2", Tags: []string{"go", "test"}, CreatedAt: pgtype.Timestamptz{Time: old, Valid: true}},
		{ID: uuid.New(), Title: "old3", Tags: []string{"go", "test"}, CreatedAt: pgtype.Timestamptz{Time: old, Valid: true}},
	}
	ks := &stubKnowledgeStore{items: items}
	ps := &stubProposalStore{}
	reflector := &stubReflector{propErr: errors.New("should not be called")}

	deps := makeKnowledgeConsolidationDeps(ks, ps, reflector)
	runKnowledgeConsolidation(deps)

	if len(ps.created) != 0 {
		t.Errorf("expected 0 proposals for all-stale items, got %d", len(ps.created))
	}
}

// TestRunKnowledgeConsolidation_KnowledgeStoreError verifies that a DB error
// from List is handled gracefully (no panic, no proposals).
func TestRunKnowledgeConsolidation_KnowledgeStoreError(t *testing.T) {
	ks := &stubKnowledgeStore{listErr: errKCStoreFailure}
	ps := &stubProposalStore{}
	reflector := &stubReflector{}

	deps := makeKnowledgeConsolidationDeps(ks, ps, reflector)
	runKnowledgeConsolidation(deps) // must not panic

	if len(ps.created) != 0 {
		t.Errorf("expected 0 proposals on store error, got %d", len(ps.created))
	}
}

// TestRunKnowledgeConsolidation_ReflectorError verifies that an AI failure
// is logged and skipped without stopping the job.
func TestRunKnowledgeConsolidation_ReflectorError(t *testing.T) {
	items := makeKnowledgeItems([]string{"go", "testing"}, 3)
	ks := &stubKnowledgeStore{items: items}
	ps := &stubProposalStore{}
	reflector := &stubReflector{propErr: errors.New("haiku rate limit")}

	deps := makeKnowledgeConsolidationDeps(ks, ps, reflector)
	runKnowledgeConsolidation(deps)

	if len(ps.created) != 0 {
		t.Errorf("expected 0 proposals on reflector error, got %d", len(ps.created))
	}
}

// TestRunKnowledgeConsolidation_ProposalStoreError verifies that a DB write
// failure for one proposal is logged and skipped without aborting.
func TestRunKnowledgeConsolidation_ProposalStoreError(t *testing.T) {
	items := makeKnowledgeItems([]string{"go", "testing"}, 3)
	ks := &stubKnowledgeStore{items: items}
	ps := &stubProposalStore{createErr: errors.New("DB write failed")}
	reflector := &stubReflector{
		proposals: []ai.KnowledgeProposal{
			{Title: "t", Content: "c"},
		},
	}

	deps := makeKnowledgeConsolidationDeps(ks, ps, reflector)
	runKnowledgeConsolidation(deps) // must not panic

	if len(ps.created) != 0 {
		t.Errorf("expected 0 proposals on DB write error, got %d", len(ps.created))
	}
}

// ---------------------------------------------------------------------------
// Tests: clusterKnowledgeByTags
// ---------------------------------------------------------------------------

// TestClusterKnowledgeByTags_BelowMinSharedTags verifies that items sharing
// only 1 tag do NOT form a qualifying cluster.
func TestClusterKnowledgeByTags_BelowMinSharedTags(t *testing.T) {
	items := makeKnowledgeItems([]string{"go"}, 5) // only 1 shared tag
	clusters := clusterKnowledgeByTags(items)
	if len(clusters) != 0 {
		t.Errorf("expected 0 clusters with only 1 shared tag, got %d", len(clusters))
	}
}

// TestClusterKnowledgeByTags_BelowMinClusterSize verifies that only 2 items
// sharing ≥2 tags do NOT form a qualifying cluster (min size is 3).
func TestClusterKnowledgeByTags_BelowMinClusterSize(t *testing.T) {
	items := makeKnowledgeItems([]string{"go", "testing"}, 2) // only 2 items
	clusters := clusterKnowledgeByTags(items)
	if len(clusters) != 0 {
		t.Errorf("expected 0 clusters with only 2 items (below min size 3), got %d", len(clusters))
	}
}

// TestClusterKnowledgeByTags_QualifyingCluster verifies that ≥3 items
// sharing ≥2 tags form at least one qualifying cluster, and every cluster
// satisfies the shared-tag minimum.
//
// The greedy seed-based algorithm seeds one cluster per qualifying item, so
// with N items all sharing the same tags, N clusters are returned (each
// seeded by a different item but all containing the full set of items).
func TestClusterKnowledgeByTags_QualifyingCluster(t *testing.T) {
	items := makeKnowledgeItems([]string{"go", "testing", "ci"}, 3)
	clusters := clusterKnowledgeByTags(items)
	if len(clusters) == 0 {
		t.Fatal("expected at least 1 qualifying cluster, got 0")
	}
	for _, cl := range clusters {
		if len(cl.sharedTags) < knowledgeConsolidationMinSharedTags {
			t.Errorf("cluster shared tags %v below minimum %d", cl.sharedTags, knowledgeConsolidationMinSharedTags)
		}
		if len(cl.items) < knowledgeConsolidationMinClusterSize {
			t.Errorf("cluster size %d below minimum %d", len(cl.items), knowledgeConsolidationMinClusterSize)
		}
	}
}

// TestClusterKnowledgeByTags_EmptyItems verifies no panic on empty input.
func TestClusterKnowledgeByTags_EmptyItems(t *testing.T) {
	clusters := clusterKnowledgeByTags(nil)
	if len(clusters) != 0 {
		t.Errorf("expected 0 clusters for nil input, got %d", len(clusters))
	}
}

// ---------------------------------------------------------------------------
// Tests: buildKnowledgeConsolidDeps
// ---------------------------------------------------------------------------

// TestBuildKnowledgeConsolidDeps_AllNilReturnsNil verifies that when any
// required dep is nil, buildKnowledgeConsolidDeps returns nil.
func TestBuildKnowledgeConsolidDeps_AllNilReturnsNil(t *testing.T) {
	ks := &stubKnowledgeStore{}
	ps := &stubProposalStore{}
	r := &stubReflector{}

	cases := []struct {
		name      string
		reflector ai.ReflectorIface
		proposal  proposal.StoreIface
		knowledge knowledge.StoreIface
	}{
		{"nil reflector", nil, ps, ks},
		{"nil proposal", r, nil, ks},
		{"nil knowledge", r, ps, nil},
		{"all nil", nil, nil, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildKnowledgeConsolidDeps(tc.reflector, tc.proposal, tc.knowledge)
			if got != nil {
				t.Errorf("expected nil deps, got %+v", got)
			}
		})
	}
}

// TestBuildKnowledgeConsolidDeps_AllSet verifies that all deps populated
// returns a non-nil struct with correct fields.
func TestBuildKnowledgeConsolidDeps_AllSet(t *testing.T) {
	ks := &stubKnowledgeStore{}
	ps := &stubProposalStore{}
	r := &stubReflector{}

	got := buildKnowledgeConsolidDeps(r, ps, ks)
	if got == nil {
		t.Fatal("expected non-nil deps, got nil")
	}
	if got.knowledge != ks {
		t.Error("knowledge store not set correctly")
	}
	if got.proposal != ps {
		t.Error("proposal store not set correctly")
	}
	if got.reflector != r {
		t.Error("reflector not set correctly")
	}
}

// TestSundayKnowledgeConsolidation_NilDeps verifies that the scheduler method
// is safe to call when knowledgeConsolidDeps is nil.
func TestSundayKnowledgeConsolidation_NilDeps(t *testing.T) {
	sc, err := New(&stubLearningStore{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	// Must not panic when deps are nil.
	sc.sundayKnowledgeConsolidation()
}

// TestRegisterSundayKnowledgeConsolidation_NilDeps_JobNotRegistered verifies
// that when knowledgeConsolidDeps is nil, the sunday-knowledge-consolidation
// job is NOT registered with gocron.
func TestRegisterSundayKnowledgeConsolidation_NilDeps_JobNotRegistered(t *testing.T) {
	sc, err := New(&stubLearningStore{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	for _, j := range sc.s.Jobs() {
		if j.Name() == "sunday-knowledge-consolidation" {
			t.Errorf("sunday-knowledge-consolidation was registered with nil deps; expected skip")
		}
	}
}
