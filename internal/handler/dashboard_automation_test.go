package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/completioncandidate"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"
)

// --- fakes ---

type fakeCandidateStore struct {
	detected []completioncandidate.Candidate
	err      error
}

func (f *fakeCandidateStore) DetectAndUpsert(
	_ context.Context, _ completioncandidate.DetectParams,
) ([]completioncandidate.Candidate, error) {
	return f.detected, f.err
}

func (f *fakeCandidateStore) ListPendingCandidates(
	_ context.Context, _ *uuid.UUID,
) ([]completioncandidate.Candidate, error) {
	return f.detected, f.err
}

type fakeHandoffStore struct {
	handoff *db.SessionHandoff
	err     error
}

func (f *fakeHandoffStore) LatestHandoff(_ context.Context) (*db.SessionHandoff, error) {
	return f.handoff, f.err
}

type fakeProposalStoreForAutomation struct {
	count int
	err   error
}

func (f *fakeProposalStoreForAutomation) ListPending(_ context.Context) ([]db.PendingProposal, error) {
	if f.err != nil {
		return nil, f.err
	}
	return make([]db.PendingProposal, f.count), nil
}

// TestGetAutomationHealth table-driven tests for the automation-health endpoint.
func TestGetAutomationHealth(t *testing.T) {
	cases := []struct {
		name        string
		candidates  []completioncandidate.Candidate
		candErr     error
		proposals   int
		propErr     error
		handoff     *db.SessionHandoff
		wantStatus  int
		wantStale   int
		wantCands   int
		wantPending int
		wantMissing bool
	}{
		{
			name:        "empty — no candidates, no proposals",
			candidates:  []completioncandidate.Candidate{},
			proposals:   0,
			handoff:     nil,
			wantStatus:  http.StatusOK,
			wantStale:   0,
			wantCands:   0,
			wantPending: 0,
			wantMissing: false,
		},
		{
			name: "candidates present — stale + gap split",
			candidates: []completioncandidate.Candidate{
				{
					ID:           uuid.New(),
					TaskID:       uuid.New(),
					Reason:       completioncandidate.ReasonStaleInProgress,
					Confidence:   completioncandidate.ConfidenceMedium,
					Status:       completioncandidate.StatusPending,
					DetectedAt:   time.Now().UTC(),
					EvidenceRefs: []string{"stale since 2020-01-01"},
				},
				{
					ID:           uuid.New(),
					TaskID:       uuid.New(),
					Reason:       completioncandidate.ReasonFinishWorkGap,
					Confidence:   completioncandidate.ConfidenceHigh,
					Status:       completioncandidate.StatusPending,
					DetectedAt:   time.Now().UTC(),
					EvidenceRefs: []string{"work_session completed at 2020-01-02"},
				},
			},
			proposals:   3,
			wantStatus:  http.StatusOK,
			wantStale:   1,
			wantCands:   1,
			wantPending: 3,
			wantMissing: false,
		},
		{
			name:       "stale handoff — missing_handoff=true",
			candidates: []completioncandidate.Candidate{},
			proposals:  0,
			handoff: &db.SessionHandoff{
				ID:     uuid.New(),
				Intent: "old handoff",
				CreatedAt: pgtype.Timestamptz{
					Time:  time.Now().UTC().Add(-49 * time.Hour),
					Valid: true,
				},
				ResolvedAt: pgtype.Timestamptz{Valid: false},
			},
			wantStatus:  http.StatusOK,
			wantStale:   0,
			wantCands:   0,
			wantPending: 0,
			wantMissing: true,
		},
		{
			name:       "detection store error — 500",
			candidates: nil,
			candErr:    errors.New("db error"),
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "proposal store error — 500",
			candidates: []completioncandidate.Candidate{},
			proposals:  0,
			propErr:    errors.New("proposal db error"),
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/dashboard/automation-health", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			cs := &fakeCandidateStore{detected: tc.candidates, err: tc.candErr}
			ps := &fakeProposalStoreForAutomation{count: tc.proposals, err: tc.propErr}
			hs := &fakeHandoffStore{handoff: tc.handoff}

			h := handler.NewDashboardHandler(nil, nil, ps)
			h.SetCandidateStore(cs)
			h.SetHandoffStore(hs)

			if err := h.GetAutomationHealth(c); err != nil {
				t.Fatalf("GetAutomationHealth returned unexpected error: %v", err)
			}

			if rec.Code != tc.wantStatus {
				t.Errorf("status: got %d, want %d (body: %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}

			if tc.wantStatus != http.StatusOK {
				return
			}

			var body struct {
				StaleInProgress       []json.RawMessage `json:"stale_in_progress"`
				CompletionCandidates  []json.RawMessage `json:"completion_candidates"`
				PendingProposalsCount int               `json:"pending_proposals_count"`
				MissingHandoff        bool              `json:"missing_handoff"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("unmarshal body: %v\nbody: %s", err, rec.Body.String())
			}
			if len(body.StaleInProgress) != tc.wantStale {
				t.Errorf("stale_in_progress: got %d, want %d", len(body.StaleInProgress), tc.wantStale)
			}
			if len(body.CompletionCandidates) != tc.wantCands {
				t.Errorf("completion_candidates: got %d, want %d", len(body.CompletionCandidates), tc.wantCands)
			}
			if body.PendingProposalsCount != tc.wantPending {
				t.Errorf("pending_proposals_count: got %d, want %d", body.PendingProposalsCount, tc.wantPending)
			}
			if body.MissingHandoff != tc.wantMissing {
				t.Errorf("missing_handoff: got %v, want %v", body.MissingHandoff, tc.wantMissing)
			}
		})
	}
}
