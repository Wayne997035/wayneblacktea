package runtime

import (
	"time"

	"github.com/google/uuid"
)

// Trigger records what started a run. Type is one of "user_intent",
// "operator", "event" or "deep_analysis_delegation".
type Trigger struct {
	Type string
	Ref  string
}

// Principal mirrors the principal_kind / principal_id columns.
type Principal struct {
	Kind string
	ID   string
}

// Status is the lifecycle state of a Run.
type Status string

const (
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
)

// Turn is one model call inside a Run.
type Turn struct {
	ID            uuid.UUID
	RunID         uuid.UUID
	Sequence      int
	ModelProvider string
	ModelID       string
	InputDigest   string
	OutputDigest  string
	StartedAt     time.Time
	FinishedAt    *time.Time
	FinishReason  FinishReason
}

// Run is one runtime run. FinalOutput and HadCapabilityWrite are in-memory
// only (not runtime_runs columns): FinalOutput is the last turn's answer text,
// HadCapabilityWrite is the OR of every turn's flag (seam for slice 3f).
type Run struct {
	ID                 uuid.UUID
	WorkspaceID        *uuid.UUID
	Trigger            Trigger
	Principal          Principal
	PresenceSessionID  *uuid.UUID
	WorkSessionID      *uuid.UUID
	OperatorID         *uuid.UUID
	Status             Status
	Budget             Budget
	ContextDigest      string
	StartedAt          time.Time
	FinishedAt         *time.Time
	FinishReason       FinishReason
	ErrorCode          string
	Turns              []Turn
	FinalOutput        string
	HadCapabilityWrite bool
}

// RunParams is the input to RunLoop. RepoName is unused here; slice 3f reads
// it for the work-session lookup.
type RunParams struct {
	WorkspaceID       *uuid.UUID
	Trigger           Trigger
	Principal         Principal
	Budget            Budget
	PresenceSessionID *uuid.UUID
	RepoName          string
}
