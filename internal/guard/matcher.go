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
func classifyFilePath(filePath, cwd, matcherName string) (RiskTier, string) {
	// Resolve to absolute path to handle ".." traversal.
	abs := filePath
	if !filepath.IsAbs(filePath) {
		abs = filepath.Join(cwd, filePath)
	}
	abs = filepath.Clean(abs)

	// Normalise cwd.
	cwdClean := filepath.Clean(cwd)

	// Check prefix: abs must be inside cwdClean.
	// Use os.PathSeparator to avoid false positives like /foo/barbaz when
	// cwd is /foo/bar.
	if !strings.HasPrefix(abs, cwdClean+string(filepath.Separator)) && abs != cwdClean {
		return T5, fmt.Sprintf("%s: file_path %q resolves outside repo root %q (path traversal risk)", matcherName, filePath, cwd)
	}

	return T1, matcherName + ": in-repo edit"
}
