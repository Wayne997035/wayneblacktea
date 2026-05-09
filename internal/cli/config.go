package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// WbtConfig is the schema for ~/.config/wayneblacktea/config.yaml.
// All fields are optional strings so that partial configs are accepted on
// read without overriding env vars that are already set.
type WbtConfig struct {
	APIKey      string `yaml:"api_key"`
	DatabaseURL string `yaml:"database_url"`
	Port        string `yaml:"port"`
	// storage
	StorageBackend string `yaml:"storage_backend"`
	SQLitePath     string `yaml:"sqlite_path"`
}

// MCPConfig is the shape of .mcp.json written for Claude Desktop / MCP clients.
type MCPConfig struct {
	MCPServers map[string]MCPServer `json:"mcpServers"`
}

// MCPServer represents a single MCP server entry in .mcp.json.
type MCPServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env"`
}

// ConfigPath returns the canonical path to the global config file:
// $XDG_CONFIG_HOME/wayneblacktea/config.yaml (defaults to ~/.config/…).
// The parent directory is created if it does not exist.
func ConfigPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating user config dir: %w", err)
	}
	dir := filepath.Join(base, "wayneblacktea")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating config dir %s: %w", dir, err)
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// WriteGlobalConfig serialises cfg to ~/.config/wayneblacktea/config.yaml
// with mode 0600 (credential-bearing file).
func WriteGlobalConfig(cfg WbtConfig) error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	b, err := yaml.Marshal(cfg) //nolint:gosec // G117: marshaling config struct to 0600 file; not a secret leak
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("writing config %s: %w", path, err)
	}
	return nil
}

// LoadGlobalConfig reads ~/.config/wayneblacktea/config.yaml and sets env vars
// for any keys present in the file that are not already set.
//
// Missing file is silently ignored. Parse errors are returned.
func LoadGlobalConfig() error {
	path, err := ConfigPath()
	if err != nil {
		// If we can't determine the config dir, treat as absent.
		return nil //nolint:nilerr // best-effort: config dir unavailable, fall through to .env
	}
	b, err := os.ReadFile(path) //nolint:gosec // G304: path comes from os.UserConfigDir + known sub-path, not user input
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading global config %s: %w", path, err)
	}
	var cfg WbtConfig
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return fmt.Errorf("parsing global config %s: %w", path, err)
	}
	SetIfAbsent("API_KEY", cfg.APIKey)
	SetIfAbsent("DATABASE_URL", cfg.DatabaseURL)
	SetIfAbsent("PORT", cfg.Port)
	SetIfAbsent("STORAGE_BACKEND", cfg.StorageBackend)
	SetIfAbsent("SQLITE_PATH", cfg.SQLitePath)
	return nil
}

// SetIfAbsent sets the env var key to value only when value is non-empty and
// the env var is not already set.
func SetIfAbsent(key, value string) {
	if value == "" {
		return
	}
	if os.Getenv(key) == "" {
		_ = os.Setenv(key, value)
	}
}

// DBConfigToWbtConfig converts wizard-collected DBConfig into WbtConfig.
func DBConfigToWbtConfig(apiKey, port string, db DBConfig) WbtConfig {
	return WbtConfig{
		APIKey:         apiKey,
		Port:           port,
		StorageBackend: db.StorageBackend,
		DatabaseURL:    db.DatabaseURL,
		SQLitePath:     db.SQLitePath,
	}
}

// WriteMCPJSON marshals the .mcp.json content and returns the bytes.
//
// The MCP entry points at `wbt mcp` rather than the standalone
// `wayneblacktea-mcp` binary so that `go install .../cmd/wbt@latest`
// installs everything an end user needs.
func WriteMCPJSON(db DBConfig) ([]byte, error) {
	env := map[string]string{
		"STORAGE_BACKEND": db.StorageBackend,
	}
	if db.DatabaseURL != "" {
		env["DATABASE_URL"] = db.DatabaseURL
	}
	if db.SQLitePath != "" {
		env["SQLITE_PATH"] = db.SQLitePath
	}
	cfg := MCPConfig{
		MCPServers: map[string]MCPServer{
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

// ReadEnvPort reads the PORT env var and returns "8080" as the default.
func ReadEnvPort() string {
	if p := os.Getenv("PORT"); p != "" {
		return p
	}
	return "8080"
}
