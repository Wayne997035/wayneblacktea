package proposal

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestDecodeConceptPayload pins DecodeConceptPayload directly (it used to run
// only incidentally inside the two materializeConcept nil-guard tests, with
// its outputs unasserted and no error-path coverage) and carries the recorded
// comparison of the three concept decoders in this repo:
//
//	Decoder 1: decodeConceptCandidatePayload in proposal_handler.go (the HTTP pre-tx gate; returns (struct, string))
//	Decoder 2: DecodeConceptPayload in accept_decode.go (the seam's in-tx decode; returns (struct, error))
//	Decoder 3: decodeConceptPayload in tools_proposal.go (the MCP path; returns (struct, string))
//
//	aspect                                   decoder 1                  decoder 2                       decoder 3
//	struct fields                            title content tags         title content tags              title content tags
//	                                         source_item_id/type        (the two source fields are      source_item_id/type
//	                                         (strings)                  not decoded into the result)    (strings)
//	title / content / tags caps              512 / 65536 / 50 (bytes, bytes, count) in all three
//	per-tag cap, non-empty title             none, none in all three
//	{}, null, empty tags                     ok, zero value in all three
//	empty bytes, []                          error in all three
//	source_item_id / source_item_type that   REJECT                     ACCEPT (field ignored)          REJECT
//	is not a string (123, ["x"])             <-- the one accept/reject divergence between the decoders
//	text: malformed JSON                    "concept proposal          "decoding concept payload: "    "decoding concept payload
//	                                         payload is malformed"      + raw json error                failed" (storeErrorText)
//	text: title cap                          "...exceeds 512            "...exceeds 512 bytes"          "...exceeds 512 bytes"
//	                                         characters" (counts bytes)
//	text: content / tags cap                 "concept content exceeds 64 KB" / "too many tags (max 50)" in all three
//
// The texts differ on purpose and are not asserted across decoders; what is
// pinned across all three is accept versus reject, by the shared case file.
func TestDecodeConceptPayload(t *testing.T) {
	// [F205-08] direct tests for DecodeConceptPayload plus the three-decoder comparison record above.
	mk := func(v any) []byte {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal fixture: %v", err)
		}
		return b
	}
	cjk170 := strings.Repeat("中", 170) // 510 bytes: the title cap counts bytes, not runes
	cjk171 := strings.Repeat("中", 171) // 513 bytes
	tag101 := strings.Repeat("t", 101) // no per-tag cap (contrast DecodeKnowledgePayload)

	cases := []struct {
		name    string
		payload []byte
		want    ConceptPayload
		wantErr string // substring of the error; "" means the decode must succeed
	}{
		{
			name:    "valid full payload",
			payload: mk(map[string]any{"title": "adopt X", "content": "because Y", "tags": []string{"go", "learning"}}),
			want:    ConceptPayload{Title: "adopt X", Content: "because Y", Tags: []string{"go", "learning"}},
		},
		{
			// Characterisation, NOT an endorsement: an empty object decodes to the zero value.
			name:    "empty object decodes to the zero value",
			payload: []byte(`{}`),
			want:    ConceptPayload{},
		},
		{
			name:    "null decodes to the zero value",
			payload: []byte(`null`),
			want:    ConceptPayload{},
		},
		{
			name:    "title exactly 512 bytes is accepted",
			payload: mk(map[string]any{"title": strings.Repeat("a", 512)}),
			want:    ConceptPayload{Title: strings.Repeat("a", 512)},
		},
		{
			name:    "title 513 bytes is rejected",
			payload: mk(map[string]any{"title": strings.Repeat("a", 513)}),
			wantErr: "concept title exceeds 512 bytes",
		},
		{
			name:    "170 CJK characters (510 bytes) are accepted",
			payload: mk(map[string]any{"title": cjk170}),
			want:    ConceptPayload{Title: cjk170},
		},
		{
			name:    "171 CJK characters (513 bytes) are rejected: caps count bytes",
			payload: mk(map[string]any{"title": cjk171}),
			wantErr: "concept title exceeds 512 bytes",
		},
		{
			name:    "content exactly 65536 bytes is accepted",
			payload: mk(map[string]any{"title": "t", "content": strings.Repeat("c", 65536)}),
			want:    ConceptPayload{Title: "t", Content: strings.Repeat("c", 65536)},
		},
		{
			name:    "content 65537 bytes is rejected",
			payload: mk(map[string]any{"title": "t", "content": strings.Repeat("c", 65537)}),
			wantErr: "concept content exceeds 64 KB",
		},
		{
			name:    "50 tags are accepted",
			payload: mk(map[string]any{"title": "t", "tags": make([]string, 50)}),
			want:    ConceptPayload{Title: "t", Tags: make([]string, 50)},
		},
		{
			name:    "51 tags are rejected",
			payload: mk(map[string]any{"title": "t", "tags": make([]string, 51)}),
			wantErr: "too many tags (max 50)",
		},
		{
			name:    "a 101-byte tag is accepted: there is no per-tag cap",
			payload: mk(map[string]any{"title": "t", "tags": []string{tag101}}),
			want:    ConceptPayload{Title: "t", Tags: []string{tag101}},
		},
		{
			name:    "source_item_* strings are ignored: the result carries only title, content, tags",
			payload: []byte(`{"title":"t","content":"c","tags":["a"],"source_item_id":"abc","source_item_type":"knowledge"}`),
			want:    ConceptPayload{Title: "t", Content: "c", Tags: []string{"a"}},
		},
		{name: "malformed JSON is rejected", payload: []byte(`{bad`), wantErr: "decoding concept payload"},
		{name: "a wrong-typed title is rejected", payload: []byte(`{"title":123}`), wantErr: "decoding concept payload"},
		{name: "a JSON array is rejected", payload: []byte(`[]`), wantErr: "decoding concept payload"},
		{name: "nil bytes are rejected", payload: nil, wantErr: "decoding concept payload"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeConceptPayload(tc.payload)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("DecodeConceptPayload: want error containing %q, got nil (result %+v)", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeConceptPayload: unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("DecodeConceptPayload = %+v, want %+v", got, tc.want)
			}
		})
	}
}
