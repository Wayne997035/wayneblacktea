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
