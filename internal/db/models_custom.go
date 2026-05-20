// Hand-written companion to sqlc-generated models.go.
//
// sqlc regenerates files whose names match its own emission pattern
// (`models.go`, `*.sql.go`); a different basename (`models_custom.go`) is safe
// from clobber. Keep this file for MarshalJSON / MarshalText / Stringer methods
// that need to override default reflection-based shapes for sqlc rows.
//
// See `_project/.claude/rules/backend-security-design.md` §3 on hiding internal
// type shapes from API surfaces.

package db

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
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
