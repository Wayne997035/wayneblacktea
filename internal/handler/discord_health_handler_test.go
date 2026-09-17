package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

// [F185-06] GET /api/health/discord — four states, all HTTP 200.
func TestGetDiscordHealth_Starting(t *testing.T) {
	h := NewDiscordHealthHandler()
	code, body := getDiscordHealth(t, h)
	if code != http.StatusOK {
		t.Fatalf("code = %d, want %d", code, http.StatusOK)
	}
	if body.Status != DiscordStatusStarting {
		t.Fatalf("status = %q, want %q", body.Status, DiscordStatusStarting)
	}
}

func TestGetDiscordHealth_OK(t *testing.T) {
	h := NewDiscordHealthHandler()
	h.Set(DiscordHealthState{Status: DiscordStatusOK})
	code, body := getDiscordHealth(t, h)
	if code != http.StatusOK {
		t.Fatalf("code = %d, want %d", code, http.StatusOK)
	}
	if body.Status != DiscordStatusOK {
		t.Fatalf("status = %q, want %q", body.Status, DiscordStatusOK)
	}
}

func TestGetDiscordHealth_Degraded(t *testing.T) {
	h := NewDiscordHealthHandler()
	h.Set(DiscordHealthState{Status: DiscordStatusDegraded, Detail: "open discord session: rate limited"})
	code, body := getDiscordHealth(t, h)
	// D2/D6: degraded is still HTTP 200 — this endpoint never fails
	// Railway's deploy healthcheck. Callers read body.Status, not the code.
	if code != http.StatusOK {
		t.Fatalf("code = %d, want %d", code, http.StatusOK)
	}
	if body.Status != DiscordStatusDegraded {
		t.Fatalf("status = %q, want %q", body.Status, DiscordStatusDegraded)
	}
	if body.Detail == "" {
		t.Fatal("detail = empty, want non-empty on degraded")
	}
}

func TestGetDiscordHealth_Unconfigured(t *testing.T) {
	h := NewDiscordHealthHandler()
	h.Set(DiscordHealthState{Status: DiscordStatusUnconfigured, Detail: "DISCORD_BOT_TOKEN not set"})
	code, body := getDiscordHealth(t, h)
	if code != http.StatusOK {
		t.Fatalf("code = %d, want %d", code, http.StatusOK)
	}
	if body.Status != DiscordStatusUnconfigured {
		t.Fatalf("status = %q, want %q", body.Status, DiscordStatusUnconfigured)
	}
}

func getDiscordHealth(t *testing.T, h *DiscordHealthHandler) (int, DiscordHealthState) {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/health/discord", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	if err := h.GetDiscordHealth(c); err != nil {
		t.Fatalf("GetDiscordHealth: %v", err)
	}
	var body DiscordHealthState
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	return rec.Code, body
}
