package ai_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
)

// TestDrafterAlternativesAcceptedBySeamDecoder [F205-04] is the drift guard
// between the producer and the consumer of a decision proposal: whatever
// ai.enforceDraftCaps lets through must be accepted by
// proposal.DecodeDecisionParams, the single decoder behind both the HTTP and
// the MCP accept paths. Before the per-alternative byte cap a CJK alternative
// of more than ~33 characters passed the drafter but was refused at accept
// time with "individual alternative exceeds 100 bytes".
//
// The payload is marshalled from proposal.DecisionProposerPayload — the type
// the producer itself writes (internal/mcp/middleware_decision_proposer.go
// aliases it) — not from a local copy of the JSON shape.
func TestDrafterAlternativesAcceptedBySeamDecoder(t *testing.T) {
	cases := []struct {
		name        string
		alternative string
	}{
		{"ASCII 200 bytes", strings.Repeat("a", 200)},
		{"ASCII 101 bytes, one past the seam cap", strings.Repeat("a", 101)},
		{"CJK 200 characters", strings.Repeat("決", 200)},
		{"CJK character straddling the cap", strings.Repeat("a", 98) + "決決決"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			draft := &ai.DecisionDraft{
				Title:        "adopt X",
				Decision:     "use X",
				Rationale:    "because Y",
				Alternatives: []string{tc.alternative, tc.alternative, tc.alternative},
			}
			rejected, reason, err := ai.EnforceDraftCapsForTest(draft)
			if rejected || err != nil {
				t.Fatalf("enforceDraftCaps rejected=%v reason=%q err=%v, want a pass", rejected, reason, err)
			}

			payload, err := json.Marshal(proposal.DecisionProposerPayload{
				Title:        draft.Title,
				Decision:     draft.Decision,
				Rationale:    draft.Rationale,
				Alternatives: draft.Alternatives,
				SessionID:    "sess-1",
				TriggerTool:  "complete_task",
			})
			if err != nil {
				t.Fatalf("marshal payload: %v", err)
			}
			if _, err := proposal.DecodeDecisionParams(payload); err != nil {
				t.Errorf("seam decoder rejected a drafter-capped payload: %v", err)
			}
		})
	}
}
