package discordbot

import (
	"strings"
	"testing"
)

func TestExtractText_GitHubRepo(t *testing.T) {
	// Minimal GitHub-like HTML with nav noise + article content
	html := `<!DOCTYPE html><html>
<head><title>GitHub - owner/repo: A great tool</title></head>
<body>
<nav>Navigation noise open more actions menu repository files navigation</nav>
<header>You signed in with another tab or window. to refresh your session.</header>
<article>
  <h1>repo</h1>
  <p>This is a fantastic tool for building things with artificial intelligence and automation pipelines.</p>
  <p>It supports multiple backends and provides a clean API for integration with external systems.</p>
</article>
<footer>Footer noise you signed out with another tab or window.</footer>
</body></html>`

	title, text := extractText(html)

	if title == "" {
		t.Error("expected non-empty title")
	}
	if strings.Contains(text, "Navigation noise") {
		t.Error("nav content should be excluded")
	}
	if strings.Contains(text, "you signed") {
		t.Error("session noise should be excluded")
	}
	if strings.Contains(text, "Footer noise") {
		t.Error("footer content should be excluded")
	}
	if !strings.Contains(text, "fantastic tool") {
		t.Errorf("article content missing, got: %q", text)
	}
}

func TestExtractText_FallbackWhenNoArticle(t *testing.T) {
	html := `<!DOCTYPE html><html>
<head><title>Simple Page</title></head>
<body>
<p>This page has no article or main element but contains meaningful content about software engineering.</p>
</body></html>`

	_, text := extractText(html)
	if !strings.Contains(text, "meaningful content") {
		t.Errorf("should fall back to full body, got: %q", text)
	}
}

func TestExtractText_SessionNoiseFiltered(t *testing.T) {
	cases := []string{
		"You signed in with another tab or window.",
		"There was an error while loading. Please reload this page.",
		"Open more actions menu repository files navigation",
		"You must be signed in to change notification settings",
	}
	for _, noise := range cases {
		if !sessionNoise.MatchString(noise) {
			t.Errorf("expected noise regex to match: %q", noise)
		}
	}
}
