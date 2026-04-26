package discord

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

// Client sends messages to a Discord webhook.
type Client struct {
	webhookURL string
	http       *http.Client
}

// NewClient returns a Client configured from DISCORD_WEBHOOK_URL env var.
// Returns nil if the env var is not set (graceful degradation).
func NewClient() *Client {
	url := os.Getenv("DISCORD_WEBHOOK_URL")
	if url == "" {
		return nil
	}
	return &Client{
		webhookURL: url,
		http:       &http.Client{Timeout: 10 * time.Second},
	}
}

type webhookPayload struct {
	Content string         `json:"content,omitempty"`
	Embeds  []webhookEmbed `json:"embeds,omitempty"`
}

type webhookEmbed struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Color       int    `json:"color,omitempty"`
}

// Send posts a plain text message to the Discord webhook.
func (c *Client) Send(ctx context.Context, message string) error {
	payload := webhookPayload{Content: message}
	return c.post(ctx, payload)
}

// SendEmbed posts an embed message to the Discord webhook.
// color is a hex string like "0x00FF00"; invalid values default to 0.
func (c *Client) SendEmbed(ctx context.Context, title, description, color string) error {
	var colorInt int
	if _, err := fmt.Sscanf(color, "0x%x", &colorInt); err != nil {
		colorInt = 0
	}
	payload := webhookPayload{
		Embeds: []webhookEmbed{
			{Title: title, Description: description, Color: colorInt},
		},
	}
	return c.post(ctx, payload)
}

func (c *Client) post(ctx context.Context, payload webhookPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling discord payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating discord request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("sending discord webhook: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, 4096)); err != nil {
		return fmt.Errorf("draining discord response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("discord webhook returned status %d", resp.StatusCode)
	}
	return nil
}
