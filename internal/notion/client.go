package notion

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const (
	notionAPIBase    = "https://api.notion.com/v1"
	notionAPIVersion = "2022-06-28"
	// notionResponseLimit caps every Notion response body read at 256 KB.
	// Database query responses with daily-briefing payloads stay well under
	// 64 KB, but list endpoints can grow once history accumulates so we
	// pick a comfortable headroom.
	notionResponseLimit = 1 << 18
)

// Client creates pages in a Notion database via the Notion API.
type Client struct {
	token   string
	dbID    string
	baseURL string
	http    *http.Client
}

// NewClient returns a Client configured from NOTION_INTEGRATION_SECRET and
// NOTION_DATABASE_ID env vars. Returns nil if NOTION_INTEGRATION_SECRET is
// not set (graceful degradation).
//
// The env var name matches Notion's public terminology ("Integration secret",
// shown verbatim in https://www.notion.so/my-integrations) and is the same
// name the project's .env.example, Railway, and docs/installation.md use.
func NewClient() *Client {
	token := os.Getenv("NOTION_INTEGRATION_SECRET")
	if token == "" {
		return nil
	}
	return &Client{
		token:   token,
		dbID:    os.Getenv("NOTION_DATABASE_ID"),
		baseURL: notionAPIBase,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

// DatabaseID returns the configured Notion database ID. Used by helpers in
// the notion package that need to scope queries to the same database.
func (c *Client) DatabaseID() string { return c.dbID }

type notionParent struct {
	DatabaseID string `json:"database_id"`
}

type notionTitleText struct {
	Content string `json:"content"`
}

type notionTextItem struct {
	Text notionTitleText `json:"text"`
}

type notionTitleProp struct {
	Title []notionTextItem `json:"title"`
}

type notionSelectProp struct {
	Select notionSelectValue `json:"select"`
}

type notionSelectValue struct {
	Name string `json:"name"`
}

type notionRichTextProp struct {
	RichText []notionTextItem `json:"rich_text"`
}

type notionProperties struct {
	Name    notionTitleProp    `json:"Name"`
	Type    notionSelectProp   `json:"Type"`
	Content notionRichTextProp `json:"Content"`
}

type notionCreatePageRequest struct {
	Parent     notionParent     `json:"parent"`
	Properties notionProperties `json:"properties"`
}

type notionCreatePageResponse struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// CreatePage creates a new page in the configured Notion database.
// Returns the URL of the created page.
func (c *Client) CreatePage(ctx context.Context, title, content, itemType string) (string, error) {
	content = truncateForNotion(content)

	payload := notionCreatePageRequest{
		Parent: notionParent{DatabaseID: c.dbID},
		Properties: notionProperties{
			Name: notionTitleProp{
				Title: []notionTextItem{{Text: notionTitleText{Content: title}}},
			},
			Type: notionSelectProp{
				Select: notionSelectValue{Name: itemType},
			},
			Content: notionRichTextProp{
				RichText: []notionTextItem{{Text: notionTitleText{Content: content}}},
			},
		},
	}

	var result notionCreatePageResponse
	if err := c.do(ctx, http.MethodPost, "/pages", payload, &result); err != nil {
		return "", err
	}
	return result.URL, nil
}

// do issues a JSON request to path (relative to baseURL), enforces the
// response-size cap, and decodes the response into out when non-nil. It is
// shared by every Notion call in this package so authorization, version
// header, and body-limit handling stay consistent.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshaling notion %s %s: %w", method, path, err)
		}
		reader = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("creating notion %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Notion-Version", notionAPIVersion)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("calling notion %s %s: %w", method, path, err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, notionResponseLimit))
	if err != nil {
		return fmt.Errorf("reading notion %s %s response: %w", method, path, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody := string(respBytes)
		if len(errBody) > 200 {
			errBody = errBody[:200] + "...[truncated]"
		}
		return fmt.Errorf("notion %s %s returned %d: %s", method, path, resp.StatusCode, errBody)
	}

	if out == nil {
		return nil
	}
	if err := json.Unmarshal(respBytes, out); err != nil {
		return fmt.Errorf("parsing notion %s %s response: %w", method, path, err)
	}
	return nil
}
