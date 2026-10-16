package handler_test

import (
	"context"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

// SQLite twins of the taskBackend cases (bodies live in
// proposal_handler_task_helpers_test.go).

// newSQLiteTaskBackend opens a fresh :memory: SQLite DB (migrations applied by
// wbtsqlite.Open) wired to the real sqlite accept adapter.
func newSQLiteTaskBackend(t *testing.T) taskBackend {
	t.Helper()
	ctx := context.Background()
	sdb, err := wbtsqlite.Open(ctx, ":memory:", "")
	if err != nil {
		t.Fatalf("wbtsqlite.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := sdb.Close(); err != nil {
			t.Logf("close sqlite: %v", err)
		}
	})

	propStore := wbtsqlite.NewProposalStore(sdb)
	deps := wbtsqlite.AcceptDeps{
		Proposal: propStore,
		GTD:      wbtsqlite.NewGTDStore(sdb),
		Learning: wbtsqlite.NewLearningStore(sdb),
		Decision: wbtsqlite.NewDecisionStore(sdb),
	}
	newAdapter := func(id uuid.UUID) proposal.AcceptAdapter { return wbtsqlite.NewAcceptAdapter(id, deps) }
	return taskBackend{
		name:           "sqlite",
		rawJSONPayload: true,
		newAdapter:     newAdapter,
		newHandler: func(wrap func(proposal.AcceptAdapter) proposal.AcceptAdapter) *handler.ProposalHandler {
			return handler.NewProposalHandler(propStore, &fakeProposalLearningStore{}).
				WithGoalProjectAccept(func(id uuid.UUID) proposal.AcceptAdapter {
					a := newAdapter(id)
					if wrap != nil {
						return wrap(a)
					}
					return a
				})
		},
		create: func(t *testing.T, typ proposal.Type, payload []byte) uuid.UUID {
			t.Helper()
			p, err := propStore.Create(ctx, proposal.CreateParams{Type: typ, Payload: payload})
			if err != nil {
				t.Fatalf("Create proposal: %v", err)
			}
			return p.ID
		},
		status: func(t *testing.T, id uuid.UUID) string {
			t.Helper()
			p, err := propStore.Get(ctx, id)
			if err != nil {
				t.Fatalf("Get proposal: %v", err)
			}
			return p.Status
		},
		taskCount: func(t *testing.T, title string) int {
			t.Helper()
			return scanOneInt(t, sdb.QueryRowContext(ctx, "SELECT count(*) FROM tasks WHERE title = ?", title).Scan)
		},
		taskID: func(t *testing.T, title string) string {
			t.Helper()
			return scanOneString(t, sdb.QueryRowContext(ctx, "SELECT id FROM tasks WHERE title = ?", title).Scan)
		},
		trackTitle: func(*testing.T, string) {},
	}
}
