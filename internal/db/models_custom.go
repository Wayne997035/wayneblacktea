// Hand-written companion to sqlc-generated models.go.
//
// sqlc regenerates files whose names match its own emission pattern
// (`models.go`, `*.sql.go`); a different basename (`models_custom.go`) is safe
// from clobber. Keep this file for MarshalJSON / MarshalText / Stringer methods
// that need to override default reflection-based shapes for sqlc rows.
//
// Internal type shapes stay off API surfaces: a row struct is storage detail,
// so anything crossing the boundary gets an explicit marshaller here rather
// than leaking sqlc's reflection default.

package db

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// pendingProposalJSON is the wire shape for PendingProposal — flat, no
// pgtype.* wrapper structs, nullable fields as `*string` with `omitempty` so
// missing values vanish entirely rather than emit `null`.
//
// The frontend distinguishes "field absent" (no information) from `null`
// (explicit absence) for the reason column (see PR #121 / #123); keep this
// shape congruent with handler.pendingProposalResponse so any future debug
// endpoint that marshals db.PendingProposal directly produces the same JSON.
type pendingProposalJSON struct {
	ID          string          `json:"id"`
	WorkspaceID *string         `json:"workspace_id,omitempty"`
	Type        string          `json:"type"`
	Payload     json.RawMessage `json:"payload"`
	Status      string          `json:"status"`
	ProposedBy  *string         `json:"proposed_by"`
	CreatedAt   string          `json:"created_at"`
	ResolvedAt  *string         `json:"resolved_at,omitempty"`
	Reason      *string         `json:"reason,omitempty"`
}

// MarshalJSON emits a clean JSON shape for PendingProposal that hides
// pgx pgtype.* wrapper internals.
//
// Without this method, default reflection marshalling of pgtype.Text /
// pgtype.UUID / pgtype.Timestamptz leaks `{"String":"x","Valid":true}` or
// `{"Bytes":[1,2,...],"Valid":true}` whenever a caller serialises a raw
// db.PendingProposal — which today happens only through the handler-layer
// toResponse() but is one stray c.JSON(prop) away from regressing.
// Adding the method here makes the type self-defending regardless of caller.
//
// sqlc regen (`cd build && task sqlc`) never touches this file (different
// basename from generated files), so the override survives schema iterations.
//
// Time format mirrors handler.toResponse() (RFC3339, second-resolution); the
// existing API contract emits this shape and tests assert on it.
func (p PendingProposal) MarshalJSON() ([]byte, error) {
	out := pendingProposalJSON{
		ID:      p.ID.String(),
		Type:    p.Type,
		Payload: json.RawMessage(p.Payload),
		Status:  p.Status,
	}
	if len(p.Payload) == 0 {
		// json.RawMessage("") marshals to invalid JSON; coerce to "null"
		// so the output is always parseable.
		out.Payload = json.RawMessage("null")
	}
	if p.WorkspaceID.Valid {
		s := uuid.UUID(p.WorkspaceID.Bytes).String()
		out.WorkspaceID = &s
	}
	if p.ProposedBy.Valid {
		s := p.ProposedBy.String
		out.ProposedBy = &s
	}
	if p.CreatedAt.Valid {
		out.CreatedAt = p.CreatedAt.Time.UTC().Format(time.RFC3339)
	}
	if p.ResolvedAt.Valid {
		s := p.ResolvedAt.Time.UTC().Format(time.RFC3339)
		out.ResolvedAt = &s
	}
	if p.Reason.Valid {
		s := p.Reason.String
		out.Reason = &s
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("marshaling pending proposal: %w", err)
	}
	return b, nil
}

// decisionJSON is the wire shape for Decision — every field Decision has
// EXCEPT actor_session_id and confirmed_by_human.
//
// actor_session_id is a server-side audit/provenance identity
// (backend-security-design.md §2 adversarial input / provenance integrity):
// PR160 round-2 security review (M-3) found that any caller of
// list_decisions / GET /api/decisions could read another MCP session's
// actor_session_id back out, turning the residual risk accepted by decision
// 91ff27d6 ("only a caller who already knows a session ID can impersonate
// it") into "any caller can learn a live session ID for free by calling a
// core read tool" — a materially cheaper attack than the one that was
// actually accepted. The column itself is untouched; only the read-side
// serialization is closed.
//
// confirmed_by_human is dropped for a different but adjacent reason (M-2):
// the confirmation gate the field's doc comment refers to
// (decision.LogParams.ConfirmedByHuman) does not exist yet, so this column
// writes false on every row regardless of whether the decision is actually
// human-approved. Surfacing "confirmed_by_human": false on every decision
// reads to an LLM caller as an explicit "not approved by a human" signal,
// which is false — it is "this feature is not wired up yet". Re-add it once
// a real confirmation gate exists to set it meaningfully.
//
// [F170-03] embedding, embedding_provider, embedding_model and embedding_dim
// are dropped for a third reason: payload size. Embedding is the raw pgvector
// blob, which encoding/json renders as base64 — a 3072-dimension float32
// vector is 12,288 bytes of column, ~16,400 bytes of base64, per decision
// row. list_decisions returns many rows at once, so a single call could
// spend six figures of a caller's context window on a field no consumer
// reads: web/ has zero references to any of the four, no tool description or
// mcpInstructions names them, and the only Go readers of Decision.Embedding
// (SearchByCosine, cmd/wbt reembed/doctor) work on the Go struct or raw SQL
// columns, never on this JSON shape. The three provider/model/dim scalars go
// with it because they only describe the vector that is no longer here.
// The columns themselves and every DB read/write are untouched — this method
// governs json.Marshal(Decision) only.
//
// Every other field reuses Decision's own pgtype.* types (not a hand-reshaped
// plain-Go mirror like pendingProposalJSON above) specifically so its wire
// representation — null-vs-value, RFC3339Nano timestamps, UUID string form —
// stays byte-identical to what encoding/json already produced for Decision
// before this type existed (pgtype.Text/UUID/Timestamptz all implement
// json.Marshaler themselves); only the audit and embedding fields disappear.
type decisionJSON struct {
	ID           uuid.UUID          `json:"id"`
	ProjectID    pgtype.UUID        `json:"project_id"`
	RepoName     pgtype.Text        `json:"repo_name"`
	Title        string             `json:"title"`
	Context      string             `json:"context"`
	Decision     string             `json:"decision"`
	Rationale    string             `json:"rationale"`
	Alternatives pgtype.Text        `json:"alternatives"`
	CreatedAt    pgtype.Timestamptz `json:"created_at"`
	WorkspaceID  pgtype.UUID        `json:"workspace_id"`
	TaskID       pgtype.UUID        `json:"task_id"`
	Source       string             `json:"source"`
}

// MarshalJSON hides Decision.ActorSessionID, Decision.ConfirmedByHuman and
// the four embedding fields from every JSON serialization boundary in this
// codebase (handler.JSON responses, MCP jsonText, any future
// c.JSON(decision) call site) — PR160 M-3 / M-2, [F170-03]. Declared on the
// VALUE receiver, not a pointer: pgtype's own MarshalJSON methods (Text,
// UUID, Timestamptz — see this package's
// go.sum-pinned pgx/v5) all use value receivers for the identical reason
// mcp.safeSessionHandoff documents (internal/mcp/session_handoff_safe.go):
// encoding/json only special-cases a pointer receiver when the value being
// marshaled is addressable, which a map value or an `any`-boxed element is
// not guaranteed to be. A value receiver makes "this type never emits those
// two fields" hold regardless of how a caller stores or nests the value —
// []db.Decision, *db.Decision, map[string]any{"decision": d}, []any{d}, a
// struct field — all of them.
//
// The write path is untouched: Decision.ActorSessionID/ConfirmedByHuman and
// the four embedding fields still round-trip through the DB exactly as
// before, and SearchByCosine still reads Decision.Embedding off the Go
// struct (this method only
// governs json.Marshal(Decision), never affects DB reads/writes, which go
// through pgx's binary/text wire protocol, not encoding/json).
func (d Decision) MarshalJSON() ([]byte, error) {
	out := decisionJSON{
		ID:           d.ID,
		ProjectID:    d.ProjectID,
		RepoName:     d.RepoName,
		Title:        d.Title,
		Context:      d.Context,
		Decision:     d.Decision,
		Rationale:    d.Rationale,
		Alternatives: d.Alternatives,
		CreatedAt:    d.CreatedAt,
		WorkspaceID:  d.WorkspaceID,
		TaskID:       d.TaskID,
		Source:       d.Source,
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("marshaling decision: %w", err)
	}
	return b, nil
}

var _ json.Marshaler = Decision{}
