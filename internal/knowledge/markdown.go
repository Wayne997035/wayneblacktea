package knowledge

import (
	"bytes"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// headingSeparator is the Unicode separator used in heading_path values.
// E.g. "Introduction › Key Concepts › Embeddings"
const headingSeparator = " › "

// Section is one heading-delimited chunk of a Markdown document.
type Section struct {
	// Level is 1–6 (H1–H6).
	Level int
	// Title is the plain-text heading text.
	Title string
	// HeadingPath is the full ancestor chain, e.g. "H1 Title › H2 Title".
	// For a top-level H1 section it equals Title.
	HeadingPath string
	// Content is the section body text (text under the heading, not including
	// the heading itself or any sub-headings and their content).
	Content string
}

// ParseMarkdownSections returns the list of sections found in src.
// Returns nil (not an error) when src contains no ATX headings — the caller
// should fall back to single-row insertion.
//
//nolint:gocyclo // AST walk requires many node-type branches; extraction would obscure the logic
func ParseMarkdownSections(src string) []Section {
	if !bytes.ContainsRune([]byte(src), '#') {
		return nil
	}

	md := goldmark.New()
	reader := text.NewReader([]byte(src))
	doc := md.Parser().Parse(reader)

	// Walk the AST and collect heading positions + body text.
	type rawSection struct {
		level   int
		title   string
		bodyBuf strings.Builder
	}

	var sections []*rawSection
	var current *rawSection

	srcBytes := []byte(src)

	err := ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		switch node := n.(type) {
		case *ast.Heading:
			if entering {
				// Build plain-text title from the heading's inline children.
				var titleBuf strings.Builder
				for child := node.FirstChild(); child != nil; child = child.NextSibling() {
					if t, ok := child.(*ast.Text); ok {
						titleBuf.Write(t.Segment.Value(srcBytes))
					}
				}
				current = &rawSection{
					level: node.Level,
					title: strings.TrimSpace(titleBuf.String()),
				}
				sections = append(sections, current)
			}
		default:
			// Paragraph, list, blockquote, code block, etc. — body content.
			// The second pass (source-range splitting below) extracts body text
			// directly from srcBytes, so nothing needs to be collected here.
			_ = entering
		}
		return ast.WalkContinue, nil
	})
	if err != nil || len(sections) == 0 {
		return nil
	}

	// Second pass: extract body content by splitting the source on heading
	// boundary offsets. goldmark AST node positions are reliable byte offsets.
	// We collect heading byte offsets from the source text directly by scanning.
	type headingMark struct {
		byteOffset int
		section    *rawSection
	}
	var marks []headingMark

	// Walk again just for heading line positions.
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if h, ok := n.(*ast.Heading); ok && entering {
			if h.Lines().Len() > 0 {
				seg := h.Lines().At(0)
				idx := len(marks)
				if idx < len(sections) {
					marks = append(marks, headingMark{
						byteOffset: seg.Start,
						section:    sections[idx],
					})
				}
			}
		}
		return ast.WalkContinue, nil
	})

	// Build body content for each section: from end of heading line to start
	// of next heading line.
	for i, mark := range marks {
		// Find end of the heading line.
		headingLineEnd := mark.byteOffset
		for headingLineEnd < len(srcBytes) && srcBytes[headingLineEnd] != '\n' {
			headingLineEnd++
		}
		if headingLineEnd < len(srcBytes) {
			headingLineEnd++ // consume the newline
		}

		var bodyEnd int
		if i+1 < len(marks) {
			bodyEnd = marks[i+1].byteOffset
		} else {
			bodyEnd = len(srcBytes)
		}
		if bodyEnd > headingLineEnd {
			mark.section.bodyBuf.Write(srcBytes[headingLineEnd:bodyEnd])
		}
	}

	// Build heading paths using an ancestor-stack approach.
	// E.g. H1→H2→H3 produces "H1 Title › H2 Title › H3 Title".
	type stackEntry struct {
		level int
		title string
	}
	var stack []stackEntry

	result := make([]Section, 0, len(sections))
	for _, sec := range sections {
		// Pop stack entries that are at the same level or deeper.
		for len(stack) > 0 && stack[len(stack)-1].level >= sec.level {
			stack = stack[:len(stack)-1]
		}
		stack = append(stack, stackEntry{level: sec.level, title: sec.title})

		parts := make([]string, len(stack))
		for i, e := range stack {
			parts[i] = e.title
		}
		result = append(result, Section{
			Level:       sec.level,
			Title:       sec.title,
			HeadingPath: strings.Join(parts, headingSeparator),
			Content:     strings.TrimSpace(sec.bodyBuf.String()),
		})
	}
	return result
}
