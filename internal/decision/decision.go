package decision

import (
	"errors"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a requested decision does not exist.
var ErrNotFound = errors.New("decision: not found")

// ErrInvalidSource is returned when a LogParams.Source is not exactly
// SourceManual or SourceAuto. The error text never echoes the caller's
// invalid value — reflected-value hygiene against log injection
// (backend-security-design.md §3).
var ErrInvalidSource = errors.New("decision: invalid source")

// ErrConflictingListFilter is returned when ListParams sets both ProjectID
// and RepoName — callers must pick one filter, not both (P3.0a Stage B).
var ErrConflictingListFilter = errors.New("decision: project_id and repo_name are mutually exclusive")

// ErrInvalidListLimit is returned when ListParams.Limit falls outside the
// 1..100 range. Handlers are expected to normalize (default 20, cap 100)
// before calling List; this is defence-in-depth for direct callers.
var ErrInvalidListLimit = errors.New("decision: limit must be between 1 and 100")

// Source identifies which code path originated a decision: a human-intent
// path ("manual" — an operator-triggered tool call or HTTP request) or a
// system-inferred path ("auto" — MCP tool-call classifier, transcript
// classifier, or a TypeDecision proposal materialiser). Source is a path
// constant chosen by the calling code; it is never decoded from a
// caller-supplied payload (backend-security-design.md §2 adversarial input /
// provenance integrity). manual does NOT prove a human confirmed the
// decision — log_decision, confirm_plan, and finish_work are LLM-callable
// MCP tools with no confirmation gate, so an injected agent can produce a
// manual-sourced row via ordinary tool use. A real confirm gate is tracked
// separately (GTD task 41ef0520-ad5a-4aa0-805b-4ba13ba927fd; security review
// round 2, M-2(a)).
type Source string

const (
	// SourceManual marks a decision written via a human-intent code path
	// (an operator-triggered tool call or HTTP request) — NOT proof that a
	// human confirmed it; see the Source doc comment above.
	SourceManual Source = "manual"
	// SourceAuto marks a decision the system inferred without going through
	// a human-intent code path.
	SourceAuto Source = "auto"
)

// Valid reports whether s is exactly SourceManual or SourceAuto.
// Comparison is case-sensitive with no trimming — near-miss values (wrong
// case, surrounding whitespace) are invalid, not coerced.
func (s Source) Valid() bool {
	return s == SourceManual || s == SourceAuto
}

// LogParams holds parameters for recording a new architectural decision.
type LogParams struct {
	ProjectID *uuid.UUID
	// TaskID links this decision to a specific task (migration 000048).
	// No FK constraint (CLAUDE.md red-line §9); referential integrity in code.
	TaskID       *uuid.UUID
	RepoName     string
	Title        string
	Context      string
	Decision     string
	Rationale    string
	Alternatives string
	// Source is required and MUST be set by the calling code path
	// (SourceManual or SourceAuto), never decoded from a caller payload.
	Source Source
}

// ListParams holds filter parameters for List. Workspace scoping is NOT a
// field here — it stays store-scoped (bound at Store construction time via
// workspace_id), never caller-supplied, so a caller cannot cross workspaces
// by passing a different workspace_id argument.
type ListParams struct {
	// ProjectID, if set, restricts results to this project. Mutually
	// exclusive with RepoName — callers (or the MCP handler) must pick one.
	ProjectID *uuid.UUID
	// RepoName, if non-empty, restricts results to this repo.
	RepoName string
	// IncludeAuto, when false (the default), restricts results to
	// Source=manual only. When true, both manual and auto decisions are
	// returned. Fail-closed: zero value is false.
	IncludeAuto bool
	// Limit MUST be in 1..100. Callers normalize (default 20, cap 100)
	// before calling List; List itself enforces the range via Validate.
	Limit int32
}

// Validate reports whether p is a well-formed filter: ProjectID and
// RepoName cannot both be set, and Limit must be in 1..100.
func (p ListParams) Validate() error {
	if p.ProjectID != nil && p.RepoName != "" {
		return ErrConflictingListFilter
	}
	if p.Limit < 1 || p.Limit > 100 {
		return ErrInvalidListLimit
	}
	return nil
}
