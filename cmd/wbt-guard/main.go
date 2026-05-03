// wbt-guard is the Claude Code PreToolUse global hook binary.
//
// Claude Code calls this binary before every tool execution (Bash, Edit, Write,
// MultiEdit, Task, etc.) with a JSON payload on stdin.
//
// Spec (Claude Code hooks):
//
//	stdin  — JSON: {"session_id":..., "tool_name":..., "tool_input":...,
//	                "cwd":..., "transcript_path":...}
//	stdout — nothing (PreToolUse observe-only mode)
//	exit 0 — ALWAYS; this hook MUST NOT block the Claude Code session
//
// Behaviour (P0a-β observe mode):
//  1. Read up to 2KB from stdin.
//  2. Parse the PreToolUse JSON envelope.
//  3. Check for .wayneblacktea/config.json marker in cwd. Absent → noop exit 0.
//  4. Classify the tool invocation using internal/guard matchers.
//  5. Check for an active bypass in guard_bypasses.
//  6. Write a guard_events row (fail-open: DB unavailable → log + exit 0).
//  7. Exit 0 unconditionally.
//
// Safety constraints:
//   - Cap stdin at 2KB (PreToolUse payloads can be larger than PostToolUse
//     because tool_input may include full file contents for Write/Edit).
//   - Total budget is unbounded in P0a-β (only DB write); should complete <200ms.
//   - Exit 0 regardless of any error (hook MUST NOT block Claude Code).
//   - Never write to stdout (PreToolUse hooks must not inject context).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/guard"
)

// maxStdinBytes caps stdin reads. PreToolUse payloads can include full file
// contents (Edit, Write), so 2KB is more generous than the 300B PostToolUse
// cap while still bounding memory usage. Anything beyond this is silently
// truncated; if the truncated prefix is not valid JSON the hook falls through
// to a noop, which is the desired fail-open behaviour.
const maxStdinBytes int64 = 2048

// hookTimeout is the total deadline for one wbt-guard invocation.
// DB writes are async-ish (single INSERT); 5s is generous.
const hookTimeout = 5 * time.Second

// preToolUsePayload is the Claude Code PreToolUse JSON envelope.
type preToolUsePayload struct {
	SessionID      string          `json:"session_id"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
	CWD            string          `json:"cwd"`
	TranscriptPath string          `json:"transcript_path"`
}

func main() {
	// Step 0: redirect slog away from stderr BEFORE the first slog call.
	// Claude Code surfaces stderr from PreToolUse hooks to the user terminal
	// as warnings — we don't want every "DB unreachable" to look like a
	// guard failure to the operator. Log to a private file in TempDir so
	// the operator can tail it for diagnostics without polluting the chat.
	configureSlog()

	if err := run(); err != nil {
		slog.Warn("wbt-guard: exiting with warning", "err", err)
	}
	// Exit 0 always — MUST NOT block Claude Code.
	os.Exit(0)
}

// configureSlog redirects the default slog handler to a Warn-level JSON
// file at <TempDir>/wbt-guard.log mode 0600. If the file cannot be opened
// (read-only fs, weird permissions), slog is wired to io.Discard so a
// failed log open never causes the hook to write to stderr and surface
// noise to the user.
func configureSlog() {
	logPath := filepath.Join(os.TempDir(), "wbt-guard.log")
	// path is os.TempDir() + constant filename; mode 0600 only for owner.
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // G304: see comment above
	if err != nil || f == nil {
		slog.SetDefault(slog.New(slog.NewJSONHandler(io.Discard, nil)))
		return
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(f, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	})))
}

func run() error {
	// Step 1: Read at most 2KB from stdin.
	raw, err := readStdin(maxStdinBytes)
	if err != nil {
		return fmt.Errorf("reading stdin: %w", err)
	}

	// Step 2: Parse the PreToolUse JSON envelope.
	var payload preToolUsePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		// Not valid JSON — noop, not a wbt-guard invocation.
		return fmt.Errorf("parsing stdin JSON: %w", err)
	}

	if payload.ToolName == "" {
		// No tool name — nothing to classify.
		return nil
	}

	// Step 3: Determine cwd. Prefer payload.CWD; fallback to os.Getwd.
	cwd := payload.CWD
	if cwd == "" {
		if wd, wdErr := os.Getwd(); wdErr == nil {
			cwd = wd
		}
	}

	// Step 4: Check for marker file in cwd.
	cfg, err := guard.LoadConfig(cwd)
	if err != nil {
		if errors.Is(err, guard.ErrMarkerAbsent) {
			// Guard not enabled for this repo — noop.
			return nil
		}
		// Malformed config — log and noop (fail-open).
		slog.Warn("wbt-guard: malformed config — noop", "err", err, "cwd", cwd)
		return nil
	}

	if !cfg.Observe {
		// observe=false → guard disabled.
		return nil
	}

	// Step 5: Classify.
	result := guard.Match(payload.ToolName, payload.ToolInput, cwd)

	// Step 6: Open DB (fail-open if unavailable).
	dbURL := guard.ResolveDBURL(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), hookTimeout)
	defer cancel()

	pool, _ := guard.OpenPool(ctx, dbURL)

	// Derive repo name from cwd directory name.
	repoName := filepath.Base(cwd)

	// Step 7: Check for active bypass.
	store := guard.NewStore(pool)
	filePathForBypass := extractFilePath(payload.ToolInput)
	bypass := guard.ResolveBypass(ctx, store, cwd, filePathForBypass, payload.ToolName)

	// Step 8: Determine would_deny.
	// In P0a-β this is purely observational — we record what *would* be denied
	// under a hypothetical enforce-mode policy (tier >= T5, no active bypass).
	wouldDeny := result.Tier >= guard.T5 && bypass == nil

	// Step 9: Write guard_events row (fail-open).
	ev := guard.Event{
		SessionID:  payload.SessionID,
		ToolName:   payload.ToolName,
		ToolInput:  payload.ToolInput,
		CWD:        cwd,
		RepoName:   repoName,
		RiskTier:   result.Tier,
		RiskReason: result.Reason,
		WouldDeny:  wouldDeny,
		Matcher:    result.MatcherName,
	}
	if bypass != nil {
		ev.BypassID = &bypass.ID
	}

	if err := store.WriteEvent(ctx, ev); err != nil {
		slog.Warn("wbt-guard: WriteEvent error (non-fatal)", "err", err)
	}

	return nil
}

// readStdin reads at most maxBytes from stdin and returns the data.
// It is not an error for stdin to have fewer than maxBytes; we cap the read
// with io.LimitReader to bound memory in case Claude Code passes a giant
// tool_input (Edit/Write payloads can include full file contents).
func readStdin(maxBytes int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(os.Stdin, maxBytes))
	if err != nil {
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	return data, nil
}

// extractFilePath attempts to pull file_path from the tool_input JSON.
// Returns empty string if not present or on parse error.
func extractFilePath(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	return m.FilePath
}
