package mcp

import (
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/outcome"
	"github.com/Wayne997035/wayneblacktea/internal/procedural"
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

// decisionWriteAck is what log_decision answers with. list_decisions and
// get_decision return the complete record.
//
// Dropped: context, decision, rationale, alternatives — the four prose fields
// that ARE the decision, all supplied on this same call — plus the embedding
// vector, which no caller has ever needed and is the single largest field on
// the row.
type decisionWriteAck struct {
	ID               uuid.UUID          `json:"id"`
	Title            string             `json:"title"`
	RepoName         pgtype.Text        `json:"repo_name"`
	ProjectID        pgtype.UUID        `json:"project_id"`
	TaskID           pgtype.UUID        `json:"task_id"`
	Source           string             `json:"source"`
	ConfirmedByHuman bool               `json:"confirmed_by_human"`
	CreatedAt        pgtype.Timestamptz `json:"created_at"`
}

func ackDecision(d *db.Decision) *decisionWriteAck {
	if d == nil {
		return nil
	}
	return &decisionWriteAck{
		ID:               d.ID,
		Title:            d.Title,
		RepoName:         d.RepoName,
		ProjectID:        d.ProjectID,
		TaskID:           d.TaskID,
		Source:           d.Source,
		ConfirmedByHuman: d.ConfirmedByHuman,
		CreatedAt:        d.CreatedAt,
	}
}

// proceduralWriteAck is what record_procedure and mark_procedure_used answer
// with. recall / search_procedures return the complete record.
//
// Dropped: when_to_use, approach_md, tools_used, files_touched. approach_md is
// the procedure itself and is by far the largest of them.
//
// success_count and last_used_at are kept precisely because mark_procedure_used
// exists to change them: they are the whole point of that call's answer.
type proceduralWriteAck struct {
	ID           uuid.UUID  `json:"id"`
	Title        string     `json:"title"`
	RepoName     string     `json:"repo_name,omitempty"`
	ProjectID    *uuid.UUID `json:"project_id,omitempty"`
	SuccessCount int        `json:"success_count"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

func ackProcedural(m *procedural.ProceduralMemory) *proceduralWriteAck {
	if m == nil {
		return nil
	}
	return &proceduralWriteAck{
		ID:           m.ID,
		Title:        m.Title,
		RepoName:     m.RepoName,
		ProjectID:    m.ProjectID,
		SuccessCount: m.SuccessCount,
		LastUsedAt:   m.LastUsedAt,
		CreatedAt:    m.CreatedAt,
	}
}

// outcomeWriteAck is what record_outcome answers with. list_outcomes and
// get_outcome return the complete record.
//
// Dropped: notes and metrics. result is kept because it is the field
// record_outcome exists to set and the server validates it against a closed
// set; supersedes_id is kept because the caller cannot know it — the supersede
// branch decides server-side whether a prior row was replaced.
type outcomeWriteAck struct {
	ID            uuid.UUID   `json:"id"`
	EntityType    string      `json:"entity_type"`
	EntityID      uuid.UUID   `json:"entity_id"`
	Result        string      `json:"result"`
	RelatedRuleID []uuid.UUID `json:"related_rule_ids,omitempty"`
	WorkSessionID *uuid.UUID  `json:"work_session_id,omitempty"`
	SupersedesID  *uuid.UUID  `json:"supersedes_id,omitempty"`
	CreatedAt     time.Time   `json:"created_at"`
}

// ackOutcome takes and returns values, not pointers, to match
// wrapUntrustedOutcome's own signature — recordOutcomeResponse embeds the
// result, so a pointer here would change that response from flattened fields
// to a nested object.
func ackOutcome(o outcome.Outcome) outcomeWriteAck {
	return outcomeWriteAck{
		ID:            o.ID,
		EntityType:    o.EntityType,
		EntityID:      o.EntityID,
		Result:        o.Result,
		RelatedRuleID: o.RelatedRuleIDs,
		WorkSessionID: o.WorkSessionID,
		SupersedesID:  o.SupersedesID,
		CreatedAt:     o.CreatedAt,
	}
}
