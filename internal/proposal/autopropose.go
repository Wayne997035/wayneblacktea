package proposal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/Wayne997035/wayneblacktea/internal/db"
)

// ConceptCandidate is the on-disk shape of a concept proposal payload.
// Stored as JSONB inside pending_proposals.payload when type='concept'.
type ConceptCandidate struct {
	Title          string   `json:"title"`
	Content        string   `json:"content"`
	Tags           []string `json:"tags,omitempty"`
	SourceItemID   string   `json:"source_item_id,omitempty"`   // knowledge_items.id that triggered the proposal
	SourceItemType string   `json:"source_item_type,omitempty"` // "article" / "til" / etc.
}

// MarshalConceptCandidate marshals c into JSON with HTML-escaping disabled.
//
// [F0902-51] Plain json.Marshal (encoding/json's default) HTML-escapes `<`,
// `>`, `&` — each becomes a 6-character Unicode escape sequence (backslash,
// u, then 4 hex digits) instead of the original single byte. A knowledge
// item's Content field can legitimately be up to 1 MiB
// (knowledgeMaxContentLen, internal/handler/knowledge_handler.go) and, for
// realistic HTML/code-snippet content, that inflation alone can push the
// marshaled ConceptCandidate payload past proposal.MaxPayloadBytes even
// though the raw content is well under it (measured: a 1 MiB content field
// with just 20% `<` density already marshals past the 2 MiB cap — see
// autopropose_marshal_test.go's TestMarshalConceptCandidate_DensityRegression
// for the exact reproduction). The failure was silent: Store.Create's
// existing size guard (store.go's MaxPayloadBytes check) correctly rejected
// the oversized payload, but AutoProposeConceptFromKnowledge's callers treat
// that as best-effort/non-fatal, so a legitimate knowledge item was accepted
// (HTTP 201 / MCP success) while its concept proposal silently never
// existed.
//
// Both marshal call sites for ConceptCandidate (this file and
// internal/storage/sqlite/proposal.go, the SQLite backend's separate Create
// implementation) MUST call this helper instead of json.Marshal directly —
// single source of truth, mirroring the existing shared MaxPayloadBytes
// constant (store.go: "so internal/storage/sqlite's ProposalStore ...
// enforces the identical byte limit from one source of truth").
//
// Residual risk NOT addressed by this helper, still true after it: `"`,
// `\`, and `\n` each still cost 2 bytes (mandatory JSON string-escaping,
// independent of SetEscapeHTML), and raw control characters plus the
// Unicode line/paragraph separators U+2028/U+2029 each still cost 6 bytes
// (each becomes a 6-character Unicode escape sequence, same shape as the
// `<`/`>`/`&` case above) — encoding/json always escapes these regardless
// of the HTML-escape setting. A content field that is almost entirely literal `"` characters
// can still exceed MaxPayloadBytes after this fix (measured: 2,097,287
// bytes for such a fixture, 135 bytes over the 2,097,152 cap — see
// TestMarshalConceptCandidate_FullQuoteDensityExceedsCap). This is a known,
// accepted residual gap, not something this helper claims to close — see
// internal/handler/knowledge_payload_cap_invariant_test.go's comment.
func MarshalConceptCandidate(c ConceptCandidate) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(c); err != nil {
		return nil, fmt.Errorf("marshaling concept candidate: %w", err)
	}
	// Encoder.Encode appends a trailing '\n' that plain json.Marshal does
	// not; strip it so the output is byte-equivalent to json.Marshal minus
	// HTML-escaping (callers compare this length directly against
	// MaxPayloadBytes and against plain json.Marshal's output in tests).
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// ShouldAutoProposeFor returns true when a knowledge item type is suitable for
// becoming a spaced-repetition concept. Pure bookmarks (just a saved URL with
// little content) are excluded — proposing them as review cards is noise.
func ShouldAutoProposeFor(item *db.KnowledgeItem) bool {
	if item == nil || item.Title == "" {
		return false
	}
	switch item.Type {
	case "article", "til", "zettelkasten":
		return true
	default: // bookmark, or anything else
		return false
	}
}

// AutoProposeConceptFromKnowledge creates a pending concept proposal from a
// freshly added knowledge item. The caller decides whether to expose the
// returned proposal ID to its consumer.
//
// Errors are returned to the caller so they can decide whether to fail the
// outer request (e.g. MCP) or fail-soft (e.g. HTTP, where the knowledge item
// is already created and shouldn't be lost just because the proposal failed).
func (s *Store) AutoProposeConceptFromKnowledge(
	ctx context.Context, item *db.KnowledgeItem, proposedBy string,
) (*db.PendingProposal, error) {
	if !ShouldAutoProposeFor(item) {
		return nil, nil //nolint:nilnil // sentinel: caller treats nil as "no proposal needed, not an error"
	}
	payload, err := MarshalConceptCandidate(ConceptCandidate{
		Title:          item.Title,
		Content:        item.Content,
		Tags:           item.Tags,
		SourceItemID:   item.ID.String(),
		SourceItemType: item.Type,
	})
	if err != nil {
		return nil, fmt.Errorf("marshaling concept payload: %w", err)
	}
	return s.Create(ctx, CreateParams{
		Type:       TypeConcept,
		Payload:    payload,
		ProposedBy: proposedBy,
	})
}
