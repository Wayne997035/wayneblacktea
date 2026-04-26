package knowledge

import "errors"

// KnowledgeType defines the allowed types for a knowledge item.
type KnowledgeType string

const (
	TypeArticle      KnowledgeType = "article"
	TypeTIL          KnowledgeType = "til"
	TypeBookmark     KnowledgeType = "bookmark"
	TypeZettelkasten KnowledgeType = "zettelkasten"
)

// ErrNotFound is returned when a requested knowledge item does not exist.
var ErrNotFound = errors.New("knowledge: not found")

// AddItemParams holds parameters for adding a new knowledge item.
type AddItemParams struct {
	Type    string
	Title   string
	Content string
	URL     string
	Tags    []string
}
