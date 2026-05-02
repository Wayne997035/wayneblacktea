package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func makeConsolidationDeps(
	gtdStore *stubGTDStore,
	propStore *stubProposalStore,
	reflector *stubReflector,
) consolidationDeps {
	return consolidationDeps{
		gtd:       gtdStore,
		proposal:  propStore,
		reflector: reflector,
	}
}

// makeActivities builds N activity_log rows for the given actor.
func makeActivities(actor string, n int) []db.ActivityLog {
	out := make([]db.ActivityLog, n)
	now := time.Now()
	for i := range out {
		out[i] = db.ActivityLog{
			ID:        uuid.New(),
			Actor:     actor,
			Action:    "action",
			CreatedAt: pgtype.Timestamptz{Time: now, Valid: true},
		}
	}
	return out
}

var errConsolidationStoreFailure = errors.New("simulated consolidation store failure")

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestRunConsolidation_HappyPath verifies that when one cluster has ≥5 activities
// and the reflector returns a proposal, one pending_proposals row is created.
func TestRunConsolidation_HappyPath(t *testing.T) {
	activities := makeActivities("wayne/wayneblacktea", 6)
	gtdStore := &stubGTDStore{activities: activities}
	propStore := &stubProposalStore{}
	reflector := &stubReflector{
		proposals: []ai.KnowledgeProposal{
			{Title: "Consolidated: wayneblacktea patterns", Content: "6 activities merged into one takeaway"},
		},
	}

	deps := makeConsolidationDeps(gtdStore, propStore, reflector)
	runConsolidation(deps)

	if len(propStore.created) != 1 {
		t.Fatalf("expected 1 proposal created, got %d", len(propStore.created))
	}
	if propStore.created[0].Type != string(proposal.TypeKnowledge) {
		t.Errorf("proposal type = %q, want %q", propStore.created[0].Type, proposal.TypeKnowledge)
	}
}

// TestRunConsolidation_BelowMinClusterSize verifies that clusters with < 5
// activities are not sent to the AI and no proposals are created.
func TestRunConsolidation_BelowMinClusterSize(t *testing.T) {
	activities := makeActivities("wayne/wayneblacktea", 4) // below threshold
	gtdStore := &stubGTDStore{activities: activities}
	propStore := &stubProposalStore{}
	// reflector must not be called
	reflector := &stubReflector{propErr: errors.New("should not be called")}

	deps := makeConsolidationDeps(gtdStore, propStore, reflector)
	runConsolidation(deps)

	if len(propStore.created) != 0 {
		t.Errorf("expected 0 proposals for cluster below min size, got %d", len(propStore.created))
	}
}

// TestRunConsolidation_EmptyActivityLog verifies that when there are no
// activities the job exits early without calling the reflector.
func TestRunConsolidation_EmptyActivityLog(t *testing.T) {
	gtdStore := &stubGTDStore{} // no activities
	propStore := &stubProposalStore{}
	reflector := &stubReflector{propErr: errors.New("should not be called")}

	deps := makeConsolidationDeps(gtdStore, propStore, reflector)
	runConsolidation(deps)

	if len(propStore.created) != 0 {
		t.Errorf("expected 0 proposals when activity log is empty, got %d", len(propStore.created))
	}
}

// TestRunConsolidation_ActivityStoreError verifies that a DB error from
// ListActivityLogsSince is handled gracefully (no panic, no proposals).
func TestRunConsolidation_ActivityStoreError(t *testing.T) {
	gtdStore := &stubGTDStore{actErr: errConsolidationStoreFailure}
	propStore := &stubProposalStore{}
	reflector := &stubReflector{}

	deps := makeConsolidationDeps(gtdStore, propStore, reflector)
	runConsolidation(deps) // must not panic

	if len(propStore.created) != 0 {
		t.Errorf("expected 0 proposals on store error, got %d", len(propStore.created))
	}
}

// TestRunConsolidation_ReflectorError verifies that an AI failure for one
// cluster is logged and skipped without stopping processing of other clusters.
func TestRunConsolidation_ReflectorError(t *testing.T) {
	activities := makeActivities("wayne/repo-a", 6)
	gtdStore := &stubGTDStore{activities: activities}
	propStore := &stubProposalStore{}
	reflector := &stubReflector{propErr: errors.New("haiku rate limit")}

	deps := makeConsolidationDeps(gtdStore, propStore, reflector)
	runConsolidation(deps)

	if len(propStore.created) != 0 {
		t.Errorf("expected 0 proposals on reflector error, got %d", len(propStore.created))
	}
}

// TestClusterActivities_MultipleActors verifies that clustering groups by actor
// prefix and only returns clusters meeting the minimum size threshold.
func TestClusterActivities_MultipleActors(t *testing.T) {
	activities := append(
		makeActivities("wayne/repo-a", 6),    // cluster A: 6 → included
		makeActivities("wayne/repo-b", 3)..., // cluster B: 3 → excluded
	)

	clusters := clusterActivities(activities)

	if len(clusters) != 1 {
		t.Fatalf("expected 1 cluster (≥5), got %d", len(clusters))
	}
	if clusters[0].key != "wayne" {
		t.Errorf("cluster key = %q, want %q", clusters[0].key, "wayne")
	}
	if len(clusters[0].activities) != 9 {
		// Both actor prefixes are "wayne" so they merge into one cluster of 6+3=9
		t.Errorf("cluster size = %d, want 9", len(clusters[0].activities))
	}
}

// TestActorKey_SplitsOnSlash verifies the actor key extractor.
func TestActorKey_SplitsOnSlash(t *testing.T) {
	cases := []struct {
		actor string
		want  string
	}{
		{"wayne/wayneblacktea", "wayne"},
		{"wayne", "wayne"},
		{"org/repo/extra", "org"},
		{"", ""},
	}
	for _, tc := range cases {
		got := actorKey(tc.actor)
		if got != tc.want {
			t.Errorf("actorKey(%q) = %q, want %q", tc.actor, got, tc.want)
		}
	}
}

// TestSaturdayReflection_NilDeps verifies that the Scheduler methods are safe
// to call when reflectionDeps / consolidDeps are nil (no CLAUDE_API_KEY set).
func TestSaturdayReflection_NilDeps(t *testing.T) {
	learningStore := &stubLearningStore{}
	sc, err := New(learningStore, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	// Must not panic when deps are nil.
	sc.saturdayReflection()
	sc.saturdayConsolidation()
}

// TestRunConsolidation_ProposalStoreError verifies that a DB write failure for
// one proposal is logged and skipped without aborting the whole job.
func TestRunConsolidation_ProposalStoreError(t *testing.T) {
	activities := makeActivities("wayne/repo-a", 6)
	gtdStore := &stubGTDStore{activities: activities}
	propStore := &stubProposalStore{createErr: errors.New("DB write failed")}
	reflector := &stubReflector{
		proposals: []ai.KnowledgeProposal{
			{Title: "t", Content: "c"},
		},
	}

	deps := makeConsolidationDeps(gtdStore, propStore, reflector)
	runConsolidation(deps) // must not panic

	if len(propStore.created) != 0 {
		t.Errorf("expected 0 proposals on DB write error, got %d", len(propStore.created))
	}
}

// Ensure stubGTDStore.ListActivityLogsSince satisfies the interface at compile time.
var _ interface {
	ListActivityLogsSince(context.Context, time.Time, int32) ([]db.ActivityLog, error)
} = (*stubGTDStore)(nil)
