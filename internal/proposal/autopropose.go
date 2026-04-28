package proposal

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/waynechen/wayneblacktea/internal/db"
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
	payload, err := json.Marshal(ConceptCandidate{
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
