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
	"path/filepath"
	"time"
)

// maxStdinBytes caps stdin reads to avoid the claude-mem #1220 silent crash
// that occurs when reading more than ~350 bytes. 300 is a safe margin.
const maxStdinBytes = 300

// hookTimeout is the total budget for one hook invocation (enqueue to server).
const hookTimeout = 50 * time.Millisecond

// httpTimeout is a fallback ceiling on the HTTP client in case the request
// ctx (hookTimeout = 50 ms) cancellation is removed in a future refactor.
// Effective ceiling today is hookTimeout because ctx fires first.
const httpTimeout = 200 * time.Millisecond

// rawNotesMaxLen caps the raw payload sent when WBT_HOOK_RAW=1. Even in raw
// dev mode we MUST NOT POST the full 16 MB stdin to the server — file
// contents written by the Edit tool can include credentials. 500 chars is
// enough for "what tool did with what file" without harvesting secrets.
// (security audit M-3)
const rawNotesMaxLen = 500

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

// Version metadata injected at link time via goreleaser ldflags.
var (
	version = "dev"
	commit  = "none"
)

func main() {
	// Handle `version` BEFORE initHookSlog so a plain `wbt-hook version`
	// invocation does not create an empty 0600 log file as a side effect.
	// Matches the wbt-guard / wbt-doctor pattern. (PR #85 R3 — reviewer minor)
	if len(os.Args) >= 2 && (os.Args[1] == "version" || os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Printf("%s %s (%s)\n", filepath.Base(os.Args[0]), version, commit)
		return
	}
	initHookSlog("wbt-hook")
	if err := run(); err != nil {
		slog.Warn("wbt-hook: exiting with warning", "err", err)
	}
	// Exit 0 always — MUST NOT block Claude Code.
	os.Exit(0)
}

// initHookSlog redirects slog to a 0600 file in os.TempDir so the hook never
// writes to stderr (Claude Code surfaces stderr as terminal warnings).
// Falls back to io.Discard if the log file cannot be opened.
func initHookSlog(name string) {
	logPath := filepath.Join(os.TempDir(), name+".log")
	//nolint:gosec // G304: logPath is os.TempDir() + constant suffix; not derived from user input
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		slog.SetDefault(slog.New(slog.NewJSONHandler(io.Discard, nil)))
		return
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(f, &slog.HandlerOptions{Level: slog.LevelWarn})))
}

func run() error {
	// Step 1: Read at most 300 bytes from stdin (claude-mem #1220 safety).
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, maxStdinBytes))
	if err != nil {
		return fmt.Errorf("reading stdin: %w", err)
	}

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

// buildNotes returns a SHA256 hex hash of toolInput, or a length-capped raw
// input when WBT_HOOK_RAW=1 is set (only for trusted dev environments).
//
// SECURITY: raw mode caps at rawNotesMaxLen (500 chars) so file contents
// written by Edit/Write/Bash that include secrets cannot be harvested
// wholesale. (security audit M-3)
func buildNotes(toolInput string) string {
	if os.Getenv("WBT_HOOK_RAW") == "1" {
		if len(toolInput) > rawNotesMaxLen {
			return toolInput[:rawNotesMaxLen] + "...[TRUNCATED]"
		}
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
	//nolint:gosec // G107: serverURL is localhost-only; port comes from trusted env, not hook stdin
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
	//nolint:gosec // G107: same localhost SSRF rationale as above; context is on req via NewRequestWithContext
	resp, err := client.Do(req)
	if err != nil {
		// Server may not be running — silently swallow, never block Claude Code.
		return nil
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}
