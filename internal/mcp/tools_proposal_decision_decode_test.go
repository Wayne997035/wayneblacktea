package mcp

import (
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
)

// TestDecodeDecisionParams_ForgedSourceIgnored is the P3.0a forged-payload
// regression test for producer #8 (decodeDecisionParams), which covers all 3
// MCP TypeDecision materialiser call sites. proposal.DecisionProposerPayload
// has no Source field, so this constructs the raw JSON bytes directly —
// simulating a payload where an attacker (or any future code path that
// stores raw bytes into pending_proposals.payload) smuggled in an extra
// "source" key. decodeDecisionParams must ignore it: Source is always
// decision.SourceAuto for this materialiser, a path constant never decoded
// from the payload.
func TestDecodeDecisionParams_ForgedSourceIgnored(t *testing.T) {
	payload := []byte(`{
		"title": "forged-source decision",
		"decision": "adopt X",
		"rationale": "because Y",
		"trigger_tool": "complete_task",
		"session_id": "sess-1",
		"source": "manual"
	}`)

	got, errMsg := decodeDecisionParams(payload)
	if errMsg != "" {
		t.Fatalf("decodeDecisionParams returned unexpected error: %s", errMsg)
	}
	if got.Source != decision.SourceAuto {
		t.Errorf("Source = %q, want %q (forged payload key must not override the path constant)",
			got.Source, decision.SourceAuto)
	}
}
