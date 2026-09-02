package handler

import (
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/proposal"
)

// [F984-01] knowledgeMaxContentLen is the largest content this package accepts,
// and every accepted item is handed to AutoProposeConceptFromKnowledge, which
// marshals it into a pending_proposals payload guarded by
// proposal.MaxPayloadBytes. Size the guard below this limit and items in the
// gap are accepted here (HTTP 201) but their concept proposals are rejected --
// the silent regression that 1f07d74 fixed by widening the cap from 128 KB.
//
// This test compares the two constants on purpose instead of expressing a size
// relative to proposal.MaxPayloadBytes. The payload-cap tests in
// internal/proposal and internal/storage/sqlite are deliberately relative so
// that retuning the cap does not break them -- which is also why none of them
// pin its value: mutating the constant back to 128 KB leaves every one of them
// green. This is the one place that fails when the guard is shrunk back below
// the largest legitimate producer.
//
// Strictly greater, not equal: the stored payload is a JSON envelope around the
// content, so it is always somewhat larger than the raw bytes counted by
// knowledgeMaxContentLen.
//
// [F0902-51] [F0902-52] The envelope overhead is NOT a flat, small margin --
// before proposal.MarshalConceptCandidate existed (both marshal call sites
// used plain json.Marshal), a 1 MiB content field with as little as 20%
// density of `<`/`>`/`&` already exceeded proposal.MaxPayloadBytes. Each
// escaped `<`, `>`, or `&` costs 6 bytes instead of 1 (encoding/json's
// default HTML-escaping rewrites it to a 6-character Unicode escape
// sequence) -- measured: 2,097,286 bytes vs. the 2,097,152 byte cap, for a
// ConceptCandidate{Title: "some knowledge item title", Content: <1 MiB,
// exactly 20% '<'>, SourceItemID: <uuid>, SourceItemType: "article"}
// fixture (the struct's own field names/title/ids add a fixed 133-byte
// envelope on top of the content field alone). There was no headroom here
// for realistic HTML/code-snippet content. See
// internal/proposal/autopropose_marshal_test.go's
// TestMarshalConceptCandidate_DensityRegression for the exact reproduction.
//
// [F0902-51]'s fix: proposal.MarshalConceptCandidate disables HTML-escaping
// (encoding/json's Encoder.SetEscapeHTML(false)), so `<`, `>`, `&` cost 1
// byte each like any other printable ASCII character, and both marshal call
// sites use it instead of json.Marshal directly.
//
// Residual worst case, still true after the fix and NOT addressed by it:
// `"`, `\`, and `\n` each still cost 2 bytes (JSON string-escaping is
// mandatory regardless of SetEscapeHTML), and raw control characters plus
// the Unicode line/paragraph separators U+2028/U+2029 each still cost 6
// bytes (encoding/json always escapes these too, regardless of the
// HTML-escape setting). A content field that is almost entirely literal `"`
// characters can still exceed the cap after the fix (measured:
// MarshalConceptCandidate's output is 2,097,287 bytes for such a fixture,
// 135 bytes over the 2,097,152 cap -- see
// internal/proposal/autopropose_marshal_test.go's
// TestMarshalConceptCandidate_FullQuoteDensityExceedsCap). This is a known,
// accepted residual gap, not something this fix claims to close: no current
// producer (knowledge_handler.go, tools_knowledge.go) generates
// quote-saturated content -- both only reject raw control characters via
// sanitize.RejectControlChars, not quote/backslash density. Do NOT read
// this fix as making the cap safe for arbitrary content.
func TestProposalPayloadCapCoversKnowledgeContentLimit(t *testing.T) {
	if proposal.MaxPayloadBytes <= knowledgeMaxContentLen {
		t.Fatalf(
			"proposal.MaxPayloadBytes (%d) does not exceed knowledgeMaxContentLen (%d): "+
				"knowledge items between the two sizes are accepted by this handler but "+
				"their concept proposals are rejected by the payload guard, and the "+
				"caller is told neither",
			proposal.MaxPayloadBytes, knowledgeMaxContentLen,
		)
	}
}
