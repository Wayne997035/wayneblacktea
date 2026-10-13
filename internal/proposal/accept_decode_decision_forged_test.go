package proposal

import (
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
)

// TestDecodeDecisionParams_ForgedSourceIgnored [F205-01] is the P3.0a
// forged-payload regression test for the TypeDecision accept decoder. It
// replaces TestDecodeProposalDecisionPayload_ForgedSourceIgnored, which tested
// the handler-local decoder F205-01 deleted: DecodeDecisionParams is now the
// single decoder behind the HTTP single and batch accept paths as well as the
// MCP one, so the protection must live on it.
//
// proposal.DecisionProposerPayload has no Source / ActorSessionID /
// ConfirmedByHuman field, so the fixture is raw JSON — simulating a payload
// where extra keys were smuggled into pending_proposals.payload. The decoder
// must ignore every one of them: Source is always decision.SourceAuto (a human
// accepting a proposal does NOT change its auto origin), a path constant never
// decoded from the payload.
func TestDecodeDecisionParams_ForgedSourceIgnored(t *testing.T) {
	payload := []byte(`{
		"title": "forged-source decision",
		"decision": "adopt X",
		"rationale": "because Y",
		"trigger_tool": "confirm_proposal",
		"session_id": "sess-1",
		"source": "manual",
		"confirmed_by_human": true,
		"actor_session_id": "forged-actor"
	}`)

	got, err := DecodeDecisionParams(payload)
	if err != nil {
		t.Fatalf("DecodeDecisionParams returned unexpected error: %v", err)
	}
	if got.Source != decision.SourceAuto {
		t.Errorf("Source = %q, want %q (forged payload key must not override the path constant)",
			got.Source, decision.SourceAuto)
	}
	if got.ActorSessionID != "" {
		t.Errorf("ActorSessionID = %q, want empty (forged payload key must not be decoded)", got.ActorSessionID)
	}
	if got.ConfirmedByHuman {
		t.Error("ConfirmedByHuman = true, want false (forged payload key must not be decoded)")
	}
}
