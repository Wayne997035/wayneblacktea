package discordbot

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

var sessionNoise = regexp.MustCompile(`(?i)(you signed (in|out)|switched accounts|to refresh your session|there was an error while loading|please reload this page|open more actions menu|repository files navigation|change notification settings)`)

const maxFetchBytes = 256 * 1024

var fetchClient = &http.Client{Timeout: 20 * time.Second}

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

func extractText(raw string) (title, text string) {
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return "", raw
	}

	// Extract <title>
	var findTitle func(*html.Node)
	findTitle = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "title" && n.FirstChild != nil {
			title = strings.TrimSpace(n.FirstChild.Data)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findTitle(c)
		}
	}
	findTitle(doc)

	// Prefer <article> or <main> if present — avoids nav/header noise.
	root := findContentRoot(doc)

	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			skip := map[string]bool{
				"script": true, "style": true, "noscript": true,
				"nav": true, "footer": true, "header": true,
				"aside": true, "form": true, "button": true,
			}
			if skip[n.Data] {
				return
			}
		}
		if n.Type == html.TextNode {
			t := strings.TrimSpace(n.Data)
			if len(t) > 20 && !sessionNoise.MatchString(t) {
				sb.WriteString(t)
				sb.WriteString(" ")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)

	text = strings.Join(strings.Fields(sb.String()), " ")
	return title, text
}

// findContentRoot returns the first <article> or <main> node, falling back to the document root.
func findContentRoot(doc *html.Node) *html.Node {
	var found *html.Node
	var search func(*html.Node)
	search = func(n *html.Node) {
		if found != nil {
			return
		}
		if n.Type == html.ElementNode && (n.Data == "article" || n.Data == "main") {
			found = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			search(c)
		}
	}
	search(doc)
	if found != nil {
		return found
	}
	return doc
}
