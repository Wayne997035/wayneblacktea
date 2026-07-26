package mcp

import (
	"encoding/json"
	"strings"
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

// TestDecodeDecisionParams_LengthCaps mirrors
// internal/proposal/accept_decode_length_test.go's
// TestDecodeDecisionParams_LengthCaps for this package's private
// decodeDecisionParams (byte-identical caps, string error-message return
// convention instead of error).
func TestDecodeDecisionParams_LengthCaps(t *testing.T) {
	cases := []struct {
		name       string
		payload    map[string]any
		wantErrMsg bool
		wantSubstr string
	}{
		{
			name:    "within limits → ok",
			payload: map[string]any{"title": "adopt X", "decision": "adopt X", "rationale": "because Y", "alternatives": []string{"Y", "Z"}},
		},
		{
			name:       "title exceeds 512 bytes → rejected",
			payload:    map[string]any{"title": strings.Repeat("a", 513)},
			wantErrMsg: true,
			wantSubstr: "decision title exceeds 512 bytes",
		},
		{
			name:       "decision text exceeds 64 KB → rejected",
			payload:    map[string]any{"title": "ok", "decision": strings.Repeat("b", 65537)},
			wantErrMsg: true,
			wantSubstr: "decision text exceeds 64 KB",
		},
		{
			name:       "rationale exceeds 64 KB → rejected",
			payload:    map[string]any{"title": "ok", "rationale": strings.Repeat("c", 65537)},
			wantErrMsg: true,
			wantSubstr: "decision rationale exceeds 64 KB",
		},
		{
			name:       "too many alternatives → rejected",
			payload:    map[string]any{"title": "ok", "alternatives": makeAltStrings(51, "x")},
			wantErrMsg: true,
			wantSubstr: "too many alternatives (max 50)",
		},
		{
			name:       "single alternative exceeds 100 bytes → rejected",
			payload:    map[string]any{"title": "ok", "alternatives": []string{strings.Repeat("d", 101)}},
			wantErrMsg: true,
			wantSubstr: "individual alternative exceeds 100 bytes",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.payload)
			if err != nil {
				t.Fatalf("marshal fixture: %v", err)
			}
			_, errMsg := decodeDecisionParams(raw)
			if tc.wantErrMsg {
				if !strings.Contains(errMsg, tc.wantSubstr) {
					t.Errorf("errMsg = %q, want substring %q", errMsg, tc.wantSubstr)
				}
				return
			}
			if errMsg != "" {
				t.Fatalf("decodeDecisionParams: unexpected errMsg: %s", errMsg)
			}
		})
	}
}

// makeAltStrings returns n copies of val — used to build an oversized
// Alternatives slice fixture without a literal 51-element list.
func makeAltStrings(n int, val string) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = val
	}
	return out
}
