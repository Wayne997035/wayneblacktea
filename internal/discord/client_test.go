package discord

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestClient constructs a Client that points at the given URL directly,
// bypassing the DISCORD_WEBHOOK_URL env lookup done by NewClient.
func newTestClient(webhookURL string) *Client {
	return &Client{
		webhookURL: webhookURL,
		http:       &http.Client{},
	}
}

// TestClient_Send_success verifies that Send returns no error when the server
// responds with 204 No Content (the canonical Discord webhook success code).
func TestClient_Send_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	if err := c.Send(context.Background(), "hello world"); err != nil {
		t.Fatalf("Send returned unexpected error: %v", err)
	}
}

// TestClient_Send_serverError verifies that Send returns an error whose message
// contains "status 500" when the server responds with 500.
func TestClient_Send_serverError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	err := c.Send(context.Background(), "trigger server error")
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error message should contain status code 500, got: %v", err)
	}
}

// TestClient_SendEmbed_success verifies that SendEmbed sends a JSON payload
// that contains the embed fields and returns no error on a 200 response.
func TestClient_SendEmbed_success(t *testing.T) {
	var receivedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		receivedBody, err = io.ReadAll(io.LimitReader(r.Body, 4096))
		if err != nil {
			http.Error(w, "read error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	const title = "Test Embed Title"
	const description = "Test embed description"
	const color = "0x00FF00"

	if err := c.SendEmbed(context.Background(), title, description, color); err != nil {
		t.Fatalf("SendEmbed returned unexpected error: %v", err)
	}

	// Parse the received payload and verify embed fields.
	var payload webhookPayload
	if err := json.Unmarshal(receivedBody, &payload); err != nil {
		t.Fatalf("failed to parse sent payload: %v — body: %q", err, receivedBody)
	}
	if len(payload.Embeds) != 1 {
		t.Fatalf("expected 1 embed in payload, got %d", len(payload.Embeds))
	}
	embed := payload.Embeds[0]
	if embed.Title != title {
		t.Errorf("embed.Title = %q, want %q", embed.Title, title)
	}
	if embed.Description != description {
		t.Errorf("embed.Description = %q, want %q", embed.Description, description)
	}
	// 0x00FF00 = 65280 decimal.
	const wantColor = 65280
	if embed.Color != wantColor {
		t.Errorf("embed.Color = %d, want %d", embed.Color, wantColor)
	}
}

// TestClient_SendEmbed_invalidColor verifies that SendEmbed defaults to color=0
// when given an unparseable hex color string and still returns no error on success.
func TestClient_SendEmbed_invalidColor(t *testing.T) {
	var receivedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		receivedBody, err = io.ReadAll(io.LimitReader(r.Body, 4096))
		if err != nil {
			http.Error(w, "read error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	if err := c.SendEmbed(context.Background(), "title", "desc", "not-a-color"); err != nil {
		t.Fatalf("SendEmbed returned unexpected error: %v", err)
	}

	var payload webhookPayload
	if err := json.Unmarshal(receivedBody, &payload); err != nil {
		t.Fatalf("failed to parse sent payload: %v", err)
	}
	if len(payload.Embeds) != 1 {
		t.Fatalf("expected 1 embed, got %d", len(payload.Embeds))
	}
	if payload.Embeds[0].Color != 0 {
		t.Errorf("expected color=0 for invalid hex, got %d", payload.Embeds[0].Color)
	}
}

// TestClient_Send_non2xx verifies that any non-2xx status (e.g. 400) is treated
// as an error.
func TestClient_Send_non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	err := c.Send(context.Background(), "bad request trigger")
	if err == nil {
		t.Fatal("expected error for 400 response, got nil")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error message should contain 400, got: %v", err)
	}
}

// TestClient_Send_contentTypeJSON verifies that the outgoing POST request
// carries Content-Type: application/json (required by Discord's API).
func TestClient_Send_contentTypeJSON(t *testing.T) {
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	if err := c.Send(context.Background(), "check headers"); err != nil {
		t.Fatalf("Send returned unexpected error: %v", err)
	}
	if !strings.HasPrefix(gotContentType, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
}

// TestNewClient_MissingEnv verifies that NewClient returns nil when
// DISCORD_WEBHOOK_URL is not set (graceful degradation documented in the source).
func TestNewClient_MissingEnv(t *testing.T) {
	// This test relies on DISCORD_WEBHOOK_URL being unset in the test
	// environment (standard CI / local dev without a Discord webhook).
	// If the env is set the test is inconclusive — skip it.
	t.Setenv("DISCORD_WEBHOOK_URL", "")

	c := NewClient()
	if c != nil {
		t.Errorf("NewClient with empty env should return nil, got %+v", c)
	}
}

// TestNewClient_WithEnv verifies that NewClient returns a non-nil Client
// when DISCORD_WEBHOOK_URL is set.
func TestNewClient_WithEnv(t *testing.T) {
	t.Setenv("DISCORD_WEBHOOK_URL", "https://discord.com/api/webhooks/fake/url")

	c := NewClient()
	if c == nil {
		t.Error("NewClient should return non-nil Client when env is set")
	}
}
