// Package discipline implements the meta-rule drift-detection store: every
// successful mutating MCP tool call is recorded as a discipline_events row,
// and the system_health tool surfaces "drift signals" (mutating events that
// happened without a recent log_decision / confirm_plan in the same session).
//
// This is the portable MCP-native replacement for the cancelled PreToolUse
// hook approach (decision e5310a15) — the same enforcement logic, but
// implemented inside the MCP server so any client (Claude Code, Discord,
// CLI) gets identical drift signals.
//
// TTL is 30 days, enforced via `task discipline-prune` (build/Taskfile.yml).
// Per backend-security-design.md §1.3, this retention policy is mandatory
// for any "every observation" table.
package discipline

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a requested event does not exist (currently
// unused by the v1 API, but reserved for future per-id lookups).
var ErrNotFound = errors.New("discipline: event not found")

// MutatingTools is the canonical allowlist of MCP tool names that trigger
// is_mutating=true when middleware records a discipline_events row.
//
// Membership is intentionally explicit: any tool not in this map is treated
// as read-only / advisory. New mutating tools MUST be added here in the same
// PR that introduces them so drift detection stays accurate.
var MutatingTools = map[string]bool{
	// GTD task lifecycle
	"add_task":      true,
	"update_task":   true,
	"complete_task": true,
	"delete_task":   true,

	// Plan / proposal confirmation
	"confirm_plan":      true,
	"confirm_proposal":  true,
	"confirm_proposals": true,

	// Decision / knowledge / memory writes
	"log_decision":           true,
	"add_knowledge":          true,
	"add_procedural":         true,
	"add_vision_item":        true,
	"update_vision_item":     true,
	"promote_vision_to_task": true,
	"create_concept":         true,
	"submit_review":          true,

	// Session / handoff
	"set_session_handoff": true,
	"resolve_handoff":     true,

	// Work session lifecycle
	"start_work":      true,
	"finish_work":     true,
	"checkpoint_work": true,

	// Goals / projects
	"propose_goal":    true,
	"propose_project": true,
	"create_goal":     true,
	"create_project":  true,

	// Procedural / repo / arch / status
	"mark_procedural_used":  true,
	"sync_repo":             true,
	"upsert_project_arch":   true,
	"update_project_status": true,

	// Watchdog meta-cognition (Memory-8) — these tools mutate
	// discipline_events_m8 and are themselves observability writes.
	"analyze_agent_behavior": true,
	"mark_loop_resolved":     true,
}

// IsMutating returns true when toolName is in the canonical MutatingTools set.
// Out-of-set tools (queries, lists, gets) are recorded with is_mutating=false.
func IsMutating(toolName string) bool {
	return MutatingTools[toolName]
}

// Event is the domain model for a single recorded MCP tool invocation.
type Event struct {
	ID               int64      `json:"id"`
	SessionID        string     `json:"session_id"`
	RepoName         string     `json:"repo_name,omitempty"`
	ToolName         string     `json:"tool_name"`
	IsMutating       bool       `json:"is_mutating"`
	ObservedAt       time.Time  `json:"observed_at"`
	LinkedDecisionID *uuid.UUID `json:"linked_decision_id,omitempty"`
	WorkspaceID      *uuid.UUID `json:"workspace_id,omitempty"`
}

// InsertParams holds the fields needed to record a new discipline_events row.
// session_id is required; the others are optional (NULL when zero-valued).
type InsertParams struct {
	SessionID        string
	RepoName         string
	ToolName         string
	IsMutating       bool
	LinkedDecisionID *uuid.UUID
	WorkspaceID      *uuid.UUID
}

// Store is the backend-agnostic contract for the discipline events bounded
// context. Postgres-backed *PgStore and SQLite-backed *SQLiteStore satisfy
// this interface.
type Store interface {
	// Insert records a single discipline event. Errors are returned to the
	// caller; the MCP middleware logs them via slog.Warn and continues.
	Insert(ctx context.Context, p InsertParams) error

	// RecentMutating returns mutating events observed at or after `since`,
	// ordered by observed_at DESC, capped at limit (caller-controlled).
	// Used by system_health to count 24h drift candidates.
	RecentMutating(ctx context.Context, since time.Time, limit int) ([]Event, error)

	// RecentDecisionTimes returns the observed_at timestamps of every
	// log_decision / confirm_plan event in `sessionID` at or after `since`,
	// ordered DESC. Used by drift-detection to know whether a mutating call
	// had a "preceding decision" inside the 15-minute window.
	RecentDecisionTimes(ctx context.Context, sessionID string, since time.Time) ([]time.Time, error)
}
