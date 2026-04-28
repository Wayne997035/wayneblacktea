package watchdog_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/waynechen/wayneblacktea/internal/watchdog"
)

func TestWatchdog_RecordsSuccessfulCalls(t *testing.T) {
	w := watchdog.New(10)
	mw := w.Middleware()
	handler := mw(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})

	for i := 0; i < 3; i++ {
		_, err := handler(context.Background(), mcp.CallToolRequest{
			Params: mcp.CallToolParams{Name: "add_task"},
		})
		if err != nil {
			t.Fatalf("handler: %v", err)
		}
	}

	recent := w.Recent(0)
	if len(recent) != 3 {
		t.Fatalf("expected 3 recorded calls, got %d", len(recent))
	}
	for _, c := range recent {
		if c.Tool != "add_task" || !c.Success {
			t.Errorf("unexpected call: %+v", c)
		}
	}
}

func TestWatchdog_RecordsErrors(t *testing.T) {
	w := watchdog.New(10)
	mw := w.Middleware()
	handler := mw(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, errors.New("boom")
	})

	_, _ = handler(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "complete_task"},
	})

	recent := w.Recent(0)
	if len(recent) != 1 {
		t.Fatalf("expected 1 call, got %d", len(recent))
	}
	if recent[0].Success || recent[0].ErrText != "boom" {
		t.Errorf("expected error recorded, got %+v", recent[0])
	}
}

func TestWatchdog_RecordsToolResultErrors(t *testing.T) {
	w := watchdog.New(10)
	mw := w.Middleware()
	handler := mw(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultError("bad input"), nil
	})

	_, _ = handler(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "log_decision"},
	})

	recent := w.Recent(0)
	if len(recent) != 1 || recent[0].Success || recent[0].ErrText != "bad input" {
		t.Errorf("expected IsError result captured, got %+v", recent[0])
	}
}

func TestWatchdog_RingBufferEvicts(t *testing.T) {
	w := watchdog.New(3)
	mw := w.Middleware()
	handler := mw(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})

	for i := 0; i < 10; i++ {
		_, _ = handler(context.Background(), mcp.CallToolRequest{
			Params: mcp.CallToolParams{Name: "add_task"},
		})
	}

	if got := len(w.Recent(0)); got != 3 {
		t.Errorf("expected ring buffer capped at 3, got %d", got)
	}
}

func TestWatchdog_CountByTool(t *testing.T) {
	w := watchdog.New(10)
	mw := w.Middleware()
	ok := mw(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})

	tools := []string{"add_task", "add_task", "complete_task", "add_task"}
	for _, name := range tools {
		_, _ = ok(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Name: name}})
	}

	counts := w.CountByTool()
	if counts["add_task"] != 3 || counts["complete_task"] != 1 {
		t.Errorf("unexpected counts %v", counts)
	}
}

func TestWatchdog_LastSuccessful(t *testing.T) {
	w := watchdog.New(10)
	mw := w.Middleware()
	ok := mw(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})

	if !w.LastSuccessful("add_task").IsZero() {
		t.Error("expected zero time before any call")
	}

	_, _ = ok(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "add_task"}})
	time.Sleep(2 * time.Millisecond)

	if got := w.LastSuccessful("add_task"); got.IsZero() {
		t.Error("expected non-zero time after a successful call")
	}
}
