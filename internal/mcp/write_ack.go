package mcp

import (
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/procedural"
	"github.com/Wayne997035/wayneblacktea/internal/skill"
	"github.com/Wayne997035/wayneblacktea/internal/vision"
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

// There is deliberately NO outcome projection here. record_outcome has
// append/merge semantics: the caller sends a fragment and the server folds it
// into the existing notes/metrics, so what comes back is a computed value,
// not an echo — this ticket's premise ("returning it carries zero
// information") is false for merge-type writes. An outcomeWriteAck was
// written, proved wrong by tools_outcome_lifecycle_test.go:509, and removed
// rather than left behind as an unused type that a later reader would wire up
// without re-deriving why it was abandoned.

// knowledgeWriteAck is what add_knowledge answers with (inside
// addKnowledgeResult). search_knowledge and recall return the complete
// record.
//
// Dropped: content, tags, url — all supplied on this same call — and the
// embedding vector, which is 768 float32s the caller can do nothing with.
// importance is kept because the server assigns it and it drives the decay
// schedule.
type knowledgeWriteAck struct {
	ID            uuid.UUID          `json:"id"`
	Type          string             `json:"type"`
	Title         string             `json:"title"`
	Source        string             `json:"source"`
	LearningValue pgtype.Int4        `json:"learning_value"`
	Importance    float64            `json:"importance"`
	CreatedAt     pgtype.Timestamptz `json:"created_at"`
	UpdatedAt     pgtype.Timestamptz `json:"updated_at"`
}

func ackKnowledge(k *db.KnowledgeItem) *knowledgeWriteAck {
	if k == nil {
		return nil
	}
	return &knowledgeWriteAck{
		ID:            k.ID,
		Type:          k.Type,
		Title:         k.Title,
		Source:        k.Source,
		LearningValue: k.LearningValue,
		Importance:    k.Importance,
		CreatedAt:     k.CreatedAt,
		UpdatedAt:     k.UpdatedAt,
	}
}

// visionWriteAck is what update_vision_item answers with. list_vision_items
// returns the complete record.
//
// Dropped: why_blocked, context_md and depends_on. context_md is markdown
// notes and is by far the largest of them.
type visionWriteAck struct {
	ID              uuid.UUID           `json:"id"`
	Title           string              `json:"title"`
	Status          vision.VisionStatus `json:"status"`
	RepoName        string              `json:"repo_name,omitempty"`
	ProjectID       *uuid.UUID          `json:"project_id,omitempty"`
	PromotedTaskID  *uuid.UUID          `json:"promoted_task_id,omitempty"`
	LastDiscussedAt *time.Time          `json:"last_discussed_at,omitempty"`
	CreatedAt       time.Time           `json:"created_at"`
}

func ackVision(v *vision.VisionItem) *visionWriteAck {
	if v == nil {
		return nil
	}
	return &visionWriteAck{
		ID:              v.ID,
		Title:           v.Title,
		Status:          v.Status,
		RepoName:        v.RepoName,
		ProjectID:       v.ProjectID,
		PromotedTaskID:  v.PromotedTaskID,
		LastDiscussedAt: v.LastDiscussedAt,
		CreatedAt:       v.CreatedAt,
	}
}

// projectWriteAck is what create_project / update_project /
// update_project_status answer with. list_projects returns the complete
// record.
//
// Dropped: description only. Everything else on a project is a short
// identifier, an enum or a timestamp — dropping those would save nothing and
// cost the caller a round trip to learn a value the server had just coerced.
type projectWriteAck struct {
	ID        uuid.UUID          `json:"id"`
	GoalID    pgtype.UUID        `json:"goal_id"`
	Name      string             `json:"name"`
	Title     string             `json:"title"`
	Status    string             `json:"status"`
	Area      string             `json:"area"`
	Priority  int32              `json:"priority"`
	RepoName  pgtype.Text        `json:"repo_name"`
	CreatedAt pgtype.Timestamptz `json:"created_at"`
	UpdatedAt pgtype.Timestamptz `json:"updated_at"`
}

func ackProject(p *db.Project) *projectWriteAck {
	if p == nil {
		return nil
	}
	return &projectWriteAck{
		ID:        p.ID,
		GoalID:    p.GoalID,
		Name:      p.Name,
		Title:     p.Title,
		Status:    p.Status,
		Area:      p.Area,
		Priority:  p.Priority,
		RepoName:  p.RepoName,
		CreatedAt: p.CreatedAt,
		UpdatedAt: p.UpdatedAt,
	}
}

// goalWriteAck is what create_goal answers with. list_goals returns the
// complete record. Dropped: description, for the same reason as the project
// twin above.
type goalWriteAck struct {
	ID        uuid.UUID          `json:"id"`
	Title     string             `json:"title"`
	Status    string             `json:"status"`
	Area      pgtype.Text        `json:"area"`
	DueDate   pgtype.Timestamptz `json:"due_date"`
	CreatedAt pgtype.Timestamptz `json:"created_at"`
	UpdatedAt pgtype.Timestamptz `json:"updated_at"`
}

func ackGoal(g *db.Goal) *goalWriteAck {
	if g == nil {
		return nil
	}
	return &goalWriteAck{
		ID:        g.ID,
		Title:     g.Title,
		Status:    g.Status,
		Area:      g.Area,
		DueDate:   g.DueDate,
		CreatedAt: g.CreatedAt,
		UpdatedAt: g.UpdatedAt,
	}
}

// skillWriteAck is what add_skill and use_skill answer with. search_skills /
// list_relevant_skills return the complete record.
//
// Dropped: description, triggers, steps, failure_modes,
// verification_checklist, source_atom_ids and examples. examples is the one
// that matters most — it is an unbounded []any of caller-authored material
// and the largest thing on the row (PR #174 had to give it a FIFO cap for
// exactly that reason).
//
// success_count / failure_count / last_used_at are kept because use_skill
// exists to move them: they are the entire answer that call has to give.
type skillWriteAck struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	SuccessCount int        `json:"success_count"`
	FailureCount int        `json:"failure_count"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

func ackSkill(sk *skill.Skill) *skillWriteAck {
	if sk == nil {
		return nil
	}
	return &skillWriteAck{
		ID:           sk.ID,
		Name:         sk.Name,
		SuccessCount: sk.SuccessCount,
		FailureCount: sk.FailureCount,
		LastUsedAt:   sk.LastUsedAt,
		CreatedAt:    sk.CreatedAt,
		UpdatedAt:    sk.UpdatedAt,
	}
}

// proposalWriteAck is what the propose_* tools answer with.
// list_pending_proposals returns the complete record.
//
// Dropped: payload and reason. payload is the proposal — the caller just
// marshalled it and sent it — and it carries its own cap because it can be
// megabytes. status is kept even though it is always "pending" at creation:
// it is server-assigned, and a caller that assumes the value rather than
// reading it is the kind of assumption that breaks silently when the server
// starts auto-resolving anything.
type proposalWriteAck struct {
	ID         uuid.UUID          `json:"id"`
	Type       string             `json:"type"`
	Status     string             `json:"status"`
	ProposedBy pgtype.Text        `json:"proposed_by"`
	CreatedAt  pgtype.Timestamptz `json:"created_at"`
}

func ackProposal(p *db.PendingProposal) *proposalWriteAck {
	if p == nil {
		return nil
	}
	return &proposalWriteAck{
		ID:         p.ID,
		Type:       p.Type,
		Status:     p.Status,
		ProposedBy: p.ProposedBy,
		CreatedAt:  p.CreatedAt,
	}
}

// conceptWriteAck is what add_concept answers with. search_knowledge and the
// review tools return the complete record.
//
// Dropped: content and tags, both supplied on this same call. status,
// importance and the timestamps are kept because the server assigns them —
// importance in particular feeds the decay schedule, so it is a value the
// caller cannot compute and would otherwise have to re-read.
type conceptWriteAck struct {
	ID         uuid.UUID          `json:"id"`
	Title      string             `json:"title"`
	Status     string             `json:"status"`
	Importance float64            `json:"importance"`
	CreatedAt  pgtype.Timestamptz `json:"created_at"`
	UpdatedAt  pgtype.Timestamptz `json:"updated_at"`
}

func ackConcept(c *db.Concept) *conceptWriteAck {
	if c == nil {
		return nil
	}
	return &conceptWriteAck{
		ID:         c.ID,
		Title:      c.Title,
		Status:     c.Status,
		Importance: c.Importance,
		CreatedAt:  c.CreatedAt,
		UpdatedAt:  c.UpdatedAt,
	}
}
