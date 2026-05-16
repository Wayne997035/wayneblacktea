package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/completioncandidate"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// candidateStore is the narrow interface DashboardHandler uses from
// the completioncandidate domain.
type candidateStore interface {
	ListPendingCandidates(ctx context.Context, workspaceID *uuid.UUID) ([]completioncandidate.Candidate, error)
}

// automationHandoffStore is the narrow interface for querying recent session handoffs.
// *session.Store satisfies this interface via LatestHandoff.
type automationHandoffStore interface {
	LatestHandoff(ctx context.Context) (*db.SessionHandoff, error)
}

// SetCandidateStore wires the completion candidate store into the dashboard handler.
func (h *DashboardHandler) SetCandidateStore(s candidateStore) {
	h.candidate = s
}

// SetHandoffStore wires the session handoff store into the dashboard handler.
func (h *DashboardHandler) SetHandoffStore(s automationHandoffStore) {
	h.handoff = s
}

// automationHealthResponse is the JSON shape for GET /api/dashboard/automation-health.
type automationHealthResponse struct {
	StaleInProgress       []candidateJSON `json:"stale_in_progress"`
	CompletionCandidates  []candidateJSON `json:"completion_candidates"`
	PendingProposalsCount int             `json:"pending_proposals_count"`
	MissingHandoff        bool            `json:"missing_handoff"`
}

// candidateJSON is the API representation of a completion candidate.
type candidateJSON struct {
	ID                string   `json:"id"`
	TaskID            string   `json:"task_id"`
	RepoName          string   `json:"repo_name,omitempty"`
	Reason            string   `json:"reason"`
	EvidenceRefs      []string `json:"evidence_refs"`
	Confidence        string   `json:"confidence"`
	SuggestedArtifact string   `json:"suggested_artifact,omitempty"`
	Status            string   `json:"status"`
	DetectedAt        string   `json:"detected_at"`
}

// toCandidateJSON converts a domain Candidate to its API representation.
func toCandidateJSON(c completioncandidate.Candidate) candidateJSON {
	refs := c.EvidenceRefs
	if refs == nil {
		refs = []string{}
	}
	return candidateJSON{
		ID:                c.ID.String(),
		TaskID:            c.TaskID.String(),
		RepoName:          c.RepoName,
		Reason:            string(c.Reason),
		EvidenceRefs:      refs,
		Confidence:        string(c.Confidence),
		SuggestedArtifact: c.SuggestedArtifact,
		Status:            string(c.Status),
		DetectedAt:        c.DetectedAt.UTC().Format(time.RFC3339),
	}
}

const (
	// missingHandoffThreshold: a pending handoff older than 48h is considered missing.
	missingHandoffThreshold = 48 * time.Hour
)

// GetAutomationHealth handles GET /api/dashboard/automation-health.
// It runs completion-candidate detection, lists pending candidates, counts pending
// proposals, and checks whether the latest session handoff is stale.
func (h *DashboardHandler) GetAutomationHealth(c echo.Context) error {
	ctx := c.Request().Context()

	resp := automationHealthResponse{
		StaleInProgress:      []candidateJSON{},
		CompletionCandidates: []candidateJSON{},
	}

	// List pending candidates (detection is run by the background scheduler, not on every GET).
	if h.candidate != nil {
		candidates, err := h.candidate.ListPendingCandidates(ctx, nil)
		if err != nil {
			c.Logger().Errorf("GetAutomationHealth ListPendingCandidates: %v", err)
			return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
		}
		for _, cd := range candidates {
			j := toCandidateJSON(cd)
			if cd.Reason == completioncandidate.ReasonStaleInProgress {
				resp.StaleInProgress = append(resp.StaleInProgress, j)
			} else {
				resp.CompletionCandidates = append(resp.CompletionCandidates, j)
			}
		}
	}

	// Count pending proposals.
	if h.proposal != nil {
		pending, err := h.proposal.ListPending(ctx)
		if err != nil {
			c.Logger().Errorf("GetAutomationHealth ListPending: %v", err)
			return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
		}
		resp.PendingProposalsCount = len(pending)
	}

	// Check for missing handoff: latest unresolved handoff older than 48h.
	if h.handoff != nil {
		handoff, err := h.handoff.LatestHandoff(ctx)
		if err != nil && !errors.Is(err, session.ErrNotFound) {
			// Non-fatal: missing handoff check is advisory.
			c.Logger().Warnf("GetAutomationHealth LatestHandoff: %v", err)
		} else if handoff != nil && !handoff.ResolvedAt.Valid {
			if handoff.CreatedAt.Valid && time.Since(handoff.CreatedAt.Time) > missingHandoffThreshold {
				resp.MissingHandoff = true
			}
		}
	}

	return c.JSON(http.StatusOK, resp)
}
