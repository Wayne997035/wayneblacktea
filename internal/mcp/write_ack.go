package mcp

import (
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// [GTD c46025c1] Write-acknowledgement projections.
//
// A write tool used to answer with the whole row it had just written, so the
// caller paid a second time for the body it had supplied a moment earlier.
// Measured on PR #171: ten opened/edited tickets, ~2 KB of description each,
// 30-40k tokens of pure echo.
//
// These types are the write-side counterpart of get_today_context's
// goalSummary / projectSummary (tools_context.go), which dropped the same
// fields on the read side under the same reasoning (W4, token-diet). Each one
// below names the tool that still returns the complete record, because a slim
// answer is only acceptable when the full one is one call away.
//
// What they keep is what the caller could NOT have known before the call:
// server-assigned ids and timestamps, and values the server may have coerced
// (status, kind and assignee all pass through normalisers). What they drop is
// the free text the caller just sent.
//
// Read tools are deliberately untouched — 22 of the 48 wrapUntrusted return
// sites are reads, and slimming those would break the "one call away" escape
// hatch these projections depend on.
//
// Neutralisation still applies to every field that survives: these project
// FROM the already-wrapped value, so a forged boundary marker in a title is
// neutralised exactly as before. Dropping a field is not a way to stop
// sanitising it.

// taskWriteAck is what add_task / update_task / complete_task /
// set_task_status answer with. get_task returns the complete record.
//
// Dropped: description, context, artifact, checklist — every one of them
// caller-supplied on the same call. artifact matters more than it looks:
// complete_task's artifact is where the evidence paragraph goes, and it is
// routinely the largest field on the row.
type taskWriteAck struct {
	ID         uuid.UUID          `json:"id"`
	Title      string             `json:"title"`
	Status     string             `json:"status"`
	Kind       string             `json:"kind"`
	Priority   int32              `json:"priority"`
	Importance pgtype.Int2        `json:"importance"`
	Assignee   pgtype.Text        `json:"assignee"`
	ProjectID  pgtype.UUID        `json:"project_id"`
	DueDate    pgtype.Timestamptz `json:"due_date"`
	CreatedAt  pgtype.Timestamptz `json:"created_at"`
	UpdatedAt  pgtype.Timestamptz `json:"updated_at"`
}

// ackTask projects an already-wrapped task. It takes the wrapped value rather
// than the raw row so the projection cannot become a way to skip
// neutralisation: callers write ackTask(wrapUntrustedTask(t)).
func ackTask(t *db.Task) *taskWriteAck {
	if t == nil {
		return nil
	}
	return &taskWriteAck{
		ID:         t.ID,
		Title:      t.Title,
		Status:     t.Status,
		Kind:       t.Kind,
		Priority:   t.Priority,
		Importance: t.Importance,
		Assignee:   t.Assignee,
		ProjectID:  t.ProjectID,
		DueDate:    t.DueDate,
		CreatedAt:  t.CreatedAt,
		UpdatedAt:  t.UpdatedAt,
	}
}
