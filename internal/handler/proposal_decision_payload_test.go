package handler

import (
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
)

// TestDecodeProposalDecisionPayload_ForgedSourceIgnored is the P3.0a
// forged-payload regression test for producer #9 (decodeProposalDecisionPayload),
// which covers the HTTP single (acceptDecision) and batch (batchResolveOne)
// TypeDecision accept paths. proposal.DecisionProposerPayload has no Source
// field, so this constructs raw JSON bytes directly — simulating a payload
// where an extra "source" key was smuggled into pending_proposals.payload.
// decodeProposalDecisionPayload must ignore it: Source is always
// decision.SourceAuto for this materialiser (a human accepting a proposal
// does NOT change its auto origin), a path constant never decoded from the
// payload.
func TestDecodeProposalDecisionPayload_ForgedSourceIgnored(t *testing.T) {
	payload := []byte(`{
		"title": "forged-source decision",
		"decision": "adopt X",
		"rationale": "because Y",
		"trigger_tool": "confirm_proposal",
		"session_id": "sess-1",
		"source": "manual"
	}`)

	got, errMsg := decodeProposalDecisionPayload(payload)
	if errMsg != "" {
		t.Fatalf("decodeProposalDecisionPayload returned unexpected error: %s", errMsg)
	}
	if got.Source != decision.SourceAuto {
		t.Errorf("Source = %q, want %q (forged payload key must not override the path constant)",
			got.Source, decision.SourceAuto)
	}
}
