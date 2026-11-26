package mcp

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// [F1126-03] Three-way required-argument diagnosis.
//
// A bare "title is required" cannot tell an LLM caller whether it forgot the
// field, sent it empty, or whether the transport dropped every parameter on
// the way (the log_decision incident: payload arrived as {} and the agent
// concluded its payload was too large). The report below names the keys that
// arrived, the required keys that are absent, and the required keys that
// arrived with a blank value. Only key NAMES are ever read into the message,
// never values: a blank test looks at the value but does not copy it.

// requiredArgReport is the three-way classification of one call's arguments.
type requiredArgReport struct {
	// Received is every key present in args, sorted.
	Received []string
	// Missing is the subset of the required keys absent from args, in the
	// order the required list gave them.
	Missing []string
	// Empty is the subset of the required keys present but blank.
	Empty []string
}

// blankFn decides whether the value of a PRESENT required key counts as blank.
type blankFn func(key string, v any) bool

// isBlankStringValue is the default blank test for raw handlers: nil, a
// non-string value, or a string that is empty after strings.TrimSpace. A
// non-string value never satisfies a handler that reads the key through
// stringArg, so it counts as blank rather than being waved through.
func isBlankStringValue(_ string, v any) bool {
	if v == nil {
		return true
	}
	s, ok := v.(string)
	if !ok {
		return true
	}
	return strings.TrimSpace(s) == ""
}

// classifyRequiredArgs classifies args against required with the default
// string blank test (see isBlankStringValue).
func classifyRequiredArgs(args map[string]any, required []string) requiredArgReport {
	return classifyRequiredWith(args, required, isBlankStringValue)
}

// classifyRequiredWith is classifyRequiredArgs with a caller-supplied blank
// test, used by the seam where number / boolean / UUID arguments need their
// own notion of "provided".
func classifyRequiredWith(args map[string]any, required []string, blank blankFn) requiredArgReport {
	rep := requiredArgReport{Received: make([]string, 0, len(args))}
	for k := range args {
		rep.Received = append(rep.Received, k)
	}
	sort.Strings(rep.Received)
	for _, k := range required {
		v, present := args[k]
		switch {
		case !present:
			rep.Missing = append(rep.Missing, k)
		case blank(k, v):
			rep.Empty = append(rep.Empty, k)
		}
	}
	return rep
}

// formatKeyList renders keys as `"a", "b"` (names only, sanitized and
// length-capped like the unknown-argument rejection), `none` when empty. At
// most maxListed keys are shown; the rest collapse to `, +N more`. maxListed
// <= 0 means no cap.
func formatKeyList(keys []string, maxListed int) string {
	if len(keys) == 0 {
		return "none"
	}
	shown := keys
	extra := 0
	if maxListed > 0 && len(keys) > maxListed {
		shown = keys[:maxListed]
		extra = len(keys) - maxListed
	}
	parts := make([]string, 0, len(shown))
	for _, k := range shown {
		parts = append(parts, fmt.Sprintf("%q", sanitizeAuditText(k, unknownArgKeyMaxRunes)))
	}
	out := strings.Join(parts, ", ")
	if extra > 0 {
		out += fmt.Sprintf(", +%d more", extra)
	}
	return out
}

// requiredArgsErrorWith builds the tool error for a failed required check
// using the given blank test. base is the legacy message and stays the prefix.
func requiredArgsErrorWith(base string, args map[string]any, required []string, blank blankFn) *mcp.CallToolResult {
	rep := classifyRequiredWith(args, required, blank)
	return mcp.NewToolResultError(fmt.Sprintf(
		"%s [received keys: %s; missing: %s; empty: %s]",
		base,
		formatKeyList(rep.Received, unknownArgMaxListed),
		formatKeyList(rep.Missing, 0),
		formatKeyList(rep.Empty, 0),
	))
}

// requiredArgsError is the three-way error with the default blank test.
func requiredArgsError(base string, args map[string]any, required []string) *mcp.CallToolResult {
	return requiredArgsErrorWith(base, args, required, isBlankStringValue)
}

// requireArgs is the raw-handler entry: nil when every key in required is
// present and non-blank, otherwise requiredArgsError(base, ...).
func requireArgs(args map[string]any, base string, required ...string) *mcp.CallToolResult {
	rep := classifyRequiredArgs(args, required)
	if len(rep.Missing) == 0 && len(rep.Empty) == 0 {
		return nil
	}
	return requiredArgsError(base, args, required)
}
