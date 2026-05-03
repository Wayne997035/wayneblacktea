package guard

import (
	"strings"
	"unicode"
)

// RiskTier classifies how risky a tool invocation is.
// Higher value = higher risk.  T7 is a catch-all for unrecognised commands.
type RiskTier int8

const (
	// T0 Read-only commands (ls, cat, git log …)
	T0 RiskTier = 0
	// T1 Build / test (go build, task check, npm test …)
	T1 RiskTier = 1
	// T2 Local run (go run, python …)
	T2 RiskTier = 2
	// T3 Safe git mutation (git add, git commit, git stash …)
	T3 RiskTier = 3
	// T4 Risky git mutation / sub-agent (git rebase, git merge, Task …)
	T4 RiskTier = 4
	// T5 Destructive local (rm, git reset --hard, git clean …)
	T5 RiskTier = 5
	// T6 Destructive remote (git push, git push --force, gh pr merge …)
	T6 RiskTier = 6
	// T7 Unknown / cannot parse
	T7 RiskTier = 7
)

// Named constants for repeated string values flagged by goconst.
const (
	reasonReadOnly    = "read-only command"
	reasonBuildTool   = "build tool"
	reasonGitReadOnly = "git read-only"
	statusStr         = "status"
	installStr        = "install"
	repoStr           = "repo"
	testStr           = "test"
	runStr            = "run"
)

// ClassifyBash classifies a Bash command string and returns the highest risk
// tier found across the entire pipeline (commands chained with &&, ||, ;, |).
// Malformed or unparseable input falls through to T7 only if no tier was
// resolved; partial information from parseable segments is still used.
func ClassifyBash(command string) (RiskTier, string) {
	if strings.TrimSpace(command) == "" {
		return T7, "empty command"
	}

	// Split on pipeline/chain operators so each simple command is classified
	// independently and the max tier is returned.
	segments := splitPipeline(command)
	maxTier := T0
	maxReason := reasonReadOnly

	for _, seg := range segments {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		tier, reason := classifySimple(seg)
		if tier > maxTier {
			maxTier = tier
			maxReason = reason
		}
	}
	return maxTier, maxReason
}

// splitPipeline splits a shell command string on &&, ||, ;, and | operators.
// It handles basic quoting (single/double quotes) to avoid splitting inside
// quoted arguments, but does not implement a full POSIX shell parser.
func splitPipeline(cmd string) []string {
	var segments []string
	var cur strings.Builder
	st := splitState{}

	runes := []rune(cmd)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if st.toggleQuote(r) {
			cur.WriteRune(r)
			continue
		}
		if !st.inQuotes() {
			if skip := operatorWidth(runes, i); skip > 0 {
				segments = append(segments, cur.String())
				cur.Reset()
				i += skip - 1 // outer loop advances 1 more
				continue
			}
		}
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		segments = append(segments, cur.String())
	}
	return segments
}

// splitState tracks the quoting state during pipeline parsing.
type splitState struct {
	inSingle, inDouble bool
}

// toggleQuote toggles single/double-quote state and reports whether the rune
// was consumed as a quote toggle (so the caller still emits it to output).
func (s *splitState) toggleQuote(r rune) bool {
	switch {
	case r == '\'' && !s.inDouble:
		s.inSingle = !s.inSingle
		return true
	case r == '"' && !s.inSingle:
		s.inDouble = !s.inDouble
		return true
	}
	return false
}

func (s *splitState) inQuotes() bool { return s.inSingle || s.inDouble }

// operatorWidth returns the number of runes that make up a pipeline operator
// starting at runes[i], or 0 if none. Operators recognised: &&, ||, |, ;.
func operatorWidth(runes []rune, i int) int {
	r := runes[i]
	if i+1 < len(runes) {
		next := runes[i+1]
		if r == '&' && next == '&' {
			return 2
		}
		if r == '|' && next == '|' {
			return 2
		}
	}
	if r == '|' || r == ';' {
		return 1
	}
	return 0
}

// classifySimple classifies a single simple command (no pipeline operators).
// It extracts the command name and flags, then looks up tier tables.
func classifySimple(cmd string) (RiskTier, string) {
	tokens := tokenize(cmd)
	if len(tokens) == 0 {
		return T7, "empty segment"
	}

	// Handle env-var prefixes like FOO=bar go test → the actual command is go.
	cmdName := firstNonEnvToken(tokens)
	if cmdName == "" {
		return T7, "no command after env assignments"
	}

	return dispatchCommand(cmdName, tokens)
}

// firstNonEnvToken returns the first token in tokens that is not an environment
// variable assignment (i.e. does not contain '=').
func firstNonEnvToken(tokens []string) string {
	for _, tok := range tokens {
		if !strings.ContainsRune(tok, '=') {
			return tok
		}
	}
	return ""
}

// dispatchCommand routes by command name to the appropriate tier function.
// Static commands resolve via the simpleTiers lookup table; commands whose
// classification depends on subcommand arguments dispatch to specialised
// classifiers (classifyGit, classifyGo, …) so cyclomatic complexity stays
// bounded.
func dispatchCommand(cmdName string, tokens []string) (RiskTier, string) {
	if entry, ok := simpleTiers[cmdName]; ok {
		return entry.tier, entry.reason
	}
	if fn, ok := dispatchers[cmdName]; ok {
		return fn(tokens, cmdName)
	}
	return T7, "unknown command: " + cmdName
}

// simpleTierEntry maps a command name with no subcommand-dependent behaviour
// to a fixed (tier, reason) pair.
type simpleTierEntry struct {
	tier   RiskTier
	reason string
}

// simpleTiers is the lookup table for commands that classify identically
// regardless of arguments (read-only viewers, simple destructive commands,
// remote-by-default tools, etc).
var simpleTiers = map[string]simpleTierEntry{
	// T0 read-only inspectors
	"ls": {T0, reasonReadOnly}, "ll": {T0, reasonReadOnly}, "la": {T0, reasonReadOnly},
	"cat": {T0, reasonReadOnly}, "less": {T0, reasonReadOnly}, "more": {T0, reasonReadOnly},
	"head": {T0, reasonReadOnly}, "tail": {T0, reasonReadOnly}, "pwd": {T0, reasonReadOnly},
	"echo": {T0, reasonReadOnly}, "printf": {T0, reasonReadOnly}, "date": {T0, reasonReadOnly},
	"whoami": {T0, reasonReadOnly}, "hostname": {T0, reasonReadOnly},
	"find": {T0, reasonReadOnly}, "grep": {T0, reasonReadOnly}, "awk": {T0, reasonReadOnly},
	"sed": {T0, reasonReadOnly}, "sort": {T0, reasonReadOnly}, "uniq": {T0, reasonReadOnly},
	"wc": {T0, reasonReadOnly}, "diff": {T0, reasonReadOnly}, "file": {T0, reasonReadOnly},
	"stat": {T0, reasonReadOnly}, "du": {T0, reasonReadOnly}, "df": {T0, reasonReadOnly},
	"ps": {T0, reasonReadOnly}, "env": {T0, reasonReadOnly}, "printenv": {T0, reasonReadOnly},
	"which": {T0, reasonReadOnly}, "type": {T0, reasonReadOnly}, "man": {T0, reasonReadOnly},
	"help": {T0, reasonReadOnly}, "true": {T0, reasonReadOnly}, "false": {T0, reasonReadOnly},
	testStr: {T0, reasonReadOnly}, "curl": {T0, reasonReadOnly}, "wget": {T0, reasonReadOnly},

	// T1 build tools (no subcommand discrimination)
	"gradle": {T1, reasonBuildTool}, "gradlew": {T1, reasonBuildTool}, "mvn": {T1, reasonBuildTool},
	"pytest": {T1, "test runner"}, "jest": {T1, "test runner"},
	"vitest": {T1, "test runner"}, "mocha": {T1, "test runner"},
	"golangci-lint": {T1, "linter"}, "staticcheck": {T1, "linter"}, "govulncheck": {T1, "linter"},

	// T2 local script execution
	"python": {T2, "local script execution"}, "python3": {T2, "local script execution"},
	"ruby": {T2, "local script execution"}, "node": {T2, "local script execution"},
	"deno": {T2, "local script execution"}, "java": {T2, "local script execution"},
	"perl": {T2, "local script execution"},

	// T5 destructive local
	"rm": {T5, "file deletion"}, "rmdir": {T5, "file deletion"},
	"mv":       {T5, "file move (potentially destructive)"},
	"chmod":    {T5, "permission change"},
	"chown":    {T5, "permission change"},
	"dd":       {T5, "raw disk/file operation"},
	"truncate": {T5, "raw disk/file operation"},

	// T6 remote / out-of-host operations
	"ssh": {T6, "remote operation"}, "scp": {T6, "remote operation"}, "rsync": {T6, "remote operation"},
}

// dispatchFn classifies a command whose tier depends on its subcommand/args.
type dispatchFn func(tokens []string, cmdName string) (RiskTier, string)

// dispatchers maps cmdName → specialised classifier. Lookup is O(1) and
// keeps dispatchCommand below the gocyclo budget.
var dispatchers = map[string]dispatchFn{
	"git":     func(tokens []string, _ string) (RiskTier, string) { return classifyGit(tokens) },
	"gh":      func(tokens []string, _ string) (RiskTier, string) { return classifyGH(tokens) },
	"go":      func(tokens []string, _ string) (RiskTier, string) { return classifyGo(tokens) },
	"task":    classifyBuildTool,
	"make":    classifyBuildTool,
	"npm":     func(tokens []string, _ string) (RiskTier, string) { return classifyNPM(tokens) },
	"yarn":    func(tokens []string, _ string) (RiskTier, string) { return classifyNPM(tokens) },
	"pnpm":    func(tokens []string, _ string) (RiskTier, string) { return classifyNPM(tokens) },
	"bun":     func(tokens []string, _ string) (RiskTier, string) { return classifyNPM(tokens) },
	"docker":  classifyDevOps,
	"kubectl": classifyDevOps,
	"helm":    classifyDevOps,
}

// classifyGit classifies git subcommands.
func classifyGit(tokens []string) (RiskTier, string) {
	if len(tokens) < 2 {
		return T7, "git: missing subcommand"
	}
	sub := tokens[1]

	if tier, reason, ok := classifyGitReadOnly(sub); ok {
		return tier, reason
	}
	if tier, reason, ok := classifyGitSafeMutation(tokens, sub); ok {
		return tier, reason
	}
	if tier, reason, ok := classifyGitRiskyMutation(tokens, sub); ok {
		return tier, reason
	}
	if tier, reason, ok := classifyGitDestructive(tokens, sub); ok {
		return tier, reason
	}
	return T7, "git: unknown subcommand: " + sub
}

func classifyGitReadOnly(sub string) (RiskTier, string, bool) {
	switch sub {
	case statusStr, "log", "diff", "show", "shortlog", "describe",
		"blame", "annotate", "bisect", "ls-files", "ls-tree",
		"rev-parse", "rev-list", "cat-file", "config", "remote",
		"branch", "tag", "fetch":
		return T0, reasonGitReadOnly, true
	}
	return 0, "", false
}

func classifyGitSafeMutation(tokens []string, sub string) (RiskTier, string, bool) {
	switch sub {
	case "add", "commit", "stash", "restore", "init", "clone", "submodule":
		return T3, "git safe mutation", true
	case "checkout":
		if hasFlag(tokens, "-b") || hasFlag(tokens, "-B") {
			return T3, "git checkout: new branch", true
		}
		return T4, "git checkout: may switch existing branch", true
	}
	return 0, "", false
}

func classifyGitRiskyMutation(tokens []string, sub string) (RiskTier, string, bool) {
	switch sub {
	case "rebase", "merge", "cherry-pick", "revert", "am", "apply", "format-patch":
		return T4, "git risky mutation: " + sub, true
	case "reset":
		if hasFlag(tokens, "--hard") {
			return T5, "git reset --hard: destructive", true
		}
		return T4, "git reset (soft/mixed)", true
	case "pull":
		return T4, "git pull", true
	}
	return 0, "", false
}

func classifyGitDestructive(tokens []string, sub string) (RiskTier, string, bool) {
	switch sub {
	case "clean":
		return T5, "git clean: destructive", true
	case "push":
		if hasFlag(tokens, "--force") || hasFlag(tokens, "-f") {
			return T6, "git push --force", true
		}
		return T6, "git push", true
	}
	return 0, "", false
}

// classifyGH classifies GitHub CLI subcommands.
func classifyGH(tokens []string) (RiskTier, string) {
	if len(tokens) < 2 {
		return T7, "gh: missing subcommand"
	}
	sub := tokens[1]

	// Read operations: always T0.
	if len(tokens) >= 3 {
		action := tokens[2]
		if action == "list" || action == "view" || action == statusStr {
			return T0, "gh read-only"
		}
		// PR-level mutations.
		if sub == "pr" && (action == "merge") {
			return T6, "gh pr merge: remote destructive"
		}
		if sub == "pr" && (action == "create" || action == "edit") {
			return T6, "gh pr create/edit: remote mutation"
		}
		if sub == repoStr && action == "delete" {
			return T6, "gh repo delete: remote destructive"
		}
	}

	switch sub {
	case "pr", "issue", repoStr, "release", runStr, "workflow",
		"api", "auth", "config", "gist", "secret", "variable":
		return T4, "gh: " + sub + " (mutation)"
	}
	return T7, "gh: unknown subcommand: " + sub
}

// classifyGo classifies go tool subcommands.
func classifyGo(tokens []string) (RiskTier, string) {
	if len(tokens) < 2 {
		return T1, "go tool"
	}
	sub := tokens[1]
	switch sub {
	case "build", installStr, "generate", "mod", "work":
		return T1, "go build/install"
	case testStr, "vet", "lint":
		return T1, "go test/vet"
	case runStr:
		return T2, "go run: local script execution"
	case "env", "version", "doc", "list", "tool":
		return T0, "go read-only"
	}
	return T1, "go: " + sub
}

// classifyBuildTool classifies task/make targets.
func classifyBuildTool(tokens []string, name string) (RiskTier, string) {
	if len(tokens) >= 2 {
		target := tokens[1]
		switch {
		case strings.Contains(target, "check") ||
			strings.Contains(target, testStr) ||
			strings.Contains(target, "lint") ||
			strings.Contains(target, "build"):
			return T1, name + " " + target
		case strings.Contains(target, "deploy") || strings.Contains(target, "push"):
			return T6, name + " " + target + ": remote mutation"
		case strings.Contains(target, "clean") || strings.Contains(target, "rm"):
			return T5, name + " " + target + ": destructive local"
		}
	}
	return T1, reasonBuildTool
}

// classifyNPM classifies npm/yarn/pnpm/bun commands.
func classifyNPM(tokens []string) (RiskTier, string) {
	if len(tokens) < 2 {
		return T1, "package manager"
	}
	sub := tokens[1]
	switch sub {
	case runStr, "exec", "start", "build", testStr, "lint",
		installStr, "ci", "i":
		return T1, "npm/yarn/pnpm: " + sub
	case "publish", "deploy":
		return T6, "npm publish: remote mutation"
	case "uninstall", "remove":
		return T3, "npm uninstall: safe local mutation"
	}
	return T1, "package manager: " + sub
}

// classifyDevOps classifies docker/kubectl/helm.
func classifyDevOps(tokens []string, name string) (RiskTier, string) {
	if len(tokens) < 2 {
		return T4, name + ": operation"
	}
	sub := tokens[1]
	switch sub {
	case "ps", "images", "info", "inspect", "logs", "stats",
		"top", "version", "get", "describe", statusStr:
		return T0, name + " read-only"
	case runStr, "exec", "start", "stop", "restart",
		"apply", "create", "upgrade", installStr:
		return T6, name + ": " + sub + " remote/service mutation"
	case "rm", "rmi", "remove", "delete", "uninstall":
		return T5, name + ": " + sub + " destructive"
	case "push", "deploy":
		return T6, name + ": " + sub + " remote mutation"
	}
	return T4, name + ": operation"
}

// hasFlag checks if tokens contains a specific flag string.
func hasFlag(tokens []string, flag string) bool {
	for _, t := range tokens {
		if t == flag {
			return true
		}
	}
	return false
}

// tokenize splits a command string into tokens, handling basic quoting.
// Single and double quoted strings are treated as single tokens (quotes stripped).
func tokenize(cmd string) []string {
	var tokens []string
	var cur strings.Builder
	inSingle := false
	inDouble := false

	for _, r := range cmd {
		switch {
		case r == '\'' && !inDouble:
			inSingle = !inSingle
		case r == '"' && !inSingle:
			inDouble = !inDouble
		case !inSingle && !inDouble && unicode.IsSpace(r):
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens
}
