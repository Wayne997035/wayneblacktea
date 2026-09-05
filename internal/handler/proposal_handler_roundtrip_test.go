package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
)

// jsonEscapeSequence builds the literal N-character ASCII text of an
// encoding/json Unicode escape sequence (e.g. backslash, u, 0, 0, 3, c) from
// individual bytes instead of a string literal containing the sequence
// itself. See internal/proposal/autopropose_marshal_test.go's copy of this
// helper for why: a source string literal containing this exact escape text
// is, to some tooling in this environment, indistinguishable from the
// actual control character it denotes and gets silently substituted for it.
func jsonEscapeSequence(hex4 string) []byte {
	b := []byte{'\\', 'u'}
	b = append(b, hex4...)
	return b
}

// TestPendingProposalResponse_PayloadStillHTMLEscapedOnOutput — [F0902-51]
// spec row 5 (Acceptance table row 5)
// and Risk flag D4.
//
// proposal.MarshalConceptCandidate (autopropose_marshal_test.go) disables
// HTML-escaping at the STORAGE step, but Echo's DefaultJSONSerializer
// (echo/v4@v4.15.1/json.go:17-24, confirmed by reading the vendored source)
// calls bare json.NewEncoder(c.Response()).Encode(i) with no
// SetEscapeHTML(false), and encoding/json re-escapes embedded
// json.RawMessage bytes via compact() at the outer encode regardless of how
// they were originally escaped. This test pins that: a proposal whose
// payload was produced by MarshalConceptCandidate with content containing
// <script>a&b</script> still comes back HTML-escaped over the wire, and
// still round-trips to the original unescaped string on decode — no
// response field, shape, or escaping visible to any client changes as a
// result of this ticket's storage-layer fix.
func TestPendingProposalResponse_PayloadStillHTMLEscapedOnOutput(t *testing.T) {
	const rawContent = "<script>a&b</script>"
	cc := proposal.ConceptCandidate{Title: "t", Content: rawContent}
	payload, err := proposal.MarshalConceptCandidate(cc)
	if err != nil {
		t.Fatalf("MarshalConceptCandidate: %v", err)
	}
	if bytes.Contains(payload, jsonEscapeSequence("003c")) {
		t.Fatalf("stored payload %s is HTML-escaped — test fixture is wrong, "+
			"MarshalConceptCandidate should leave <script> raw at the storage step", payload)
	}

	row := db.PendingProposal{
		ID:      uuid.New(),
		Type:    "concept",
		Status:  "pending",
		Payload: payload,
	}
	store := &fakeProposalStore{
		byID: map[uuid.UUID]*db.PendingProposal{row.ID: &row},
		all:  []db.PendingProposal{row},
	}
	e := newEcho()
	h := handler.NewProposalHandler(store, &fakeProposalLearningStore{})
	e.GET("/api/proposals", h.ListProposals)
	rec := performRequest(e, http.MethodGet, "/api/proposals?status=all", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.Bytes()

	// Echo's outer encode re-escapes the embedded RawMessage: the response
	// body must contain the escaped form of '<', NOT the raw '<' the
	// payload was stored with.
	wantEscapedScript := append(append(jsonEscapeSequence("003c"), []byte("script")...), jsonEscapeSequence("003e")...)
	if !bytes.Contains(body, wantEscapedScript) {
		t.Errorf("response body does not contain the HTML-escaped <script> form (body: %s) — "+
			"if this starts failing, either Echo's serializer started calling SetEscapeHTML(false) or "+
			"toResponse stopped using json.RawMessage; either way this response's escaping behavior changed",
			body)
	}
	if bytes.Contains(body, []byte("<script>")) {
		t.Errorf("response body contains literal unescaped <script> (body: %s) — "+
			"payload should have been re-escaped by Echo's outer JSON encode", body)
	}

	// Round-trip: decoding the response must still recover the original,
	// unescaped content string byte-for-byte.
	var items []struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(body, &items); err != nil {
		t.Fatalf("response not valid JSON array: %v (body: %s)", err, body)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	var decoded struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(items[0].Payload, &decoded); err != nil {
		t.Fatalf("decode payload: %v (payload: %s)", err, items[0].Payload)
	}
	if decoded.Content != rawContent {
		t.Errorf("round-tripped content = %q, want %q", decoded.Content, rawContent)
	}
}
