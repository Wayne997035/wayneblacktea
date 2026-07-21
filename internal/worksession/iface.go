// Package worksession implements the work_session first-class data model
// introduced in P0a-α Session Core. It is intentionally named worksession
// (not session) to avoid collision with the existing session_handoff package
// (internal/session).
package worksession

import (
	"context"
	"errors"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/redact"
	"github.com/google/uuid"
)

// ErrNotFound is returned when the requested work session does not exist.
var ErrNotFound = errors.New("worksession: not found")

// ErrAlreadyActive is returned when a second in_progress session is created
// for the same workspace+repo (partial unique index violation).
var ErrAlreadyActive = errors.New("worksession: another session is already in_progress for this repo")

// AllowedVerificationStatuses is the closed set of verification_status values
// accepted by finish_work. Mirrored in the DB CHECK constraint (migration
// 000065) for dual-layer defence.
var AllowedVerificationStatuses = map[string]bool{
	"not_run": true,
	"passed":  true,
	"failed":  true,
	"unknown": true,
}

// AllowedFinalResults is the closed set of final_result values accepted by
// finish_work. Mirrors outcome.AllowedResults (migration 000065 CHECK).
var AllowedFinalResults = map[string]bool{
	"success":   true,
	"failure":   true,
	"partial":   true,
	"unknown":   true,
	"regressed": true,
}

// AllowedEvidenceTypes is the closed set of work_session_evidence.evidence_type
// values. Mirrored in the DB CHECK constraint (migration 000066).
var AllowedEvidenceTypes = map[string]bool{
	"command":     true,
	"pr":          true,
	"ci":          true,
	"railway":     true,
	"manual_note": true,
}

// AllowedEvidenceStatuses is the closed set of work_session_evidence.status
// values. Mirrored in the DB CHECK constraint (migration 000066).
var AllowedEvidenceStatuses = map[string]bool{
	"passed":  true,
	"failed":  true,
	"unknown": true,
}

// MaxListRecentLimit is the server-side hard cap on ListRecent's limit
// parameter, regardless of what the caller requests (resource guard against
// an LLM requesting an unbounded list). Mirrors outcome.maxOutcomeLimit.
const MaxListRecentLimit = 100

// MaxEvidenceListLimit is the server-side hard cap on the number of
// work_session_evidence rows GetEvidence returns for a single session,
// regardless of how many rows actually exist (resource guard against an
// unbounded evidence table — sibling constant of MaxListRecentLimit above).
// Both stores apply this as a SQL LIMIT while preserving ORDER BY created_at
// ASC, so hitting the cap drops the NEWEST row(s), not the oldest. The
// current single write path (finish_work's evidence array, capped
// server-side at maxEvidenceItems=20 in internal/mcp/tools_worksession.go)
// cannot reach this cap on its own — it exists as defence in depth against
// any future write path or direct AddEvidence caller that inserts
// unboundedly.
const MaxEvidenceListLimit = 100

// EvidenceOutputExcerptCap is the maximum rune length for
// Evidence.OutputExcerpt and Session.VerificationOutputExcerpt after
// redaction. Applied AFTER redact.ForLLM — redact-then-cap order is
// mandatory: a credential straddling the cap boundary must not survive as an
// unredacted partial (backend-security-design.md §3.1).
const EvidenceOutputExcerptCap = 2000

// RedactAndCapOutputExcerpt applies redact.ForLLM to s and then caps the
// result to EvidenceOutputExcerptCap runes. Both the Postgres and SQLite
// stores call this single implementation for AddEvidence's output_excerpt
// and Finish's verification_output_excerpt so the redact-then-cap ordering
// can never drift between backends.
func RedactAndCapOutputExcerpt(s string) string {
	redacted := redact.ForLLM(s)
	runes := []rune(redacted)
	if len(runes) > EvidenceOutputExcerptCap {
		return string(runes[:EvidenceOutputExcerptCap])
	}
	return redacted
}

// CheckControlChars returns a non-empty error message when value contains
// ASCII control characters (< 0x20, excluding tab), CR, LF, NUL, or the
// Unicode line/paragraph separators U+2028/U+2029. Applied to single-line
// command-like fields (branch_name, verification_command, evidence.command)
// so a prompt-injected agent cannot smuggle a second shell instruction via an
// embedded newline.
//
// This mirrors internal/mcp/noise_filter.go:checkCommandField exactly, but is
// duplicated here (not imported) because internal/worksession must not
// depend on internal/mcp (reverse dependency).
func CheckControlChars(name, value string) string {
	for _, r := range value {
		if r == '\n' || r == '\r' || r == 0x00 {
			return name + " must not contain newline, carriage return, or null byte"
		}
		if r < 0x20 && r != '\t' {
			return name + " must not contain ASCII control characters"
		}
		if r == ' ' || r == ' ' {
			return name + " must not contain Unicode line/paragraph separator"
		}
	}
	return ""
}

// ResolveCompletedTaskIDs computes the final set of task IDs a Finish call
// should mark completed (wbt-2.0 P2 review F2). deferredTaskIDs always wins
// over completedTaskIDs: any task ID present in both is dropped from the
// result, so a caller cannot accidentally (or a prompt-injected agent
// cannot maliciously) mark a task completed by listing it in both arrays.
// When completedTaskIDs is empty, the fallback source is linkedTaskIDs (all
// tasks linked to the session) — with deferred IDs excluded the same way, so
// a deferred task is never swept into "completed" just because the caller
// omitted completed_task_ids. Both PG and SQLite stores call this single
// implementation so the exclusion semantics can never drift between
// backends. Preserves the input order of completedTaskIDs (or
// linkedTaskIDs, in the fallback case).
func ResolveCompletedTaskIDs(completedTaskIDs, deferredTaskIDs, linkedTaskIDs []uuid.UUID) []uuid.UUID {
	deferredSet := make(map[uuid.UUID]bool, len(deferredTaskIDs))
	for _, id := range deferredTaskIDs {
		deferredSet[id] = true
	}

	source := completedTaskIDs
	if len(source) == 0 {
		source = linkedTaskIDs
	}

	out := make([]uuid.UUID, 0, len(source))
	for _, id := range source {
		if deferredSet[id] {
			continue
		}
		out = append(out, id)
	}
	return out
}

// Session is the domain model for a work session.
// It mirrors the work_sessions table columns using Go-native types so both
// the Postgres and SQLite stores can return the same struct.
type Session struct {
	ID          uuid.UUID  `json:"id"`
	WorkspaceID uuid.UUID  `json:"workspace_id"`
	RepoName    string     `json:"repo_name"`
	ProjectID   *uuid.UUID `json:"project_id,omitempty"`
	Title       string     `json:"title"`
	Goal        string     `json:"goal"`
	Status      string     `json:"status"`
	Source      string     `json:"source"`
	// ConfirmedPlanID is reserved for future linking to a first-class plans table.
	// Currently always nil; do not set in P0a-α.
	ConfirmedPlanID  *uuid.UUID `json:"confirmed_plan_id,omitempty"`
	CurrentTaskID    *uuid.UUID `json:"current_task_id,omitempty"`
	FinalSummary     *string    `json:"final_summary,omitempty"`
	StartedAt        *string    `json:"started_at,omitempty"`
	LastCheckpointAt *string    `json:"last_checkpoint_at,omitempty"`
	CompletedAt      *string    `json:"completed_at,omitempty"`
	CreatedAt        string     `json:"created_at"`
	UpdatedAt        string     `json:"updated_at"`

	// --- wbt-2.0 P2.1 evidence-chain columns (migration 000065) ---

	// ContextPackID links to the in-memory assemble_context Pack used when
	// this session started. Phase 2 does not persist Packs yet, so this is
	// always nil today — the column exists for a future phase that adds pack
	// persistence, at which point start_work will populate it.
	ContextPackID             *uuid.UUID `json:"context_pack_id,omitempty"`
	VerificationStatus        *string    `json:"verification_status,omitempty"`
	VerificationCommand       *string    `json:"verification_command,omitempty"`
	VerificationOutputExcerpt *string    `json:"verification_output_excerpt,omitempty"`
	OutcomeID                 *uuid.UUID `json:"outcome_id,omitempty"`
	FinalResult               *string    `json:"final_result,omitempty"`
	BranchName                *string    `json:"branch_name,omitempty"`
}

// Evidence is the domain model for a work_session_evidence row — a single
// piece of proof (command output, PR link, CI result, Railway deploy log, or
// manual note) attached to a work session, typically recorded alongside
// finish_work. See AllowedEvidenceTypes / AllowedEvidenceStatuses for the
// closed value sets.
type Evidence struct {
	ID            uuid.UUID  `json:"id"`
	WorkspaceID   *uuid.UUID `json:"workspace_id,omitempty"`
	SessionID     uuid.UUID  `json:"session_id"`
	EvidenceType  string     `json:"evidence_type"`
	Status        string     `json:"status"`
	Command       *string    `json:"command,omitempty"`
	Artifact      *string    `json:"artifact,omitempty"`
	OutputExcerpt *string    `json:"output_excerpt,omitempty"`
	CreatedAt     string     `json:"created_at"`
}

// EvidenceInput holds one evidence row to insert as part of a Finish call.
// WorkspaceID and SessionID are populated by the store from the enclosing
// FinishParams.SessionID and the store's own workspace scope — callers do
// not set them here.
type EvidenceInput struct {
	EvidenceType  string
	Status        string
	Command       *string
	Artifact      *string
	OutputExcerpt *string
}

// SessionTask is the domain model for a work_session_tasks row.
type SessionTask struct {
	SessionID uuid.UUID `json:"session_id"`
	TaskID    uuid.UUID `json:"task_id"`
	Role      string    `json:"role"`
	CreatedAt string    `json:"created_at"`
}

// CreateParams holds the inputs for creating a new work session.
type CreateParams struct {
	WorkspaceID     uuid.UUID
	RepoName        string
	ProjectID       *uuid.UUID
	Title           string
	Goal            string
	Source          string
	ConfirmedPlanID *uuid.UUID
	TaskIDs         []uuid.UUID // linked with role=primary
	// BranchName is the git branch this session is working on (wbt-2.0 P2.1).
	// Optional; validated with CheckControlChars (single-line, no newlines).
	BranchName *string
	// Assignee is the canonical actor initiating this session (P6.8 — closes
	// the sixth in_progress transition path that P6.7's domain-layer gate
	// (gtd.RequireAssigneeForInProgress) cannot reach: batchMarkTasksInProgress
	// is a raw batch UPDATE against the tasks table that bypasses gtd.Store
	// entirely). Validated via gtd.NormalizeActor by both Store implementations
	// before use (defense in depth — mirrors gtd.Store.CreateTask/UpdateTask
	// re-validating despite the MCP layer already validating on the way in).
	// When a linked task_id already has a non-empty assignee, that value is
	// kept and this field is ignored for that task. When a linked task_id has
	// no assignee, this value is stamped onto it at the moment it flips from
	// pending to in_progress; if this field is ALSO empty, that task_id is
	// excluded from the flip and stays pending — the batch-update equivalent
	// of RequireAssigneeForInProgress rejecting the transition, rather than
	// silently producing an ownerless in_progress row. Empty is allowed at
	// the session level: a session started with no assignee simply defers to
	// whatever each linked task already has.
	Assignee string
}

// CheckpointParams holds the inputs for checkpointing a work session.
type CheckpointParams struct {
	SessionID         uuid.UUID
	Summary           string
	CompletedTaskIDs  []uuid.UUID
	NewTaskTitles     []string
	NewDecisionTitles []string
	Blockers          []string
	NextActions       []string
}

// FinishParams holds the inputs for finishing a work session.
type FinishParams struct {
	SessionID          uuid.UUID
	Summary            string
	CompletedTaskIDs   []uuid.UUID
	DeferredTaskIDs    []uuid.UUID
	Artifact           *string
	FollowUpTaskTitles []string

	// --- wbt-2.0 P2.1 evidence-chain additions ---

	// VerificationStatus must be one of AllowedVerificationStatuses when set.
	VerificationStatus *string
	// VerificationCommand is validated with CheckControlChars (single-line).
	VerificationCommand *string
	// VerificationOutputExcerpt is redacted via RedactAndCapOutputExcerpt
	// (redact.ForLLM THEN rune-cap to EvidenceOutputExcerptCap) before storage.
	VerificationOutputExcerpt *string
	// FinalResult must be one of AllowedFinalResults when set.
	FinalResult *string
	// OutcomeID optionally links this session to an already-created outcome
	// row. When set, the store persists it directly; SetOutcomeLink is the
	// alternative path used when the outcome is created after Finish returns.
	OutcomeID *uuid.UUID
	// Evidence holds zero or more evidence rows to insert alongside the
	// Finish UPDATE. Insertion is best-effort (non-fatal): the session is
	// already committed/updated by the time evidence rows are attempted, so a
	// failure to add one evidence row does not roll back or fail Finish.
	Evidence []EvidenceInput
}

// ActiveSessionResult is returned by GetActive.
type ActiveSessionResult struct {
	Active                bool          `json:"active"`
	Session               *Session      `json:"session,omitempty"`
	LinkedTasks           []SessionTask `json:"linked_tasks,omitempty"`
	LastCheckpoint        *string       `json:"last_checkpoint,omitempty"`
	ImplementationAllowed bool          `json:"implementation_allowed"`
}

// StoreIface is the backend-agnostic contract for the worksession bounded
// context. Both the Postgres and SQLite stores must satisfy this interface.
type StoreIface interface {
	// Create inserts a new work session with status=in_progress and links the
	// supplied task IDs (role=primary). Returns ErrAlreadyActive if a session
	// is already in_progress for the same workspace+repo.
	Create(ctx context.Context, p CreateParams) (*Session, error)

	// GetActive returns the in_progress session for workspace+repo, or
	// ErrNotFound when none exists.
	GetActive(ctx context.Context, workspaceID uuid.UUID, repoName string) (*ActiveSessionResult, error)

	// Checkpoint updates last_checkpoint_at, sets status=checkpointed, and
	// returns the updated session.
	Checkpoint(ctx context.Context, p CheckpointParams) (*Session, error)

	// Finish sets status=completed, completed_at=now, stores final_summary,
	// and returns the updated session. It uses a conditional UPDATE
	// (WHERE status='in_progress' OR status='checkpointed') to prevent races.
	Finish(ctx context.Context, p FinishParams) (*Session, error)

	// GetByID returns the session with the given ID, scoped to workspaceID.
	GetByID(ctx context.Context, workspaceID, sessionID uuid.UUID) (*Session, error)

	// LinkTask attaches a task to a session with the given role. No-ops if
	// the (session_id, task_id) pair already exists.
	LinkTask(ctx context.Context, sessionID, taskID uuid.UUID, role string) error

	// LinkedTasks returns all task links for the given session.
	LinkedTasks(ctx context.Context, sessionID uuid.UUID) ([]SessionTask, error)

	// ListRecent returns sessions for the workspace ordered by created_at
	// DESC, optionally filtered by repoName (empty = no filter). limit is
	// hard-capped at MaxListRecentLimit (100) server-side regardless of the
	// value the caller requests.
	ListRecent(ctx context.Context, workspaceID uuid.UUID, repoName string, limit int) ([]Session, error)

	// AddEvidence inserts one evidence row and returns the persisted record.
	// ev.OutputExcerpt is passed through RedactAndCapOutputExcerpt (redact
	// THEN cap — never the reverse) before storage. ev.Command is validated
	// with CheckControlChars; a non-empty violation reason is returned as an
	// error instead of writing the row.
	AddEvidence(ctx context.Context, ev Evidence) (*Evidence, error)

	// GetEvidence returns all work_session_evidence rows for sessionID,
	// ordered by created_at ASC. Returns an empty (non-nil) slice, never nil,
	// when no rows exist.
	GetEvidence(ctx context.Context, sessionID uuid.UUID) ([]Evidence, error)

	// SetOutcomeLink sets work_sessions.outcome_id for sessionID, scoped to
	// the store's workspace. Returns ErrNotFound when sessionID does not
	// exist in that workspace — callers (e.g. record_outcome's best-effort
	// linkage) are expected to tolerate this per the no-FK design (a stale
	// session_id is allowed to persist silently on the outcomes side).
	SetOutcomeLink(ctx context.Context, sessionID, outcomeID uuid.UUID) error

	// PruneOlderThan hard-deletes work_session_evidence rows older than
	// cutoff. Mirrors outcome.Store.PruneOlderThan; called daily by the
	// scheduler to enforce the 90-day TTL (backend-security-design.md §1.3).
	PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}
