// Package watchdog records recent MCP tool invocations in process memory and
// surfaces lightweight system-health signals (stuck tasks, pending review
// queue depth, last N tool calls). It is a partial answer to the "Claude
// forgets to call complete_task / log_decision" failure mode: tools cannot
// force discipline, but exposing the gap makes the omission visible.
//
// Design notes:
//   - Pure in-memory ring buffer; restarting the MCP server clears history.
//     This is intentional — historical analysis lives in the persistent
//     activity_log table, not here.
//   - Middleware wraps every mcp-go tool handler so registration order
//     does not matter.
//   - Reading is lock-shared; recording is lock-exclusive but bounded
//     (no I/O while holding the lock).
package watchdog

import (
	"context"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ToolCall is a single recorded MCP tool invocation.
type ToolCall struct {
	Tool      string    `json:"tool"`
	At        time.Time `json:"at"`
	DurMillis int64     `json:"dur_ms"`
	Success   bool      `json:"success"`
	ErrText   string    `json:"err,omitempty"`
}

// Watchdog records the last N tool calls. Safe for concurrent use.
type Watchdog struct {
	mu       sync.RWMutex
	calls    []ToolCall
	maxCalls int
}

// New returns a Watchdog with the given ring-buffer capacity. capacity <= 0
// defaults to 200.
func New(capacity int) *Watchdog {
	if capacity <= 0 {
		capacity = 200
	}
	return &Watchdog{maxCalls: capacity}
}

// Middleware returns an mcp-go ToolHandlerMiddleware that records every
// invocation. Wire it via server.NewMCPServer(... server.WithToolHandlerMiddleware(w.Middleware()) ...).
func (w *Watchdog) Middleware() server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			start := time.Now()
			res, err := next(ctx, req)
			w.record(req.Params.Name, start, res, err)
			return res, err //nolint:wrapcheck // middleware passes through caller's error verbatim
		}
	}
}

func (w *Watchdog) record(toolName string, start time.Time, res *mcp.CallToolResult, err error) {
	call := ToolCall{
		Tool:      toolName,
		At:        start,
		DurMillis: time.Since(start).Milliseconds(),
		Success:   err == nil && (res == nil || !res.IsError),
	}
	if err != nil {
		call.ErrText = err.Error()
	} else if res != nil && res.IsError {
		call.ErrText = errTextFromResult(res)
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.calls) >= w.maxCalls {
		w.calls = w.calls[1:]
	}
	w.calls = append(w.calls, call)
}

// errTextFromResult walks the Content slice and returns the first text
// payload, since MCP errors come back as TextContent with IsError=true.
func errTextFromResult(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if t, ok := c.(mcp.TextContent); ok {
			return t.Text
		}
	}
	return "tool returned IsError without text content"
}

// Recent returns the last n recorded calls, newest last (chronological order).
// n <= 0 or larger than the buffer returns everything currently held.
func (w *Watchdog) Recent(n int) []ToolCall {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if n <= 0 || n > len(w.calls) {
		n = len(w.calls)
	}
	out := make([]ToolCall, n)
	copy(out, w.calls[len(w.calls)-n:])
	return out
}

// CountByTool returns a per-tool invocation count for the entire current
// buffer. Useful for spotting "10 add_task without a single complete_task"
// patterns at a glance.
func (w *Watchdog) CountByTool() map[string]int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := make(map[string]int, len(w.calls))
	for _, c := range w.calls {
		out[c.Tool]++
	}
	return out
}

// LastSuccessful returns the timestamp of the most recent successful call to
// the given tool name, or the zero time if not present in the buffer.
func (w *Watchdog) LastSuccessful(toolName string) time.Time {
	w.mu.RLock()
	defer w.mu.RUnlock()
	for i := len(w.calls) - 1; i >= 0; i-- {
		c := w.calls[i]
		if c.Tool == toolName && c.Success {
			return c.At
		}
	}
	return time.Time{}
}
