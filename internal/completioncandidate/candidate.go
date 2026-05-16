// Package completioncandidate implements detection rules that surface GTD tasks
// that appear done but whose status is still pending or in_progress.
package completioncandidate

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Confidence represents how certain the detection rule is about the candidate.
type Confidence string

const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

// Status represents the lifecycle of a completion candidate.
type Status string

const (
	StatusPending     Status = "pending"
	StatusAccepted    Status = "accepted"
	StatusRejected    Status = "rejected"
	StatusAutoApplied Status = "auto_applied"
)

// Reason identifies which detection rule produced the candidate.
type Reason string

const (
	ReasonStaleInProgress  Reason = "stale_in_progress"
	ReasonFinishWorkGap    Reason = "finish_work_gap"
	ReasonArtifactEvidence Reason = "artifact_evidence"
	ReasonCompletionSignal Reason = "completion_signal"
)

// Candidate is the domain model for a completion_candidates row.
type Candidate struct {
	ID                uuid.UUID
	WorkspaceID       *uuid.UUID
	TaskID            uuid.UUID
	RepoName          string
	Reason            Reason
	EvidenceRefs      []string
	Confidence        Confidence
	SuggestedArtifact string
	Status            Status
	DetectedAt        time.Time
	ResolvedAt        *time.Time
}

// UpsertParams holds the inputs for creating or updating a candidate.
type UpsertParams struct {
	WorkspaceID       *uuid.UUID
	TaskID            uuid.UUID
	RepoName          string
	Reason            Reason
	EvidenceRefs      []string
	Confidence        Confidence
	SuggestedArtifact string
}

// DetectParams holds the detection configuration for DetectAndUpsert.
type DetectParams struct {
	WorkspaceID    *uuid.UUID
	StaleThreshold time.Duration // default 24h; hours of inactivity before flagging in_progress task
	LookbackDays   int           // default 7, max 30; days of activity_log to scan
}

// Store is the backend-agnostic interface for the completioncandidate domain.
// Both SQLite and Postgres stores must satisfy this interface.
type Store interface {
	// ListPendingCandidates returns all candidates with status='pending',
	// optionally filtered to the given workspace.
	ListPendingCandidates(ctx context.Context, workspaceID *uuid.UUID) ([]Candidate, error)

	// UpsertCandidate inserts or updates a candidate identified by (task_id, reason).
	// On conflict (task_id, reason) the detected_at is refreshed and confidence updated.
	UpsertCandidate(ctx context.Context, p UpsertParams) (*Candidate, error)

	// PruneResolved deletes resolved candidates older than olderThan. Returns the
	// number of rows deleted.
	PruneResolved(ctx context.Context, olderThan time.Duration) (int64, error)

	// DetectAndUpsert runs all 4 detection rules against the tasks / work_sessions /
	// activity_log tables and upserts any matches into completion_candidates.
	// Returns all upserted candidates (including previously pending ones that were
	// refreshed). Read-only for tasks; writes to completion_candidates only.
	DetectAndUpsert(ctx context.Context, p DetectParams) ([]Candidate, error)
}
