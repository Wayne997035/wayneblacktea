package knowledge

import (
	"strings"
	"testing"
)

// TestParseMarkdownSections_NoHeadings verifies that plain text (no headings)
// returns nil — the caller should fall back to single-row insertion.
func TestParseMarkdownSections_NoHeadings(t *testing.T) {
	input := "This is plain text with no headings.\n\nAnother paragraph."
	got := ParseMarkdownSections(input)
	if got != nil {
		t.Errorf("expected nil for no-headings input, got %+v", got)
	}
}

// TestParseMarkdownSections_Empty verifies that an empty string returns nil.
func TestParseMarkdownSections_Empty(t *testing.T) {
	got := ParseMarkdownSections("")
	if got != nil {
		t.Errorf("expected nil for empty input, got %+v", got)
	}
}

// TestParseMarkdownSections_SingleH1 verifies a single H1 heading is detected.
func TestParseMarkdownSections_SingleH1(t *testing.T) {
	input := "# Introduction\n\nThis is the intro body."
	sections := ParseMarkdownSections(input)
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d: %+v", len(sections), sections)
	}
	s := sections[0]
	if s.Level != 1 {
		t.Errorf("Level: got %d, want 1", s.Level)
	}
	if s.Title != "Introduction" {
		t.Errorf("Title: got %q, want %q", s.Title, "Introduction")
	}
	if s.HeadingPath != "Introduction" {
		t.Errorf("HeadingPath: got %q, want %q", s.HeadingPath, "Introduction")
	}
	if !strings.Contains(s.Content, "intro body") {
		t.Errorf("Content: expected 'intro body', got %q", s.Content)
	}
}

// TestParseMarkdownSections_H1H2 verifies that H2 under H1 builds correct heading path.
func TestParseMarkdownSections_H1H2(t *testing.T) {
	input := `# Architecture

Overview paragraph.

## Components

Component details here.

## Deployment

Deploy section.
`
	sections := ParseMarkdownSections(input)
	if len(sections) != 3 {
		t.Fatalf("expected 3 sections, got %d: %+v", len(sections), sections)
	}

	cases := []struct {
		idx         int
		level       int
		title       string
		headingPath string
	}{
		{0, 1, "Architecture", "Architecture"},
		{1, 2, "Components", "Architecture › Components"},
		{2, 2, "Deployment", "Architecture › Deployment"},
	}
	for _, tc := range cases {
		s := sections[tc.idx]
		if s.Level != tc.level {
			t.Errorf("[%d] Level: got %d, want %d", tc.idx, s.Level, tc.level)
		}
		if s.Title != tc.title {
			t.Errorf("[%d] Title: got %q, want %q", tc.idx, s.Title, tc.title)
		}
		if s.HeadingPath != tc.headingPath {
			t.Errorf("[%d] HeadingPath: got %q, want %q", tc.idx, s.HeadingPath, tc.headingPath)
		}
	}
}

// TestParseMarkdownSections_DeepNesting verifies 3-level nesting produces correct paths.
func TestParseMarkdownSections_DeepNesting(t *testing.T) {
	input := `# Top

## Middle

### Deep

Deep body.
`
	sections := ParseMarkdownSections(input)
	if len(sections) != 3 {
		t.Fatalf("expected 3 sections, got %d", len(sections))
	}

	// The deepest section should have the full path.
	deep := sections[2]
	if deep.HeadingPath != "Top › Middle › Deep" {
		t.Errorf("deep HeadingPath: got %q, want %q", deep.HeadingPath, "Top › Middle › Deep")
	}
	if deep.Level != 3 {
		t.Errorf("deep Level: got %d, want 3", deep.Level)
	}
}

// TestParseMarkdownSections_SiblingReset verifies that sibling H2s do not
// accumulate in the heading path — each sibling starts fresh.
func TestParseMarkdownSections_SiblingReset(t *testing.T) {
	input := `# Doc

## Alpha

Alpha body.

## Beta

Beta body.
`
	sections := ParseMarkdownSections(input)
	var beta *Section
	for i := range sections {
		if sections[i].Title == "Beta" {
			beta = &sections[i]
		}
	}
	if beta == nil {
		t.Fatal("Beta section not found")
	}
	if beta.HeadingPath != "Doc › Beta" {
		t.Errorf("Beta HeadingPath: got %q, want %q", beta.HeadingPath, "Doc › Beta")
	}
}

// TestParseMarkdownSections_BodyContentAssignment verifies each section contains
// only its own body text (not the heading line or sub-section bodies).
func TestParseMarkdownSections_BodyContentAssignment(t *testing.T) {
	input := `# Root

Root intro.

## Child

Child body only.
`
	sections := ParseMarkdownSections(input)
	if len(sections) < 2 {
		t.Fatalf("expected at least 2 sections, got %d", len(sections))
	}
	root := sections[0]
	child := sections[1]

	if strings.Contains(root.Content, "Child body") {
		t.Errorf("Root content should not include child body: %q", root.Content)
	}
	if strings.Contains(child.Content, "Root intro") {
		t.Errorf("Child content should not include root intro: %q", child.Content)
	}
	if !strings.Contains(child.Content, "Child body") {
		t.Errorf("Child content should contain 'Child body', got %q", child.Content)
	}
}

// TestHasHeadings verifies the cheap HasHeadings check.
func TestHasHeadings(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"# Heading\n\nBody", true},
		{"No heading here", false},
		{"", false},
		{"## Only H2\n\nStuff", true},
	}
	for _, tc := range cases {
		got := HasHeadings(tc.input)
		if got != tc.want {
			t.Errorf("HasHeadings(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}
