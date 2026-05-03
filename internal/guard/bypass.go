package guard

import (
	"context"
	"path/filepath"
	"strings"
)

// ResolveBypass checks whether an active bypass exists for the given tool
// invocation context. Resolution order (narrowest wins): file > dir > repo > global.
//
// Parameters:
//   - cwd: absolute path to the repo root (from PreToolUse payload)
//   - filePath: the file being operated on (empty for non-file tools)
//   - toolName: the Claude Code tool name (e.g. "Bash", "Edit")
//
// Returns the first matching Bypass or nil if no bypass applies.
// Fail-open: any Store error returns nil bypass.
func ResolveBypass(ctx context.Context, store *Store, cwd, filePath, toolName string) *Bypass {
	if store == nil {
		return nil
	}

	repoName := filepath.Base(cwd)

	// Build candidates in narrowest-first order.
	type candidate struct {
		scope  string
		target string
	}

	var candidates []candidate

	// file-level
	if filePath != "" {
		abs := filePath
		if !filepath.IsAbs(filePath) {
			abs = filepath.Join(cwd, filePath)
		}
		abs = filepath.Clean(abs)
		candidates = append(candidates, candidate{"file", abs})
	}

	// dir-level: parent directory of filePath (or cwd itself)
	if filePath != "" {
		abs := filePath
		if !filepath.IsAbs(filePath) {
			abs = filepath.Join(cwd, filePath)
		}
		dir := filepath.Dir(filepath.Clean(abs))
		candidates = append(candidates, candidate{"dir", dir})
	} else {
		candidates = append(candidates, candidate{"dir", cwd})
	}

	// repo-level
	candidates = append(candidates, candidate{"repo", repoName})

	// global
	candidates = append(candidates, candidate{"global", "global"})

	for _, c := range candidates {
		b, _ := store.FindBypass(ctx, c.scope, c.target, toolName)
		if b != nil {
			return b
		}
	}
	return nil
}

// IsWhitespacesOnly returns true if s contains only whitespace characters.
func IsWhitespacesOnly(s string) bool {
	return strings.TrimSpace(s) == ""
}
