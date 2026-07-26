package proposal

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/validator"
	"github.com/google/uuid"
)

// This file holds the payload-decode helpers shared by pgAcceptAdapter
// (accept_pg.go, same package) and sqliteAcceptAdapter
// (internal/storage/sqlite/accept_proposal.go, which already imports this
// package). They exist here — exported, distinct from
// internal/mcp/tools_proposal.go's private decodeGoalParams et al. — for two
// reasons:
//
//  1. internal/mcp already imports internal/proposal (it's the consumer of
//     this package's types), so internal/proposal importing internal/mcp to
//     reuse its private decoders would cycle.
//  2. The SQLite adapter lives in a different package
//     (internal/storage/sqlite, forced there by a separate import-cycle
//     constraint — see accept_pg.go's PgAcceptDeps doc comment) but needs
//     the SAME decode logic for the 4 proposal types it also materialises
//     (Goal/Project/Concept/Decision), so duplicating decode between the two
//     new adapters would immediately drift.
//
// The mcp-package originals are untouched (out of scope for this task) and
// keep serving the current (not yet seam-routed) acceptProposal path; these
// are this seam's own copy, not a replacement, until a later task rewires
// acceptProposal to call AcceptSeam.

// goalPayload is the JSONB shape stored in pending_proposals when
// type=goal. Mirrors internal/mcp/tools_proposal.go's private goalPayload.
type goalPayload struct {
	Title       string `json:"title"`
	Area        string `json:"area"`
	Description string `json:"description,omitempty"`
	DueDate     string `json:"due_date,omitempty"` // RFC3339; empty → no due date
}

// projectPayload is the JSONB shape stored in pending_proposals when
// type=project. Mirrors internal/mcp/tools_proposal.go's private
// projectPayload.
type projectPayload struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Area        string `json:"area"`
	Description string `json:"description,omitempty"`
	GoalID      string `json:"goal_id,omitempty"` // UUID string
	Priority    int32  `json:"priority,omitempty"`
}

// ConceptPayload is the JSONB shape stored in pending_proposals when
// type=concept. Exported (unlike goalPayload/projectPayload above) because
// DecodeConceptPayload returns it directly to the caller, which lives in a
// different package for the SQLite adapter. Mirrors
// internal/mcp/tools_proposal.go's private conceptPayload (SourceItemID /
// SourceItemType omitted — unused by either adapter's materialise step).
type ConceptPayload struct {
	Title   string   `json:"title"`
	Content string   `json:"content"`
	Tags    []string `json:"tags,omitempty"`
}

// taskTitleMaxLen mirrors internal/mcp's mcpTaskMaxTitle (middleware_classify.go).
const taskTitleMaxLen = 500

// DecodeGoalParams decodes a type=goal pending_proposals.payload into
// gtd.CreateGoalParams. Ported from internal/mcp/tools_proposal.go's
// decodeGoalParams (identical validation; `error` instead of a `string`
// error-message return since this is new code, not a byte-identical port).
func DecodeGoalParams(payload []byte) (gtd.CreateGoalParams, error) {
	var p goalPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return gtd.CreateGoalParams{}, fmt.Errorf("decoding goal payload: %w", err)
	}
	gp := gtd.CreateGoalParams{
		Title:       p.Title,
		Area:        p.Area,
		Description: p.Description,
	}
	if p.DueDate != "" {
		t, err := time.Parse(time.RFC3339, p.DueDate)
		if err != nil {
			return gtd.CreateGoalParams{}, fmt.Errorf("invalid due_date in payload: %w", err)
		}
		gp.DueDate = &t
	}
	return gp, nil
}

// DecodeProjectParams decodes a type=project pending_proposals.payload into
// gtd.CreateProjectParams. Ported from internal/mcp/tools_proposal.go's
// decodeProjectParams.
func DecodeProjectParams(payload []byte) (gtd.CreateProjectParams, error) {
	var p projectPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return gtd.CreateProjectParams{}, fmt.Errorf("decoding project payload: %w", err)
	}
	pp := gtd.CreateProjectParams{
		Name:        p.Name,
		Title:       p.Title,
		Area:        p.Area,
		Description: p.Description,
		Priority:    p.Priority,
	}
	if p.GoalID != "" {
		gid, err := uuid.Parse(p.GoalID)
		if err != nil {
			return gtd.CreateProjectParams{}, fmt.Errorf("invalid goal_id in payload: %w", err)
		}
		pp.GoalID = &gid
	}
	return pp, nil
}

// DecodeConceptPayload decodes and validates a type=concept
// pending_proposals.payload. Ported from
// internal/mcp/tools_proposal.go's decodeConceptPayload (same length caps).
func DecodeConceptPayload(payload []byte) (ConceptPayload, error) {
	var p ConceptPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return ConceptPayload{}, fmt.Errorf("decoding concept payload: %w", err)
	}
	if len(p.Title) > 512 {
		return ConceptPayload{}, fmt.Errorf("concept title exceeds 512 bytes")
	}
	if len(p.Content) > 65536 {
		return ConceptPayload{}, fmt.Errorf("concept content exceeds 64 KB")
	}
	if len(p.Tags) > 50 {
		return ConceptPayload{}, fmt.Errorf("too many tags (max 50)")
	}
	return p, nil
}

// DecodeDecisionParams decodes a type=decision pending_proposals.payload
// (DecisionProposerPayload, defined in payload.go, same package) into
// decision.LogParams. Ported from internal/mcp/tools_proposal.go's
// decodeDecisionParams, alternatives-into-rationale concatenation included.
func DecodeDecisionParams(payload []byte) (decision.LogParams, error) {
	var p DecisionProposerPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return decision.LogParams{}, fmt.Errorf("decoding decision payload: %w", err)
	}
	if p.Title == "" {
		return decision.LogParams{}, fmt.Errorf("decision payload missing title")
	}
	rationale := p.Rationale
	if len(p.Alternatives) > 0 {
		rationale += "\n\nAlternatives considered:"
		for _, a := range p.Alternatives {
			rationale += "\n- " + a
		}
	}
	return decision.LogParams{
		Title:     p.Title,
		Context:   fmt.Sprintf("auto-proposed by trigger_tool=%s session=%s", p.TriggerTool, p.SessionID),
		Decision:  p.Decision,
		Rationale: rationale,
		// A human accepting a proposal does NOT change its auto origin — see
		// the identical comment on internal/mcp/tools_proposal.go's
		// decodeDecisionParams.
		Source: decision.SourceAuto,
	}, nil
}

// DecodeTaskParams decodes a type=task pending_proposals.payload
// (TaskPayload, defined in payload.go) into gtd.CreateTaskParams, running the
// same validator.CheckTaskInput vagueness check
// internal/mcp/tools_proposal.go's decodeTaskProposalParams runs. strict
// mirrors WBT_STRICT_VAGUENESS (validator.StrictModeEnabled()): true turns
// warnings into a fatal error, false returns them alongside ok params.
func DecodeTaskParams(payload []byte, strict bool) (gtd.CreateTaskParams, []string, error) {
	var tp TaskPayload
	if err := json.Unmarshal(payload, &tp); err != nil {
		return gtd.CreateTaskParams{}, nil, fmt.Errorf("decoding task payload: %w", err)
	}
	if tp.Title == "" {
		return gtd.CreateTaskParams{}, nil, fmt.Errorf("task payload missing title")
	}
	if len(tp.Title) > taskTitleMaxLen {
		return gtd.CreateTaskParams{}, nil, fmt.Errorf("task title exceeds %d characters", taskTitleMaxLen)
	}

	kind := tp.SuggestedKind
	if kind == "" {
		kind = validator.KindGeneral
	}
	if !validator.IsValidKind(kind) {
		kind = validator.KindGeneral
	}

	warnings := validator.CheckTaskInput(tp.Description, kind)
	if len(warnings) > 0 && strict {
		return gtd.CreateTaskParams{}, warnings, fmt.Errorf("vagueness check failed: %v", warnings)
	}

	return gtd.CreateTaskParams{
		Title:       tp.Title,
		Description: tp.Description,
		Kind:        kind,
	}, warnings, nil
}

// DecodeKnowledgePayload decodes and validates a type=knowledge
// pending_proposals.payload (KnowledgePayload, defined in payload.go).
// Ported from internal/mcp/tools_proposal.go's decodeKnowledgePayload (same
// length caps).
func DecodeKnowledgePayload(payload []byte) (KnowledgePayload, error) {
	var kp KnowledgePayload
	if err := json.Unmarshal(payload, &kp); err != nil {
		return KnowledgePayload{}, fmt.Errorf("decoding knowledge payload: %w", err)
	}
	if kp.Title == "" {
		return KnowledgePayload{}, fmt.Errorf("knowledge payload missing title")
	}
	if len(kp.Title) > 512 {
		return KnowledgePayload{}, fmt.Errorf("knowledge title exceeds 512 bytes")
	}
	if len(kp.Content) > 65536 {
		return KnowledgePayload{}, fmt.Errorf("knowledge content exceeds 64 KB")
	}
	if len(kp.Tags) > 50 {
		return KnowledgePayload{}, fmt.Errorf("too many tags (max 50)")
	}
	for _, tag := range kp.Tags {
		if len(tag) > 100 {
			return KnowledgePayload{}, fmt.Errorf("individual tag exceeds 100 bytes")
		}
	}
	return kp, nil
}
