package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RunInit runs the interactive wizard and writes .env + .mcp.json.
func RunInit() error {
	r := bufio.NewReader(os.Stdin)

	db, err := CollectDBConfig(r)
	if err != nil {
		return err
	}

	port, err := PromptWithDefault(r, "Server port [default: 8080]: ", "8080")
	if err != nil {
		return fmt.Errorf("reading server port: %w", err)
	}

	apiKey, err := CollectAPIKey(r)
	if err != nil {
		return err
	}

	envContent := GenerateEnvFile(apiKey, port, db)
	if err := os.WriteFile(".env", []byte(envContent), 0o600); err != nil {
		return fmt.Errorf("writing .env: %w", err)
	}

	// Write global config so `wbt serve` works from any directory without -env.
	cfg := DBConfigToWbtConfig(apiKey, port, db)
	if err := WriteGlobalConfig(cfg); err != nil {
		// Non-fatal: user still has .env in the current dir.
		fmt.Fprintf(os.Stderr, "wbt: warning: could not write global config: %v\n", err)
	} else {
		cfgP, _ := ConfigPath()
		fmt.Printf("Created global config at %s\n", cfgP)
	}

	mcpJSON, err := WriteMCPJSON(db)
	if err != nil {
		return err
	}
	if err := os.WriteFile(".mcp.json", mcpJSON, 0o600); err != nil {
		return fmt.Errorf("writing .mcp.json: %w", err)
	}

	fmt.Println("Created .env and .mcp.json — run `wbt serve` to start")
	fmt.Println("No AI provider key is required for core MCP memory features.")
	PrintHookSnippet(apiKey, port)
	return nil
}

// PrintHookSnippet prints a copy-pasteable ~/.claude/settings.json snippet
// that registers wbt-hook as a Claude Code PostToolUse global hook.
func PrintHookSnippet(apiKey, port string) {
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

// CollectDBConfig asks the user whether to use SQLite or Postgres.
func CollectDBConfig(r *bufio.Reader) (DBConfig, error) {
	dbChoice, err := Prompt(r, "Database? [1] SQLite (default)  [2] Postgres: ")
	if err != nil {
		return DBConfig{}, fmt.Errorf("reading database choice: %w", err)
	}

	if strings.TrimSpace(dbChoice) == "2" {
		return CollectPostgresConfig(r)
	}
	return CollectSQLiteConfig(r)
}

// CollectPostgresConfig collects Postgres connection details.
func CollectPostgresConfig(r *bufio.Reader) (DBConfig, error) {
	dsn, err := PromptRequired(r, "DATABASE_URL (postgres://...): ", "DATABASE_URL must not be empty")
	if err != nil {
		return DBConfig{}, fmt.Errorf("reading DATABASE_URL: %w", err)
	}
	return DBConfig{StorageBackend: "postgres", DatabaseURL: dsn}, nil
}

// CollectSQLiteConfig collects SQLite file path details.
func CollectSQLiteConfig(r *bufio.Reader) (DBConfig, error) {
	defaultDBPath := filepath.Join(HomeDir(), ".wayneblacktea", "data.db")
	rawPath, err := PromptWithDefault(r, fmt.Sprintf("DB path [default: %s]: ", defaultDBPath), defaultDBPath)
	if err != nil {
		return DBConfig{}, fmt.Errorf("reading SQLite path: %w", err)
	}
	return DBConfig{StorageBackend: "sqlite", SQLitePath: rawPath}, nil
}

// CollectAPIKey asks for an API key or generates one if the user leaves it empty.
func CollectAPIKey(r *bufio.Reader) (string, error) {
	raw, err := Prompt(r, "API key for HTTP server (API_KEY) [default: generate random]: ")
	if err != nil {
		return "", fmt.Errorf("reading API key: %w", err)
	}
	if key := strings.TrimSpace(raw); key != "" {
		return key, nil
	}
	key, err := RandomHex(32)
	if err != nil {
		return "", fmt.Errorf("generating random API key: %w", err)
	}
	return key, nil
}

// Prompt prints the question and returns the trimmed answer from r.
func Prompt(r *bufio.Reader, question string) (string, error) {
	fmt.Fprintf(os.Stdout, "%s", question)
	line, err := r.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("reading input: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// PromptRequired calls Prompt and returns an error if the result is empty.
func PromptRequired(r *bufio.Reader, question, emptyErrMsg string) (string, error) {
	val, err := Prompt(r, question)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(val) == "" {
		return "", fmt.Errorf("%s", emptyErrMsg) //nolint:err113 // dynamic sentinel string from caller; no static error var
	}
	return val, nil
}

// PromptWithDefault calls Prompt and returns defaultVal if the user input is empty.
func PromptWithDefault(r *bufio.Reader, question, defaultVal string) (string, error) {
	val, err := Prompt(r, question)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(val) == "" {
		return defaultVal, nil
	}
	return strings.TrimSpace(val), nil
}

// HomeDir returns the user home directory, falling back to "." on error.
func HomeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}
