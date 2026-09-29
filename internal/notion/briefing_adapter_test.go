package notion

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
	"github.com/google/uuid"
)

// errBriefingAdapterFake is the sentinel error every fake store below
// returns, so each error-wrap test (F0929-66) can assert both errors.Is
// pass-through and the adapter's distinguishing message prefix.
var errBriefingAdapterFake = errors.New("fake store failure")

// The 4 fakes below embed their StoreIface as a nil interface and override
// only the one method briefingStoresAdapter delegates to — same
// embedded-nil-interface convention as internal/cli/doctor_cmd_test.go's
// fakeErrStore (cited in the spec).
type fakeGTDStoreForBriefing struct{ gtd.StoreIface }

func (f *fakeGTDStoreForBriefing) Tasks(_ context.Context, _ *uuid.UUID) ([]db.Task, error) {
	return nil, errBriefingAdapterFake
}

func (f *fakeGTDStoreForBriefing) WeeklyProgress(_ context.Context) (int64, int64, error) {
	return 0, 0, errBriefingAdapterFake
}

type fakeLearningStoreForBriefing struct{ learning.StoreIface }

func (f *fakeLearningStoreForBriefing) DueReviews(_ context.Context, _ int) ([]learning.DueReview, error) {
	return nil, errBriefingAdapterFake
}

type fakeProposalStoreForBriefing struct{ proposal.StoreIface }

func (f *fakeProposalStoreForBriefing) ListPending(_ context.Context) ([]db.PendingProposal, error) {
	return nil, errBriefingAdapterFake
}

type fakeDecisionStoreForBriefing struct{ decision.StoreIface }

func (f *fakeDecisionStoreForBriefing) All(_ context.Context, _ int32) ([]db.Decision, error) {
	return nil, errBriefingAdapterFake
}

func TestBriefingStoresAdapter_Tasks_WrapsError(t *testing.T) {
	a := &briefingStoresAdapter{gtd: &fakeGTDStoreForBriefing{}}
	_, err := a.Tasks(context.Background(), nil)
	if err == nil || !errors.Is(err, errBriefingAdapterFake) {
		t.Fatalf("Tasks err = %v, want wrapped errBriefingAdapterFake", err)
	}
	if !strings.Contains(err.Error(), "gtd tasks:") {
		t.Errorf("err = %q, want prefix %q", err.Error(), "gtd tasks:")
	}
}

func TestBriefingStoresAdapter_WeeklyProgress_WrapsError(t *testing.T) {
	a := &briefingStoresAdapter{gtd: &fakeGTDStoreForBriefing{}}
	_, _, err := a.WeeklyProgress(context.Background())
	if err == nil || !errors.Is(err, errBriefingAdapterFake) {
		t.Fatalf("WeeklyProgress err = %v, want wrapped errBriefingAdapterFake", err)
	}
	if !strings.Contains(err.Error(), "weekly progress:") {
		t.Errorf("err = %q, want prefix %q", err.Error(), "weekly progress:")
	}
}

func TestBriefingStoresAdapter_DueReviews_WrapsError(t *testing.T) {
	a := &briefingStoresAdapter{learning: &fakeLearningStoreForBriefing{}}
	_, err := a.DueReviews(context.Background(), 10)
	if err == nil || !errors.Is(err, errBriefingAdapterFake) {
		t.Fatalf("DueReviews err = %v, want wrapped errBriefingAdapterFake", err)
	}
	if !strings.Contains(err.Error(), "due reviews:") {
		t.Errorf("err = %q, want prefix %q", err.Error(), "due reviews:")
	}
}

func TestBriefingStoresAdapter_ListPending_WrapsError(t *testing.T) {
	a := &briefingStoresAdapter{proposal: &fakeProposalStoreForBriefing{}}
	_, err := a.ListPending(context.Background())
	if err == nil || !errors.Is(err, errBriefingAdapterFake) {
		t.Fatalf("ListPending err = %v, want wrapped errBriefingAdapterFake", err)
	}
	if !strings.Contains(err.Error(), "list pending proposals:") {
		t.Errorf("err = %q, want prefix %q", err.Error(), "list pending proposals:")
	}
}

func TestBriefingStoresAdapter_All_WrapsError(t *testing.T) {
	a := &briefingStoresAdapter{decision: &fakeDecisionStoreForBriefing{}}
	_, err := a.All(context.Background(), 10)
	if err == nil || !errors.Is(err, errBriefingAdapterFake) {
		t.Fatalf("All err = %v, want wrapped errBriefingAdapterFake", err)
	}
	if !strings.Contains(err.Error(), "all decisions:") {
		t.Errorf("err = %q, want prefix %q", err.Error(), "all decisions:")
	}
}

// The 4 sentinel fakes below stand in for storage.ServerStores' 4 relevant
// accessor return values in TestNewBriefingStores_WiresCorrectFields — each
// is its own distinct type, embedding a nil StoreIface, so identity
// (`!= sentinelX`) can prove NewBriefingStores wired the right field from
// the right accessor instead of two fields getting silently transposed.
type (
	sentinelGTDStore      struct{ gtd.StoreIface }
	sentinelLearningStore struct{ learning.StoreIface }
	sentinelProposalStore struct{ proposal.StoreIface }
	sentinelDecisionStore struct{ decision.StoreIface }
)

// fakeServerStoresForBriefing embeds storage.ServerStores as a nil interface
// (embedded-nil-interface convention) and overrides only the 4 accessors
// NewBriefingStores reads.
type fakeServerStoresForBriefing struct {
	storage.ServerStores
	gtdStore      *sentinelGTDStore
	learningStore *sentinelLearningStore
	proposalStore *sentinelProposalStore
	decisionStore *sentinelDecisionStore
}

func (f *fakeServerStoresForBriefing) GTD() gtd.StoreIface           { return f.gtdStore }
func (f *fakeServerStoresForBriefing) Learning() learning.StoreIface { return f.learningStore }
func (f *fakeServerStoresForBriefing) Proposal() proposal.StoreIface { return f.proposalStore }
func (f *fakeServerStoresForBriefing) Decision() decision.StoreIface { return f.decisionStore }

// TestNewBriefingStores_WiresCorrectFields verifies NewBriefingStores (F0929-66)
// assigns each of briefingStoresAdapter's 4 fields from the correspondingly
// named storage.ServerStores accessor, not a transposed one — a transposed
// assignment (e.g. learning: stores.Proposal()) would compile (both are
// interfaces the struct fields don't statically distinguish by name) and
// only surface at runtime.
func TestNewBriefingStores_WiresCorrectFields(t *testing.T) {
	fake := &fakeServerStoresForBriefing{
		gtdStore:      &sentinelGTDStore{},
		learningStore: &sentinelLearningStore{},
		proposalStore: &sentinelProposalStore{},
		decisionStore: &sentinelDecisionStore{},
	}

	got, ok := NewBriefingStores(fake).(*briefingStoresAdapter)
	if !ok {
		t.Fatalf("NewBriefingStores returned %T, want *briefingStoresAdapter", NewBriefingStores(fake))
	}
	if got.gtd != gtd.StoreIface(fake.gtdStore) {
		t.Errorf("gtd field = %v, want fake.gtdStore (%v)", got.gtd, fake.gtdStore)
	}
	if got.learning != learning.StoreIface(fake.learningStore) {
		t.Errorf("learning field = %v, want fake.learningStore (%v)", got.learning, fake.learningStore)
	}
	if got.proposal != proposal.StoreIface(fake.proposalStore) {
		t.Errorf("proposal field = %v, want fake.proposalStore (%v)", got.proposal, fake.proposalStore)
	}
	if got.decision != decision.StoreIface(fake.decisionStore) {
		t.Errorf("decision field = %v, want fake.decisionStore (%v)", got.decision, fake.decisionStore)
	}
}
