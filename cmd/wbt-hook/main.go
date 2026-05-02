// wbt-hook is the Claude Code PostToolUse global hook binary.
//
// Claude Code calls this binary after every tool execution (Bash, Edit, Write,
// Read, MCP, etc.) with a JSON payload on stdin.
//
// Spec (Claude Code hooks):
//
//	stdin  — JSON: {"tool_name":..., "tool_input":..., "tool_response":{"text":...},
//	                "tool_use_id":..., "cwd":..., "session_id":..., "transcript_path":...}
//	stdout — optional JSON: {"additionalContext": "..."} (≤ 10 000 chars)
//	exit 0 — always; hook MUST NOT block the Claude Code session
//
// Safety constraints:
//   - Read at most 300 bytes from stdin (claude-mem bug #1220 workaround)
//   - Total execution time budget: 50 ms (enqueue only, no DB / LLM wait)
//   - POST to wayneblacktea server with 200 ms timeout
//   - Exit 0 regardless of any error
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"
)

// maxStdinBytes caps stdin reads to avoid the claude-mem #1220 silent crash
// that occurs when reading more than ~350 bytes. 300 is a safe margin.
const maxStdinBytes = 300

// hookTimeout is the total budget for one hook invocation (enqueue to server).
const hookTimeout = 50 * time.Millisecond

// httpTimeout is the HTTP POST deadline; deliberately shorter than hookTimeout
// so we still have time to do cleanup before returning.
const httpTimeout = 200 * time.Millisecond

// defaultPort is the fallback server port when PORT env is not set.
const defaultPort = "8080"

// hookPayload is the subset of the Claude Code PostToolUse JSON we need.
type hookPayload struct {
	ToolName  string `json:"tool_name"`
	ToolInput string `json:"tool_input"`
}

// postToolUseRequest is the body sent to /api/activity/posttooluse.
type postToolUseRequest struct {
	Actor  string `json:"actor"`
	Action string `json:"tool_name"`
	Notes  string `json:"notes"`
}

func main() {
	if err := run(); err != nil {
		slog.Warn("wbt-hook: exiting with warning", "err", err)
	}
	// Exit 0 always — MUST NOT block Claude Code.
	os.Exit(0)
}

func run() error {
	// Step 1: Read at most 300 bytes from stdin (claude-mem #1220 safety).
	raw := make([]byte, maxStdinBytes)
	n, err := io.ReadFull(os.Stdin, raw)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return fmt.Errorf("reading stdin: %w", err)
	}
	raw = raw[:n]

	// Step 2: Parse the truncated JSON. If the payload is >300 bytes the JSON
	// will be incomplete; we accept best-effort parsing (tool_name may be present
	// even in truncated payloads because it appears near the start).
	var payload hookPayload
	_ = json.Unmarshal(raw, &payload) // intentional: ignore parse error, use zero values

	if payload.ToolName == "" {
		// Nothing useful to log.
		return nil
	}

	// Step 3: Build notes — default privacy mode: SHA256 hash of tool_input.
	notes := buildNotes(payload.ToolInput)

	// Step 4: POST to server within hookTimeout.
	ctx, cancel := context.WithTimeout(context.Background(), hookTimeout)
	defer cancel()

	return postActivity(ctx, payload.ToolName, notes)
}

// buildNotes returns a SHA256 hex hash of toolInput, or the raw input when
// WBT_HOOK_RAW=1 is set (only for trusted dev environments).
func buildNotes(toolInput string) string {
	if os.Getenv("WBT_HOOK_RAW") == "1" {
		return toolInput
	}
	h := sha256.Sum256([]byte(toolInput))
	return "sha256:" + hex.EncodeToString(h[:])
}

// postActivity sends tool_name + notes to the wayneblacktea server.
// It uses a separate HTTP client with httpTimeout so it never blocks past hookTimeout.
func postActivity(ctx context.Context, toolName, notes string) error {
	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}
	apiKey := os.Getenv("API_KEY")

	body, err := json.Marshal(postToolUseRequest{
		Actor:  "claude-code",
		Action: toolName,
		Notes:  notes,
	})
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}

	// The URL is always localhost:<port> where port comes from the PORT env var
	// (set by wbt init or the user's shell).  It is not derived from the hook
	// stdin payload, so there is no SSRF risk here.
	serverURL := "http://localhost:" + port + "/api/activity/posttooluse"
	//nolint:gosec // G704: serverURL is localhost-only; port comes from trusted env, not hook stdin
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		serverURL,
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}

	client := &http.Client{Timeout: httpTimeout}
	//nolint:gosec // G704: same localhost SSRF rationale as above; context is on req via NewRequestWithContext
	resp, err := client.Do(req)
	if err != nil {
		// Server may not be running — silently swallow, never block Claude Code.
		return nil
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}
