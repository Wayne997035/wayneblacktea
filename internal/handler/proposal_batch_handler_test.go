package handler_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// fakeProposalListStore satisfies the proposalListStore interface for unit tests.
type fakeProposalListStore struct {
	pending       []db.PendingProposal
	getResult     *db.PendingProposal
	resolveResult *db.PendingProposal
	err           error
	getErr        error
}

func (f *fakeProposalListStore) ListPending(_ context.Context) ([]db.PendingProposal, error) {
	return f.pending, f.err
}

func (f *fakeProposalListStore) Get(_ context.Context, _ uuid.UUID) (*db.PendingProposal, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.getResult, f.err
}

func (f *fakeProposalListStore) Resolve(_ context.Context, _ uuid.UUID, _ proposal.Status) (*db.PendingProposal, error) {
	return f.resolveResult, f.err
}

// fakeLearningStoreForProposal satisfies the proposalConceptStore interface.
type fakeLearningStoreForProposal struct{}

func (f *fakeLearningStoreForProposal) CreateConcept(_ context.Context, _, _ string, _ []string) (*db.Concept, error) {
	return &db.Concept{}, nil
}

func newProposalHandler(store *fakeProposalListStore) *handler.ProposalHandler {
	return handler.NewProposalHandler(store, &fakeLearningStoreForProposal{})
}

// TestConfirmBatch_HappyPath verifies that valid accept/reject requests return 200.
func TestConfirmBatch_HappyPath(t *testing.T) {
	id1, id2 := uuid.New(), uuid.New()
	cases := []struct {
		name   string
		body   string
		action string
	}{
		{
			name:   "accept two proposals",
			body:   `{"ids":["` + id1.String() + `","` + id2.String() + `"],"action":"accept"}`,
			action: "accept",
		},
		{
			name:   "reject two proposals",
			body:   `{"ids":["` + id1.String() + `","` + id2.String() + `"],"action":"reject"}`,
			action: "reject",
		},
		{
			name: "single ID accept",
			body: `{"ids":["` + id1.String() + `"],"action":"accept"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			h := newProposalHandler(&fakeProposalListStore{})
			e.POST("/api/proposals/confirm-batch", h.ConfirmBatch)
			rec := performRequest(e, http.MethodPost, "/api/proposals/confirm-batch", tc.body)
			if rec.Code != http.StatusOK {
				t.Errorf("got %d, want 200 (body: %s)", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestConfirmBatch_ValidationErrors verifies input validation rejects bad requests.
func TestConfirmBatch_ValidationErrors(t *testing.T) {
	validID := uuid.New().String()
	cases := []struct {
		name     string
		body     string
		wantCode int
	}{
		{
			name:     "invalid action → 400",
			body:     `{"ids":["` + validID + `"],"action":"destroy"}`,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "empty ids array → 400",
			body:     `{"ids":[],"action":"accept"}`,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "ids exceeds 100 → 400",
			body:     buildBigIDs(101) + `,"action":"accept"}`,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "invalid UUID in ids → 400",
			body:     `{"ids":["not-a-uuid"],"action":"accept"}`,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "missing action → 400",
			body:     `{"ids":["` + validID + `"]}`,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "invalid JSON → 400",
			body:     `{bad`,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "null ids → 400",
			body:     `{"ids":null,"action":"accept"}`,
			wantCode: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			h := newProposalHandler(&fakeProposalListStore{})
			e.POST("/api/proposals/confirm-batch", h.ConfirmBatch)
			rec := performRequest(e, http.MethodPost, "/api/proposals/confirm-batch", tc.body)
			if rec.Code != tc.wantCode {
				t.Errorf("got %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

// TestConfirmBatch_StoreError verifies that a per-item resolve error returns 200
// with an error entry (batch API: per-item failure is non-fatal for the overall request).
func TestConfirmBatch_StoreError(t *testing.T) {
	e := echo.New()
	store := &fakeProposalListStore{err: errors.New("db exploded")}
	h := newProposalHandler(store)
	e.POST("/api/proposals/confirm-batch", h.ConfirmBatch)
	rec := performRequest(e, http.MethodPost, "/api/proposals/confirm-batch",
		`{"ids":["`+uuid.New().String()+`"],"action":"accept"}`)
	if rec.Code != http.StatusOK {
		t.Errorf("got %d, want 200 — batch API returns per-item errors, not 500 (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestConfirmBatch_PartialFailureReturns200 verifies that when Resolve returns
// ErrNotFound for all items the batch still returns 200 with skipped entries
// (per-item failure model: HTTP 200 always, error per entry).
func TestConfirmBatch_PartialFailureReturns200(t *testing.T) {
	e := echo.New()
	store := &fakeProposalListStore{err: proposal.ErrNotFound}
	h := newProposalHandler(store)
	e.POST("/api/proposals/confirm-batch", h.ConfirmBatch)

	body := `{"ids":["` + uuid.New().String() + `","` + uuid.New().String() + `"],"action":"reject"}`
	rec := performRequest(e, http.MethodPost, "/api/proposals/confirm-batch", body)
	if rec.Code != http.StatusOK {
		t.Errorf("got %d, want 200 (per-item ErrNotFound still returns 200): %s", rec.Code, rec.Body.String())
	}
}

// TestConfirmBatch_ExactlyMaxIDs verifies that exactly 100 IDs are accepted.
func TestConfirmBatch_ExactlyMaxIDs(t *testing.T) {
	e := echo.New()
	h := newProposalHandler(&fakeProposalListStore{})
	e.POST("/api/proposals/confirm-batch", h.ConfirmBatch)
	rec := performRequest(e, http.MethodPost, "/api/proposals/confirm-batch",
		buildBigIDs(100)+`,"action":"accept"}`)
	if rec.Code != http.StatusOK {
		t.Errorf("exactly 100 IDs should return 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

// buildBigIDs creates a JSON object prefix with the given number of valid UUIDs
// in the ids array (caller must append `,"action":"..."}` to complete the object).
func buildBigIDs(n int) string {
	var sb strings.Builder
	sb.WriteString(`{"ids":[`)
	for i := range n {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteByte('"')
		sb.WriteString(uuid.New().String())
		sb.WriteByte('"')
	}
	sb.WriteByte(']')
	return sb.String()
}
