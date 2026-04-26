package knowledge

// KnowledgeType defines the allowed types for a knowledge item.
type KnowledgeType string

const (
	TypeArticle      KnowledgeType = "article"
	TypeTIL          KnowledgeType = "til"
	TypeBookmark     KnowledgeType = "bookmark"
	TypeZettelkasten KnowledgeType = "zettelkasten"
)

// AddItemParams holds parameters for adding a new knowledge item.
type AddItemParams struct {
	Type          string
	Title         string
	Content       string
	URL           string
	Tags          []string
	Source        string // "manual", "discord", etc. — defaults to "manual"
	LearningValue int    // 1-5; 0 = unset
}
