package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestDecodeGoalParams_LengthCaps mirrors
// internal/proposal/accept_decode_length_test.go's TestDecodeGoalParams_LengthCaps
// for this package's private decodeGoalParams (byte-identical caps, string
// error-message return convention instead of error — see
// backend-security-design.md §2.1: this decoder is reachable from any MCP
// client calling propose_goal → confirm_proposal, which a prompt-injected
// agent controls).
func TestDecodeGoalParams_LengthCaps(t *testing.T) {
	cases := []struct {
		name       string
		payload    map[string]any
		wantErrMsg bool
		wantSubstr string
	}{
		{
			name:    "within limits → ok",
			payload: map[string]any{"title": "Become CEO", "area": "career"},
		},
		{
			name:       "title exceeds 512 bytes → rejected",
			payload:    map[string]any{"title": strings.Repeat("a", 513), "area": "career"},
			wantErrMsg: true,
			wantSubstr: "goal title exceeds 512 bytes",
		},
		{
			name:       "description exceeds 64 KB → rejected",
			payload:    map[string]any{"title": "ok", "description": strings.Repeat("b", 65537)},
			wantErrMsg: true,
			wantSubstr: "goal description exceeds 64 KB",
		},
		{
			// M1 (round-2 security review): area had no length cap while
			// title/description did, so a prompt-injected agent could stuff
			// unbounded content into area to bypass the other two caps.
			name:       "area exceeds 512 bytes → rejected",
			payload:    map[string]any{"title": "ok", "area": strings.Repeat("z", 513)},
			wantErrMsg: true,
			wantSubstr: "goal area exceeds 512 bytes",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.payload)
			if err != nil {
				t.Fatalf("marshal fixture: %v", err)
			}
			_, errMsg := decodeGoalParams(raw)
			if tc.wantErrMsg {
				if !strings.Contains(errMsg, tc.wantSubstr) {
					t.Errorf("errMsg = %q, want substring %q", errMsg, tc.wantSubstr)
				}
				return
			}
			if errMsg != "" {
				t.Fatalf("decodeGoalParams: unexpected errMsg: %s", errMsg)
			}
		})
	}
}

// TestDecodeProjectParams_LengthCaps mirrors TestDecodeGoalParams_LengthCaps
// for decodeProjectParams's Name/Title/Description caps.
func TestDecodeProjectParams_LengthCaps(t *testing.T) {
	cases := []struct {
		name       string
		payload    map[string]any
		wantErrMsg bool
		wantSubstr string
	}{
		{
			name:    "within limits → ok",
			payload: map[string]any{"name": "proj", "title": "Project", "area": "projects"},
		},
		{
			name:       "name exceeds 512 bytes → rejected",
			payload:    map[string]any{"name": strings.Repeat("a", 513), "title": "ok"},
			wantErrMsg: true,
			wantSubstr: "project name exceeds 512 bytes",
		},
		{
			name:       "title exceeds 512 bytes → rejected",
			payload:    map[string]any{"name": "ok", "title": strings.Repeat("b", 513)},
			wantErrMsg: true,
			wantSubstr: "project title exceeds 512 bytes",
		},
		{
			name:       "description exceeds 64 KB → rejected",
			payload:    map[string]any{"name": "ok", "title": "ok", "description": strings.Repeat("c", 65537)},
			wantErrMsg: true,
			wantSubstr: "project description exceeds 64 KB",
		},
		{
			// M1 (round-2 security review): same area-cap bypass as
			// TestDecodeGoalParams_LengthCaps's "area exceeds 512 bytes" case.
			name:       "area exceeds 512 bytes → rejected",
			payload:    map[string]any{"name": "ok", "title": "ok", "area": strings.Repeat("z", 513)},
			wantErrMsg: true,
			wantSubstr: "project area exceeds 512 bytes",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.payload)
			if err != nil {
				t.Fatalf("marshal fixture: %v", err)
			}
			_, errMsg := decodeProjectParams(raw)
			if tc.wantErrMsg {
				if !strings.Contains(errMsg, tc.wantSubstr) {
					t.Errorf("errMsg = %q, want substring %q", errMsg, tc.wantSubstr)
				}
				return
			}
			if errMsg != "" {
				t.Fatalf("decodeProjectParams: unexpected errMsg: %s", errMsg)
			}
		})
	}
}
