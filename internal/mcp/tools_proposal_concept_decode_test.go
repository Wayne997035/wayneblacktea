package mcp

import (
	"encoding/json"
	"os"
	"testing"
)

// TestMCPDecodeConceptPayload_SharedCases feeds every row of
// ../proposal/testdata/concept_payload_cases.json, byte for byte, to the MCP
// path's concept decoder and asserts accept versus reject only. The proposal
// package's DecodeConceptPayload and the HTTP handler's concept decoder read
// the same file, so changing the accept surface of any one of the three turns
// that decoder's test red. Error texts are not compared: they differ on
// purpose.
func TestMCPDecodeConceptPayload_SharedCases(t *testing.T) {
	raw, err := os.ReadFile("../proposal/testdata/concept_payload_cases.json")
	if err != nil {
		t.Fatalf("read shared cases: %v", err)
	}
	var cases []struct {
		Name    string `json:"name"`
		Payload string `json:"payload"`
		Accept  bool   `json:"accept"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("parse shared cases: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("shared case file has no cases")
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			_, errMsg := decodeConceptPayload([]byte(c.Payload))
			if c.Accept && errMsg != "" {
				t.Errorf("want accept, got error %q", errMsg)
			}
			if !c.Accept && errMsg == "" {
				t.Error("want reject, got accept")
			}
		})
	}
}
