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
)

// Client creates pages in a Notion database via the Notion API.
type Client struct {
	token string
	dbID  string
	http  *http.Client
}

// NewClient returns a Client configured from NOTION_API_KEY and NOTION_DATABASE_ID env vars.
// Returns nil if NOTION_API_KEY is not set (graceful degradation).
func NewClient() *Client {
	token := os.Getenv("NOTION_API_KEY")
	if token == "" {
		return nil
	}
	return &Client{
		token: token,
		dbID:  os.Getenv("NOTION_DATABASE_ID"),
		http:  &http.Client{Timeout: 15 * time.Second},
	}
}

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
	// Truncate content to 2000 chars — Notion rich_text limit is 2000.
	if len(content) > 2000 {
		content = content[:2000]
	}

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

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshaling notion page request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, notionAPIBase+"/pages", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating notion request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Notion-Version", notionAPIVersion)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("calling notion API: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16)) // 64 KB limit
	if err != nil {
		return "", fmt.Errorf("reading notion response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("notion API returned %d: %s", resp.StatusCode, string(respBytes))
	}

	var result notionCreatePageResponse
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return "", fmt.Errorf("parsing notion response: %w", err)
	}

	return result.URL, nil
}
