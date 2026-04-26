package discordbot

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const maxFetchBytes = 256 * 1024 // 256 KB

var fetchClient = &http.Client{Timeout: 20 * time.Second}

// FetchURL downloads a URL and extracts readable text content.
// Returns the page title and body text (whitespace-normalised).
func FetchURL(ctx context.Context, rawURL string) (title, text string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 wayneblacktea-bot/1.0")

	resp, err := fetchClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("fetch %s: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes))
	if err != nil {
		return "", "", fmt.Errorf("read body: %w", err)
	}

	title, text = extractText(string(body))
	if title == "" {
		title = rawURL
	}
	if len(text) > 8000 {
		text = text[:8000] + "…"
	}
	return title, text, nil
}

// extractText parses HTML and returns the page title and visible text.
func extractText(raw string) (title, text string) {
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return "", raw
	}

	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.ElementNode:
			skip := map[string]bool{
				"script": true, "style": true, "noscript": true,
				"nav": true, "footer": true, "header": true,
				"aside": true, "form": true, "button": true,
			}
			if skip[n.Data] {
				return
			}
			if n.Data == "title" && n.FirstChild != nil {
				title = strings.TrimSpace(n.FirstChild.Data)
			}
		case html.TextNode:
			t := strings.TrimSpace(n.Data)
			if len(t) > 20 {
				sb.WriteString(t)
				sb.WriteString(" ")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	// Collapse whitespace
	words := strings.Fields(sb.String())
	text = strings.Join(words, " ")
	return title, text
}
