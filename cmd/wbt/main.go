// wbt is the one-click installer CLI for wayneblacktea.
//
// Usage:
//
//	wbt init   — interactive wizard that writes .env and .mcp.json
//	wbt serve  — loads .env and starts the wayneblacktea-server binary
//	wbt guard  — manage guard bypass rules
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/guard"
	"github.com/Wayne997035/wayneblacktea/internal/mcprunner"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
)

const usage = `wbt — wayneblacktea one-click installer

Commands:
  wbt init   Run interactive setup wizard (writes .env and .mcp.json)
  wbt serve  Load .env and start the wayneblacktea-server (HTTP API)
  wbt mcp    Serve MCP stdio (wired into .mcp.json by ` + "`wbt init`" + `;
             open Claude Code from the directory containing .mcp.json)
  wbt guard  Manage guard bypass rules (see: wbt guard --help)
`

const guardUsage = `wbt guard — manage wbt-guard bypass rules

Subcommands:
  wbt guard bypass add   --scope <s> --target <t> [--tool <name>] [--ttl <duration>] --reason <text>
  wbt guard bypass list  [--scope <s>]
  wbt guard bypass revoke <id>

Scopes: global, repo, dir, file
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "%s", usage)
		os.Exit(1)
	}
	var err error
	switch os.Args[1] {
	case "init":
		err = runInit()
	case "serve":
		err = runServe()
	case "mcp":
		err = runMCP()
	case "guard":
		err = runGuard(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n%s", os.Args[1], usage)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// runGuard dispatches wbt guard subcommands.
func runGuard(args []string) error {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "%s", guardUsage)
		return fmt.Errorf("guard: missing subcommand")
	}
	if args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintf(os.Stdout, "%s", guardUsage)
		return nil
	}
	if args[0] != "bypass" {
		return fmt.Errorf("guard: unknown subcommand %q", args[0])
	}
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "%s", guardUsage)
		return fmt.Errorf("guard bypass: missing action (add|list|revoke)")
	}

	// Load .env for DATABASE_URL if present — non-fatal.
	_ = godotenv.Load()

	switch args[1] {
	case "add":
		return runGuardBypassAdd(args[2:])
	case "list":
		return runGuardBypassList(args[2:])
	case "revoke":
		return runGuardBypassRevoke(args[2:])
	default:
		return fmt.Errorf("guard bypass: unknown action %q (want add|list|revoke)", args[1])
	}
}

// runGuardBypassAdd adds a bypass rule.
func runGuardBypassAdd(args []string) error {
	fs := flag.NewFlagSet("guard bypass add", flag.ContinueOnError)
	scopeFlag := fs.String("scope", "", "bypass scope: global|repo|dir|file (required)")
	targetFlag := fs.String("target", "", "bypass target value (required)")
	toolFlag := fs.String("tool", "", "tool name to bypass (empty = all tools)")
	ttlFlag := fs.String("ttl", "", "bypass TTL duration (e.g. 1h, 24h, 7d); empty = no expiry")
	reasonFlag := fs.String("reason", "", "reason for bypass (required, must not be empty)")
	dangerouslyGlobal := fs.Bool(
		"i-understand-this-is-global",
		false,
		"required confirmation when --scope=global; whitelists every repo on every machine",
	)

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("guard bypass add: %w", err)
	}

	if err := validateGuardBypassFlags(*scopeFlag, *targetFlag, *reasonFlag, *dangerouslyGlobal); err != nil {
		return err
	}

	var expiresAt *time.Time
	if *ttlFlag != "" {
		dur, err := parseDuration(*ttlFlag)
		if err != nil {
			return fmt.Errorf("guard bypass add: --ttl %q: %w", *ttlFlag, err)
		}
		exp := time.Now().UTC().Add(dur)
		expiresAt = &exp
	}

	var toolName *string
	if *toolFlag != "" {
		toolName = toolFlag
	}

	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	pool, _ := guard.OpenPool(ctx, dbURL)
	if pool == nil {
		return fmt.Errorf("guard bypass add: database unavailable (set DATABASE_URL)")
	}
	store := guard.NewStore(pool)

	id, err := store.AddBypass(ctx, *scopeFlag, *targetFlag, toolName, *reasonFlag, currentUser(), expiresAt)
	if err != nil {
		return fmt.Errorf("guard bypass add: %w", err)
	}

	fmt.Printf("bypass added: %s\n", id)
	return nil
}

// validBypassScopes lists the scopes accepted by the schema CHECK constraint.
// Pulled out of validateGuardBypassFlags so it can be unit-tested without
// hitting the rest of the wiring.
//
//nolint:gochecknoglobals // immutable enum table; equivalent to a const set.
var validBypassScopes = map[string]bool{
	"file":   true,
	"dir":    true,
	"repo":   true,
	"global": true,
}

// overlyBroadScopes lists --target values that, while technically valid as
// absolute paths, would whitelist effectively every project on the machine
// and so are rejected with a more helpful error than the schema CHECK.
//
//nolint:gochecknoglobals // immutable allowlist.
var overlyBroadScopes = map[string]bool{
	"/":      true,
	"/home":  true,
	"/Users": true,
}

// validateGuardBypassFlags exhaustively validates the (scope, target, reason)
// triple plus the global confirmation. Done client-side so:
//   - The error UX is "valid scopes are file, dir, repo, global", not
//     `pq: violates check constraint "guard_bypasses_scope_check"`.
//   - Combinations the DB cannot express (e.g. global without the
//     i-understand-this-is-global flag, file scope with a relative path)
//     are rejected up-front.
//
// Validation rules:
//   - scope MUST be in {file, dir, repo, global}.
//   - When scope=global, target MUST be the literal "global" AND the
//     --i-understand-this-is-global flag MUST be set.
//   - When scope in {file, dir}, target MUST be an absolute path AND
//     MUST NOT be one of the overly-broad system roots ("/", "/home",
//     "/Users").
//   - reason MUST be non-empty (non-whitespace).
func validateGuardBypassFlags(scope, target, reason string, iUnderstandGlobal bool) error {
	if scope == "" {
		return fmt.Errorf("guard bypass add: --scope is required (one of: file, dir, repo, global)")
	}
	if !validBypassScopes[scope] {
		return fmt.Errorf("guard bypass add: --scope %q invalid; want one of: file, dir, repo, global", scope)
	}
	if target == "" {
		return fmt.Errorf("guard bypass add: --target is required")
	}
	if guard.IsWhitespacesOnly(reason) {
		return fmt.Errorf("guard bypass add: --reason is required and must not be empty or whitespace-only")
	}

	switch scope {
	case "global":
		if target != "global" {
			return fmt.Errorf(
				"guard bypass add: --scope global requires --target=global literal (got %q)",
				target,
			)
		}
		if !iUnderstandGlobal {
			return fmt.Errorf(
				"guard bypass add: --scope global requires --i-understand-this-is-global " +
					"(this whitelists every repo on every machine)",
			)
		}
	case "dir", "file":
		if !filepath.IsAbs(target) {
			return fmt.Errorf("guard bypass add: --scope %s requires an absolute --target path (got %q)", scope, target)
		}
		if overlyBroadScopes[target] {
			return fmt.Errorf(
				"guard bypass add: --scope %s --target %q would whitelist too broadly; pick a deeper directory",
				scope, target,
			)
		}
	}
	return nil
}

// runGuardBypassList lists active bypass rules.
func runGuardBypassList(args []string) error {
	fs := flag.NewFlagSet("guard bypass list", flag.ContinueOnError)
	scopeFlag := fs.String("scope", "", "filter by scope (optional)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("guard bypass list: %w", err)
	}

	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	pool, _ := guard.OpenPool(ctx, dbURL)
	if pool == nil {
		return fmt.Errorf("guard bypass list: database unavailable (set DATABASE_URL)")
	}
	store := guard.NewStore(pool)

	bypasses, err := store.ListBypasses(ctx, *scopeFlag)
	if err != nil {
		return fmt.Errorf("guard bypass list: %w", err)
	}

	if len(bypasses) == 0 {
		fmt.Println("no active bypasses")
		return nil
	}

	fmt.Printf("%-36s  %-6s  %-30s  %-10s  %s\n", "ID", "SCOPE", "TARGET", "TOOL", "REASON")
	fmt.Println(strings.Repeat("-", 110))
	for _, b := range bypasses {
		toolDisplay := "(all)"
		if b.ToolName != nil {
			toolDisplay = *b.ToolName
		}
		fmt.Printf("%-36s  %-6s  %-30s  %-10s  %s\n",
			b.ID.String(), b.Scope, b.Target, toolDisplay, b.Reason)
	}
	return nil
}

// runGuardBypassRevoke revokes a bypass by ID.
func runGuardBypassRevoke(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("guard bypass revoke: missing bypass ID")
	}
	idStr := args[0]
	id, err := uuid.Parse(idStr)
	if err != nil {
		return fmt.Errorf("guard bypass revoke: invalid UUID %q: %w", idStr, err)
	}

	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	pool, _ := guard.OpenPool(ctx, dbURL)
	if pool == nil {
		return fmt.Errorf("guard bypass revoke: database unavailable (set DATABASE_URL)")
	}
	store := guard.NewStore(pool)

	if err := store.RevokeBypass(ctx, id); err != nil {
		return fmt.Errorf("guard bypass revoke: %w", err)
	}

	fmt.Printf("bypass %s revoked\n", id)
	return nil
}

// parseDuration extends time.ParseDuration to accept "d" for days.
func parseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		days := strings.TrimSuffix(s, "d")
		d, err := time.ParseDuration(days + "h")
		if err != nil {
			return 0, fmt.Errorf("invalid day duration %q", s)
		}
		return d * 24, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("parsing duration %q: %w", s, err)
	}
	return d, nil
}

// currentUser returns the current OS user or "unknown".
func currentUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u := os.Getenv("USERNAME"); u != "" {
		return u
	}
	return "unknown"
}

// runMCP serves MCP stdio by delegating to the shared mcprunner package
// (also used by cmd/mcp). Reads .env from CWD if present so users do not
// need to set DATABASE_URL / CLAUDE_API_KEY in the environment that
// Claude Code launches the hook from.
func runMCP() error {
	// Best-effort .env load — absent file is not fatal because Claude Code
	// may export the env vars itself.
	_ = godotenv.Load()
	if err := mcprunner.Run(); err != nil {
		return fmt.Errorf("running MCP stdio server: %w", err)
	}
	return nil
}

// dbConfig holds the database configuration collected during init.
type dbConfig struct {
	storageBackend string
	databaseURL    string
	sqlitePath     string
}

// runInit runs the interactive wizard and writes .env + .mcp.json.
func runInit() error {
	r := bufio.NewReader(os.Stdin)

	db, err := collectDBConfig(r)
	if err != nil {
		return err
	}

	port, err := promptWithDefault(r, "Server port [default: 8080]: ", "8080")
	if err != nil {
		return fmt.Errorf("reading server port: %w", err)
	}

	apiKey, err := collectAPIKey(r)
	if err != nil {
		return err
	}

	envContent := buildEnvFile(apiKey, port, db)
	if err := os.WriteFile(".env", []byte(envContent), 0o600); err != nil {
		return fmt.Errorf("writing .env: %w", err)
	}

	mcpJSON, err := buildMCPJSON(db)
	if err != nil {
		return err
	}
	if err := os.WriteFile(".mcp.json", mcpJSON, 0o600); err != nil {
		return fmt.Errorf("writing .mcp.json: %w", err)
	}

	fmt.Println("Created .env and .mcp.json — run `wbt serve` to start")
	fmt.Println("No AI provider key is required for core MCP memory features.")
	printHookSnippet(apiKey, port)
	return nil
}

// printHookSnippet prints a copy-pasteable ~/.claude/settings.json snippet
// that registers wbt-hook as a Claude Code PostToolUse global hook.
// It does NOT write the file automatically; the user must copy it manually
// so they can review it and merge with any existing settings.
func printHookSnippet(apiKey, port string) {
	fmt.Printf(`
--- PostToolUse hook setup (optional) ---
To capture every Claude Code tool call in your activity log, add the following
to ~/.claude/settings.json (merge with existing content if the file already exists).

WARNING: WBT_HOOK_RAW=1 logs raw tool input — only enable in trusted dev environments.

{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "wbt-hook",
            "environment": {
              "API_KEY": %q,
              "PORT": %q
            }
          }
        ]
      }
    ]
  }
}
-----------------------------------------
`, apiKey, port)
}

// collectDBConfig asks the user whether to use SQLite or Postgres and collects
// the relevant connection details.
func collectDBConfig(r *bufio.Reader) (dbConfig, error) {
	dbChoice, err := prompt(r, "Database? [1] SQLite (default)  [2] Postgres: ")
	if err != nil {
		return dbConfig{}, fmt.Errorf("reading database choice: %w", err)
	}

	if strings.TrimSpace(dbChoice) == "2" {
		return collectPostgresConfig(r)
	}
	return collectSQLiteConfig(r)
}

// collectPostgresConfig collects Postgres connection details.
func collectPostgresConfig(r *bufio.Reader) (dbConfig, error) {
	dsn, err := promptRequired(r, "DATABASE_URL (postgres://...): ", "DATABASE_URL must not be empty")
	if err != nil {
		return dbConfig{}, fmt.Errorf("reading DATABASE_URL: %w", err)
	}
	return dbConfig{storageBackend: "postgres", databaseURL: dsn}, nil
}

// collectSQLiteConfig collects SQLite file path details.
func collectSQLiteConfig(r *bufio.Reader) (dbConfig, error) {
	defaultDBPath := filepath.Join(homeDir(), ".wayneblacktea", "data.db")
	rawPath, err := promptWithDefault(r, fmt.Sprintf("DB path [default: %s]: ", defaultDBPath), defaultDBPath)
	if err != nil {
		return dbConfig{}, fmt.Errorf("reading SQLite path: %w", err)
	}
	return dbConfig{storageBackend: "sqlite", sqlitePath: rawPath}, nil
}

// collectAPIKey asks for an API key or generates one if the user leaves it empty.
func collectAPIKey(r *bufio.Reader) (string, error) {
	raw, err := prompt(r, "API key for HTTP server (API_KEY) [default: generate random]: ")
	if err != nil {
		return "", fmt.Errorf("reading API key: %w", err)
	}
	if key := strings.TrimSpace(raw); key != "" {
		return key, nil
	}
	key, err := randomHex(32)
	if err != nil {
		return "", fmt.Errorf("generating random API key: %w", err)
	}
	return key, nil
}

// runServe loads .env from the current directory and runs wayneblacktea-server.
func runServe() error {
	// Non-fatal: if .env doesn't exist, existing env vars are used (Railway, etc.)
	if err := godotenv.Load(".env"); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("loading .env: %w", err)
	}

	if os.Getenv("API_KEY") == "" {
		return fmt.Errorf("API_KEY must be set (run `wbt init` first)")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	fmt.Printf("Starting wayneblacktea at http://localhost:%s\n", port)

	serverBin, err := exec.LookPath("wayneblacktea-server")
	if err != nil {
		return fmt.Errorf("wayneblacktea-server not found in PATH: %w", err)
	}

	cmd := exec.CommandContext(context.Background(), serverBin) //nolint:gosec // G204: serverBin resolved from exec.LookPath, not user input
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("server exited: %w", err)
	}
	return nil
}

// prompt prints the question and returns the trimmed answer from r.
func prompt(r *bufio.Reader, question string) (string, error) {
	fmt.Fprintf(os.Stdout, "%s", question)
	line, err := r.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("reading input: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// promptRequired calls prompt and returns an error if the result is empty.
func promptRequired(r *bufio.Reader, question, emptyErrMsg string) (string, error) {
	val, err := prompt(r, question)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(val) == "" {
		return "", fmt.Errorf("%s", emptyErrMsg)
	}
	return val, nil
}

// promptWithDefault calls prompt and returns defaultVal if the user input is empty.
func promptWithDefault(r *bufio.Reader, question, defaultVal string) (string, error) {
	val, err := prompt(r, question)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(val) == "" {
		return defaultVal, nil
	}
	return strings.TrimSpace(val), nil
}

// randomHex generates n cryptographically random bytes encoded as hex.
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("crypto/rand: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// homeDir returns the user home directory, falling back to "." on error.
func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}

// buildEnvFile returns the contents of the .env file to write.
func buildEnvFile(apiKey, port string, db dbConfig) string {
	var sb strings.Builder
	sb.WriteString("# wayneblacktea — generated by wbt init\n")
	writeEnvLine(&sb, "API_KEY", apiKey)
	writeEnvLine(&sb, "PORT", port)
	writeEnvLine(&sb, "STORAGE_BACKEND", db.storageBackend)
	if db.databaseURL != "" {
		writeEnvLine(&sb, "DATABASE_URL", db.databaseURL)
	}
	if db.sqlitePath != "" {
		writeEnvLine(&sb, "SQLITE_PATH", db.sqlitePath)
	}
	sb.WriteString("\n# Optional AI features. Leave unset for memory-only mode.\n")
	writeOptionalEnvLine(&sb, "GEMINI_API_KEY", "knowledge embeddings / dedup")
	writeOptionalEnvLine(&sb, "GROQ_API_KEY", "Discord /analyze")
	writeOptionalEnvLine(&sb, "CLAUDE_API_KEY", "advanced automation: classifier / reflection / snapshots")
	return sb.String()
}

// writeEnvLine appends a KEY=value line, double-quoting the value when it
// contains any character that godotenv would otherwise interpret as syntax:
//   - space / tab: would split the line
//   - '#': would start a comment, silently truncating the value
//   - '"' / '\\': would corrupt the parser
//   - newline / carriage return: would break the file structure
//
// This prevents silent credential corruption when an API key or DSN contains
// '#' (a common character in randomly-generated secrets) or other shell-special
// chars.
func writeEnvLine(sb *strings.Builder, key, value string) {
	if strings.ContainsAny(value, " \t#\"\\\r\n") {
		fmt.Fprintf(sb, "%s=%q\n", key, value)
	} else {
		fmt.Fprintf(sb, "%s=%s\n", key, value)
	}
}

// writeOptionalEnvLine emits a two-line block: a comment describing the
// purpose, then a fully-commented assignment the user can uncomment. Splitting
// across two lines avoids the inline-comment trap where uncommenting
// `# KEY= # note` leaves a trailing inline comment that godotenv would treat
// as part of the value if the user accidentally omits the leading space.
func writeOptionalEnvLine(sb *strings.Builder, key, note string) {
	fmt.Fprintf(sb, "# %s\n# %s=\n", note, key)
}

// mcpConfig is the shape of .mcp.json written for Claude Desktop / MCP clients.
type mcpConfig struct {
	MCPServers map[string]mcpServer `json:"mcpServers"`
}

type mcpServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env"`
}

// buildMCPJSON marshals the .mcp.json content.
//
// The MCP entry points at `wbt mcp` (delegating to the shared mcprunner
// package) rather than the standalone `wayneblacktea-mcp` binary. This way
// `go install …/cmd/wbt@latest` installs everything an end user needs — no
// separate go install for the MCP binary, no name mismatch between the
// installed binary and the .mcp.json command field.
func buildMCPJSON(db dbConfig) ([]byte, error) {
	env := map[string]string{
		"STORAGE_BACKEND": db.storageBackend,
	}
	if db.databaseURL != "" {
		env["DATABASE_URL"] = db.databaseURL
	}
	if db.sqlitePath != "" {
		env["SQLITE_PATH"] = db.sqlitePath
	}
	cfg := mcpConfig{
		MCPServers: map[string]mcpServer{
			"wayneblacktea": {
				Command: "wbt",
				Args:    []string{"mcp"},
				Env:     env,
			},
		},
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling .mcp.json: %w", err)
	}
	return b, nil
}
