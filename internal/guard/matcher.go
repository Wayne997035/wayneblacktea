package guard

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// MatchResult holds the output of a matcher for one tool invocation.
type MatchResult struct {
	// Tier is the assessed risk tier.
	Tier RiskTier
	// Reason is a human-readable explanation of the tier.
	Reason string
	// MatcherName identifies which matcher produced the result.
	MatcherName string
}

// Match classifies a PreToolUse invocation and returns the result.
// cwd is the absolute working directory at time of invocation (from the
// Claude Code hook payload).
func Match(toolName string, toolInput json.RawMessage, cwd string) MatchResult {
	switch toolName {
	case "Bash":
		return matchBash(toolInput)
	case "Edit":
		return matchFileOp(toolInput, cwd, "Edit")
	case "Write":
		return matchFileOp(toolInput, cwd, "Write")
	case "MultiEdit":
		return matchMultiEdit(toolInput, cwd)
	case "Task":
		return matchTask(toolInput)
	default:
		// Unknown tool — treat as T4 (could do anything, but no concrete evidence
		// of destructive action).
		return MatchResult{
			Tier:        T4,
			Reason:      "unknown tool: " + toolName,
			MatcherName: "unknown",
		}
	}
}

// ---- Bash matcher ----

type bashInput struct {
	Command string `json:"command"`
}

func matchBash(raw json.RawMessage) MatchResult {
	var inp bashInput
	if err := json.Unmarshal(raw, &inp); err != nil {
		return MatchResult{T7, "bash: malformed input: " + err.Error(), "bash"}
	}
	tier, reason := ClassifyBash(inp.Command)
	return MatchResult{tier, reason, "bash"}
}

// ---- Edit / Write matcher ----

type editInput struct {
	FilePath string `json:"file_path"`
}

type writeInput struct {
	FilePath string `json:"file_path"`
}

func matchFileOp(raw json.RawMessage, cwd, matcherName string) MatchResult {
	var fp string
	switch matcherName {
	case "Edit":
		var inp editInput
		if err := json.Unmarshal(raw, &inp); err != nil {
			return MatchResult{T7, matcherName + ": malformed input: " + err.Error(), strings.ToLower(matcherName)}
		}
		fp = inp.FilePath
	default: // Write
		var inp writeInput
		if err := json.Unmarshal(raw, &inp); err != nil {
			return MatchResult{T7, matcherName + ": malformed input: " + err.Error(), strings.ToLower(matcherName)}
		}
		fp = inp.FilePath
	}

	if fp == "" {
		return MatchResult{T7, matcherName + ": empty file_path", strings.ToLower(matcherName)}
	}

	tier, reason := classifyFilePath(fp, cwd, matcherName)
	return MatchResult{tier, reason, strings.ToLower(matcherName)}
}

// ---- MultiEdit matcher ----

type multiEditInput struct {
	Edits []struct {
		FilePath string `json:"file_path"`
	} `json:"edits"`
}

func matchMultiEdit(raw json.RawMessage, cwd string) MatchResult {
	var inp multiEditInput
	if err := json.Unmarshal(raw, &inp); err != nil {
		return MatchResult{T7, "multiedit: malformed input: " + err.Error(), "multiedit"}
	}
	maxTier := T0
	maxReason := "multiedit: in-repo edit"

	for _, e := range inp.Edits {
		if e.FilePath == "" {
			continue
		}
		tier, reason := classifyFilePath(e.FilePath, cwd, "MultiEdit")
		if tier > maxTier {
			maxTier = tier
			maxReason = reason
		}
	}
	return MatchResult{maxTier, maxReason, "multiedit"}
}

// ---- Task (sub-agent) matcher ----

type taskInput struct {
	SubagentType string `json:"subagent_type"`
	Prompt       string `json:"prompt"`
}

func matchTask(raw json.RawMessage) MatchResult {
	var inp taskInput
	if err := json.Unmarshal(raw, &inp); err != nil {
		// Even if we can't parse the input, a Task invocation is risky.
		return MatchResult{T4, "task: malformed input (still flagged as sub-agent)", "task"}
	}
	reason := fmt.Sprintf("task sub-agent dispatch: type=%q", inp.SubagentType)
	if len(inp.Prompt) > 0 {
		// Append first 80 chars of prompt for context in logs.
		preview := inp.Prompt
		if len(preview) > 80 {
			preview = preview[:80] + "..."
		}
		reason += " prompt=" + preview
	}
	// T4 by default: sub-agents can execute arbitrary tools including T6+ ones.
	// This is intentionally conservative — the sub-agent's own tool invocations
	// will themselves trigger separate guard_events rows.
	return MatchResult{T4, reason, "task"}
}

// classifyFilePath resolves a file path and checks whether it is inside cwd.
// Paths outside the repo root are classified as T5 (path traversal risk).
//
// Defence-in-depth checks (in order):
//  1. Reject control characters (\x00, \r, \n) — these break path semantics
//     in different OS layers (Linux honours the null terminator; macOS may
//     not; auditing tools display only the prefix).
//  2. Reject leading "~" — filepath.Join does NOT shell-expand tilde, so
//     "~/secrets" would resolve relative to cwd as "<cwd>/~/secrets" and
//     create a literal "~" directory if written. The operator must pass
//     either an absolute path or a repo-relative path.
//  3. Resolve to absolute via filepath.Clean.
//  4. Resolve symlinks via filepath.EvalSymlinks: a symlink inside the repo
//     pointing OUT of the repo is a path-traversal vector that Clean alone
//     cannot detect. EvalSymlinks fails for non-existent paths (which is
//     the normal case for new file writes) — we fall back to the Clean'd
//     path because no symlink can be followed for a path that does not
//     yet exist.
//  5. Check prefix against cwd using HasPrefix(abs, cwd+sep) || abs==cwd
//     to avoid sibling-prefix matches like "/repohijack" against "/repo".
func classifyFilePath(filePath, cwd, matcherName string) (RiskTier, string) {
	// Step 1: control-character rejection.
	if strings.ContainsAny(filePath, "\x00\r\n") {
		return T7, fmt.Sprintf("%s: path contains control char (\\x00 / \\r / \\n)", matcherName)
	}

	// Step 2: tilde rejection. filepath.Join does not expand ~ — tilde is
	// a shell-only abstraction. An operator passing "~/foo" expects shell
	// semantics; without expansion we'd silently create a literal "~" dir.
	// Reject and demand the absolute form.
	if strings.HasPrefix(filePath, "~") {
		return T7, fmt.Sprintf("%s: tilde (~) is not shell-expanded; pass absolute or repo-relative path", matcherName)
	}

	// Step 3: resolve to absolute and Clean.
	abs := filePath
	if !filepath.IsAbs(filePath) {
		abs = filepath.Join(cwd, filePath)
	}
	abs = filepath.Clean(abs)

	// Step 4: follow symlinks. For a path that doesn't exist yet (typical
	// Write tool case), EvalSymlinks fails — but we still need symlink
	// resolution applied to the deepest existing PARENT so the prefix check
	// compares apples-to-apples with cwd. macOS, for example, symlinks
	// /var → /private/var, so a tempdir-based Write target needs the
	// /private/var prefix consistently on both sides of the comparison.
	abs = resolveExistingPrefix(abs)

	// Step 5: cwd is operator-controlled (from PreToolUse payload). EvalSymlinks
	// it too so a symlinked cwd doesn't false-positive against its real target.
	cwdClean := filepath.Clean(cwd)
	cwdClean = resolveExistingPrefix(cwdClean)

	// Step 6: prefix check. The "+sep" guard prevents sibling-prefix attacks
	// (e.g. "/repohijack" matching "/repo").
	if !strings.HasPrefix(abs, cwdClean+string(filepath.Separator)) && abs != cwdClean {
		return T5, fmt.Sprintf("%s: file_path %q resolves outside repo root %q (path traversal risk)", matcherName, filePath, cwd)
	}

	return T1, matcherName + ": in-repo edit"
}

// resolveExistingPrefix returns p with symlinks resolved on the deepest
// existing prefix, then re-joins the unresolved suffix. This handles the
// common Write-tool case where the target file does not yet exist (so
// EvalSymlinks(p) fails) but its parent directories do — and the parent
// chain may include OS symlinks (e.g. /var → /private/var on macOS) that
// must be resolved for the prefix check to be correct.
//
// Algorithm:
//  1. Start with p; if EvalSymlinks succeeds, return that.
//  2. Walk one path component up at a time until EvalSymlinks succeeds
//     or we hit the root. Re-join the unresolved tail to the resolved
//     prefix.
//  3. If nothing resolves (rare; pathological FS), return Clean(p).
func resolveExistingPrefix(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	// Walk up; cap iterations to len(path) to avoid pathological loops.
	cur := p
	suffixes := []string{}
	for i := 0; i < 4096; i++ {
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		suffixes = append([]string{filepath.Base(cur)}, suffixes...)
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			joined := resolved
			for _, s := range suffixes {
				joined = filepath.Join(joined, s)
			}
			return filepath.Clean(joined)
		}
		cur = parent
	}
	return filepath.Clean(p)
}
