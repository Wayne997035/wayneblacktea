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

	// ParentID is set by recursive fan-out calls when inserting section children
	// of a Markdown document. Nil for top-level (root) items.
	// Raw UUID bytes matching pgtype.UUID.Bytes / uuid.UUID layout.
	ParentID *[16]byte

	// HeadingPath is the breadcrumb path for a section child,
	// e.g. "Introduction › Key Concepts". Empty for root items.
	HeadingPath string

	// HeadingLevel is the ATX heading depth (1–6) for a section, 0 for root.
	HeadingLevel int

	// Cross-domain reference fields (migration 000049).
	// No FK constraint (CLAUDE.md red-line §9); referential integrity in code.
	ProjectID  *[16]byte // nil → NULL
	TaskID     *[16]byte // nil → NULL
	DecisionID *[16]byte // nil → NULL
}
