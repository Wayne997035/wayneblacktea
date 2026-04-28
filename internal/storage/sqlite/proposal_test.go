package sqlite_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

func openProposalStore(t *testing.T, dsn, workspaceID string) *sqlite.ProposalStore {
	t.Helper()
	d, err := sqlite.Open(context.Background(), dsn, workspaceID)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return sqlite.NewProposalStore(d)
}

func TestProposalStore_CreateGetListRoundTrip(t *testing.T) {
	s := openProposalStore(t, ":memory:", "")
	payload := []byte(`{"title":"Ship SQLite"}`)
	created, err := s.Create(context.Background(), proposal.CreateParams{
		Type:       proposal.TypeGoal,
		Payload:    payload,
		ProposedBy: "codex",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Status != string(proposal.StatusPending) || created.Type != string(proposal.TypeGoal) ||
		string(created.Payload) != string(payload) || !created.ProposedBy.Valid {
		t.Fatalf("unexpected created proposal: %+v", created)
	}

	got, err := s.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("wrong proposal from Get: %+v", got)
	}
	pending, err := s.ListPending(context.Background())
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != created.ID {
		t.Fatalf("unexpected pending list: %+v", pending)
	}
}

func TestProposalStore_NullOptionalFields(t *testing.T) {
	s := openProposalStore(t, ":memory:", "")
	created, err := s.Create(context.Background(), proposal.CreateParams{
		Type: proposal.TypeTask, Payload: []byte(`{"title":"task"}`),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.WorkspaceID.Valid || created.ProposedBy.Valid || created.ResolvedAt.Valid {
		t.Fatalf("expected NULL optionals, got %+v", created)
	}
}

func TestProposalStore_EmptyTable(t *testing.T) {
	s := openProposalStore(t, ":memory:", "")
	rows, err := s.ListPending(context.Background())
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected empty pending list, got %+v", rows)
	}
	_, err = s.Get(context.Background(), uuid.New())
	if !errors.Is(err, proposal.ErrNotFound) {
		t.Fatalf("expected proposal.ErrNotFound, got %v", err)
	}
}

func TestProposalStore_ConfirmProposalAcceptCompletedFlow(t *testing.T) {
	s := openProposalStore(t, ":memory:", "")
	created, err := s.Create(context.Background(), proposal.CreateParams{
		Type: proposal.TypeConcept, Payload: []byte(`{"title":"concept"}`),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	resolved, err := s.Resolve(context.Background(), created.ID, proposal.StatusAccepted)
	if err != nil {
		t.Fatalf("Resolve accepted: %v", err)
	}
	if resolved.Status != string(proposal.StatusAccepted) || !resolved.ResolvedAt.Valid {
		t.Fatalf("accept should complete proposal as accepted with resolved_at: %+v", resolved)
	}
	pending, err := s.ListPending(context.Background())
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("accepted proposal should leave pending list, got %+v", pending)
	}
}

func TestProposalStore_ConfirmProposalRejectRejectedFlow(t *testing.T) {
	s := openProposalStore(t, ":memory:", "")
	created, err := s.Create(context.Background(), proposal.CreateParams{
		Type: proposal.TypeTask, Payload: []byte(`{"title":"task"}`),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	resolved, err := s.Resolve(context.Background(), created.ID, proposal.StatusRejected)
	if err != nil {
		t.Fatalf("Resolve rejected: %v", err)
	}
	if resolved.Status != string(proposal.StatusRejected) || !resolved.ResolvedAt.Valid {
		t.Fatalf("reject should mark proposal rejected with resolved_at: %+v", resolved)
	}
	if _, err := s.Resolve(context.Background(), created.ID, proposal.StatusAccepted); !errors.Is(err, proposal.ErrNotFound) {
		t.Fatalf("re-resolve should return ErrNotFound, got %v", err)
	}
}

func TestProposalStore_AutoProposeConceptFromKnowledge(t *testing.T) {
	s := openProposalStore(t, ":memory:", "")
	item := &db.KnowledgeItem{
		ID:      uuid.New(),
		Type:    "til",
		Title:   "SQLite JSON tags",
		Content: "Store TEXT arrays as JSON",
		Tags:    []string{"sqlite"},
	}
	created, err := s.AutoProposeConceptFromKnowledge(context.Background(), item, "codex")
	if err != nil {
		t.Fatalf("AutoProposeConceptFromKnowledge: %v", err)
	}
	if created == nil || created.Type != string(proposal.TypeConcept) {
		t.Fatalf("expected concept proposal, got %+v", created)
	}
	var payload proposal.ConceptCandidate
	if err := json.Unmarshal(created.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.SourceItemID != item.ID.String() || payload.Tags[0] != "sqlite" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestProposalStore_WorkspaceIsolation(t *testing.T) {
	wsA, wsB := uuid.New().String(), uuid.New().String()
	dsn := "file:proposal-" + uuid.New().String() + "?mode=memory&cache=shared"
	storeA := openProposalStore(t, dsn, wsA)
	storeB := openProposalStore(t, dsn, wsB)

	created, err := storeA.Create(context.Background(), proposal.CreateParams{
		Type: proposal.TypeGoal, Payload: []byte(`{"title":"a"}`),
	})
	if err != nil {
		t.Fatalf("Create A: %v", err)
	}
	if !created.WorkspaceID.Valid {
		t.Fatalf("expected workspace_id: %+v", created)
	}
	rowsB, err := storeB.ListPending(context.Background())
	if err != nil {
		t.Fatalf("ListPending B: %v", err)
	}
	if len(rowsB) != 0 {
		t.Fatalf("workspace B should not see A proposals: %+v", rowsB)
	}
}

func TestProposalStore_ContextCanceled(t *testing.T) {
	s := openProposalStore(t, ":memory:", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.ListPending(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
