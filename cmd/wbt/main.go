// wbt is the one-click installer CLI for wayneblacktea.
//
// Usage:
//
//	wbt init   — interactive wizard that writes .env and .mcp.json
//	wbt serve  — loads .env and starts the wayneblacktea-server binary
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
)

const usage = `wbt — wayneblacktea one-click installer

Commands:
  wbt init   Run interactive setup wizard (writes .env and .mcp.json)
  wbt serve  Load .env and start the wayneblacktea-server
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
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n%s", os.Args[1], usage)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
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

	claudeKey, err := promptRequired(r, "API key for Claude (CLAUDE_API_KEY): ", "CLAUDE_API_KEY must not be empty")
	if err != nil {
		return fmt.Errorf("reading CLAUDE_API_KEY: %w", err)
	}

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

	envContent := buildEnvFile(claudeKey, apiKey, port, db)
	if err := os.WriteFile(".env", []byte(envContent), 0o600); err != nil {
		return fmt.Errorf("writing .env: %w", err)
	}

	mcpJSON, err := buildMCPJSON(claudeKey, db)
	if err != nil {
		return err
	}
	if err := os.WriteFile(".mcp.json", mcpJSON, 0o600); err != nil {
		return fmt.Errorf("writing .mcp.json: %w", err)
	}

	fmt.Println("Created .env and .mcp.json — run `wbt serve` to start")
	return nil
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

	if os.Getenv("API_KEY") == "" && os.Getenv("CLAUDE_API_KEY") == "" {
		return fmt.Errorf("API_KEY or CLAUDE_API_KEY must be set (run `wbt init` first)")
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
func buildEnvFile(claudeKey, apiKey, port string, db dbConfig) string {
	var sb strings.Builder
	sb.WriteString("# wayneblacktea — generated by wbt init\n")
	writeEnvLine(&sb, "CLAUDE_API_KEY", claudeKey)
	writeEnvLine(&sb, "API_KEY", apiKey)
	writeEnvLine(&sb, "PORT", port)
	writeEnvLine(&sb, "STORAGE_BACKEND", db.storageBackend)
	if db.databaseURL != "" {
		writeEnvLine(&sb, "DATABASE_URL", db.databaseURL)
	}
	if db.sqlitePath != "" {
		writeEnvLine(&sb, "SQLITE_PATH", db.sqlitePath)
	}
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

// mcpConfig is the shape of .mcp.json written for Claude Desktop / MCP clients.
type mcpConfig struct {
	MCPServers map[string]mcpServer `json:"mcpServers"`
}

type mcpServer struct {
	Command string            `json:"command"`
	Env     map[string]string `json:"env"`
}

// buildMCPJSON marshals the .mcp.json content.
func buildMCPJSON(claudeKey string, db dbConfig) ([]byte, error) {
	env := map[string]string{
		"CLAUDE_API_KEY":  claudeKey,
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
				Command: "wayneblacktea-mcp",
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
