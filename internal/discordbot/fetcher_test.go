package discordbot

import (
	"context"
	"strings"
	"testing"
)

// TestExtractText_ArticlePreferred verifies that <article> content is preferred over nav/header/footer noise.
func TestExtractText_ArticlePreferred(t *testing.T) {
	raw := `<!DOCTYPE html><html>
<head><title>GitHub - owner/repo: A great tool</title></head>
<body>
<nav>Navigation noise open more actions menu repository files navigation</nav>
<header>You signed in with another tab or window. to refresh your session.</header>
<article>
  <h1>repo</h1>
  <p>This repository provides a semantic runtime for coordinating multiple AI agents with shared state.</p>
  <p>Agents read from and write to the same store, enabling structured collaboration across tasks.</p>
</article>
<footer>Footer content you signed out with another tab or window.</footer>
</body></html>`

	title, text := extractText(raw)

	if title == "" {
		t.Error("expected non-empty title")
	}
	if strings.Contains(text, "Navigation noise") {
		t.Error("nav content should be excluded")
	}
	if strings.Contains(strings.ToLower(text), "you signed") {
		t.Error("session noise should be excluded")
	}
	if strings.Contains(text, "Footer content") {
		t.Error("footer content should be excluded")
	}
	if !strings.Contains(text, "semantic runtime") {
		t.Errorf("article content missing, got: %q", text)
	}
}

// TestExtractText_FallbackWhenNoArticle verifies full-body fallback when no <article>/<main> exists.
func TestExtractText_FallbackWhenNoArticle(t *testing.T) {
	raw := `<!DOCTYPE html><html>
<head><title>Simple Page</title></head>
<body>
<p>This documentation page explains distributed systems concepts and consensus algorithms in detail.</p>
</body></html>`

	_, text := extractText(raw)
	if !strings.Contains(text, "consensus algorithms") {
		t.Errorf("should fall back to full body, got: %q", text)
	}
}

// TestExtractText_SessionNoiseRegex verifies all known noise patterns are matched.
func TestExtractText_SessionNoiseRegex(t *testing.T) {
	cases := []string{
		"You signed in with another tab or window.",
		"There was an error while loading. Please reload this page.",
		"Open more actions menu repository files navigation",
		"You must be signed in to change notification settings",
		"switched accounts on another tab or window",
	}
	for _, noise := range cases {
		if !sessionNoise.MatchString(noise) {
			t.Errorf("sessionNoise should match: %q", noise)
		}
	}
}

// TestFetchURL_RealGitHub is an integration test against a real GitHub URL.
// Skipped unless -run=Integration is passed explicitly.
func TestFetchURL_RealGitHub(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	title, text, err := FetchURL(ctx, "https://github.com/Koopa0/koopa")
	if err != nil {
		t.Fatalf("FetchURL failed: %v", err)
	}
	if title == "" {
		t.Error("expected non-empty title")
	}
	if len(text) < 500 {
		t.Errorf("expected at least 500 chars of content, got %d", len(text))
	}
	// Should contain README content, not GitHub UI noise
	if strings.Contains(strings.ToLower(text), "you signed in with another tab") {
		t.Error("session noise leaked into extracted content")
	}
	if strings.Contains(strings.ToLower(text), "open more actions menu") {
		t.Error("GitHub UI noise leaked into extracted content")
	}
	t.Logf("title: %s", title)
	t.Logf("content length: %d", len(text))
	t.Logf("content preview: %.200s", text)
}
