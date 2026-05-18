package proposal

// DecisionProposerPayload is the JSONB shape persisted into pending_proposals
// when type='decision'. It is created by the auto-decision-proposer middleware
// (internal/mcp/middleware_decision_proposer.go) AFTER a successful mutating
// MCP tool call when no log_decision/confirm_plan happened in the recent
// window. confirm_proposal materialises an accepted row into the `decisions`
// table by calling decision.Store.Log with the matching fields.
//
// Lives in the proposal package (rather than internal/mcp) so both the
// middleware (writer) and the materialiser in tools_proposal.go (reader) can
// import the same shape without a circular dependency
// (proposal MUST NOT import mcp, but mcp already imports proposal).
type DecisionProposerPayload struct {
	Title        string   `json:"title"`
	Decision     string   `json:"decision"`
	Rationale    string   `json:"rationale"`
	Alternatives []string `json:"alternatives,omitempty"`
	SessionID    string   `json:"session_id"`
	TriggerTool  string   `json:"trigger_tool"`
}

// KnowledgePayload is the JSONB shape stored in pending_proposals.payload
// when type='knowledge'. Written by the reflection cron job
// (internal/scheduler/reflection.go) and read by the confirm_proposal
// materialiser. Centralised here so scheduler and materialiser share the same
// wire format without a cross-package import.
type KnowledgePayload struct {
	Title   string   `json:"title"`
	Content string   `json:"content"`
	Tags    []string `json:"tags,omitempty"`
}

// TaskPayload is the JSONB shape stored in pending_proposals.payload when
// type='task'. Written by the auto-capture paths (internal/mcp/middleware_classify.go
// autoCaptureMCPTask + internal/handler/autolog_handler.go autoCreateTask) so an
// LLM classifier's IsTask=true verdict goes through the user review queue
// instead of bypassing the validator that handleAddTask runs.
//
// SuggestedKind defaults to "general" — the classifier doesn't predict task
// kind. confirm_proposal materialises the row via gtd.Store.CreateTask only
// after running validator.CheckVagueness / CheckKindFields against Title +
// (optional) Description that the user may have edited during review.
//
// Lives in the proposal package so both producers (mcp / handler) and the
// confirm materialiser (in tools_proposal.go + proposal_handler.go) share the
// same wire format without a circular dep.
type TaskPayload struct {
	Title               string `json:"title"`
	SourceTool          string `json:"source_tool"`
	ArgSummary          string `json:"arg_summary,omitempty"`
	ResultSummary       string `json:"result_summary,omitempty"`
	ClassifierRationale string `json:"classifier_rationale,omitempty"`
	SuggestedKind       string `json:"suggested_kind,omitempty"`
	Description         string `json:"description,omitempty"`
}

// MaxTaskPayloadBytes caps the JSON-encoded TaskPayload size before persist.
// 4 KiB is comfortably above the redacted arg/result summaries (each ≤ 500/300
// runes) and any classifier rationale, but small enough to bound DB row size
// in a high-frequency auto-capture loop.
const MaxTaskPayloadBytes = 4 * 1024
