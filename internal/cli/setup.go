// setup.go implements `wbt setup`, the one-command install entry point.
// The 9-step orchestration is documented in design-specs/setup-flow.md
// (SA spec acfe9d7f1fbca1a54). Each step is a separate function so future
// flags (--dry-run, --no-mcp, etc.) can be inserted without restructuring.

package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/lifecycle"
	"gopkg.in/yaml.v3"
)

// healthPollInterval is how often `wbt setup` re-probes /health.
const healthPollInterval = 200 * time.Millisecond

// sqliteBackend is the canonical name we write into config.yaml.
const sqliteBackend = "sqlite"

// healthPollDeadline bounds the wait for the server to become healthy.
// The default is generous to absorb slow first-launch SQLite migration.
const healthPollDeadline = 15 * time.Second

// RunSetup orchestrates the one-command install. See package doc for the
// step ordering. args is the slice of CLI arguments after "setup".
//
// Supported flags:
//
//	--port=<n>      override port (default: WBT_PORT env or 8080 from config)
//	--server-bin=<path>  override wayneblacktea-server path (for tests)
//	--mcp-name=<n>  override MCP server name (default: wayneblacktea)
//	--no-mcp        skip claude mcp registration entirely
func RunSetup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	portFlag := fs.Int("port", 0, "TCP port for the wayneblacktea HTTP server (default: WBT_PORT or 8080)")
	serverBin := fs.String("server-bin", "", "absolute path to wayneblacktea-server (default: exec.LookPath)")
	mcpName := fs.String("mcp-name", "wayneblacktea", "MCP server name registered with claude CLI")
	noMCP := fs.Bool("no-mcp", false, "skip automatic `claude mcp add` registration")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("setup: %w", err)
	}

	step("Reading or creating config…")
	apiKey, cfg, err := readOrCreateConfig()
	if err != nil {
		return err
	}
	check("Config ready")

	step("Ensuring SQLite directory…")
	if err := ensureSQLite(cfg); err != nil {
		return err
	}
	check("SQLite directory ready")

	step("Resolving port…")
	port, err := resolvePort(*portFlag, cfg)
	if err != nil {
		return err
	}
	check(fmt.Sprintf("Port resolved: %d", port))

	// Persist the resolved port so `wbt status` (which can't read the live
	// server's bound port directly) reports it correctly.
	if cfg.Port != strconv.Itoa(port) {
		cfg.Port = strconv.Itoa(port)
		if err := WriteGlobalConfig(cfg); err != nil {
			return fmt.Errorf("writing resolved port to config: %w", err)
		}
	}

	step("Checking for an existing healthy server…")
	if reuseExistingServer(port) {
		check("Reusing existing healthy server")
		registerMCPIfRequested(*noMCP, *mcpName, port)
		printNextSteps(port)
		return nil
	}

	step("Reclaiming TCP port if occupied…")
	killCtx, killCancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := lifecycle.KillOccupier(killCtx, port, nil); err != nil {
		killCancel()
		return fmt.Errorf("reclaiming port %d: %w", port, err)
	}
	killCancel()
	check("Port is free")

	step("Spawning wayneblacktea-server in the background…")
	binPath, err := resolveServerBinary(*serverBin)
	if err != nil {
		return err
	}
	handle, err := spawnServer(binPath, apiKey, port, cfg)
	if err != nil {
		return err
	}
	check(fmt.Sprintf("Server spawned (pid %d, logs %s)", handle.PID, handle.LogPath))

	step("Waiting for /health…")
	if err := waitForHealth(port, healthPollDeadline); err != nil {
		return fmt.Errorf("server failed health check: %w", err)
	}
	check("Server is healthy")

	registerMCPIfRequested(*noMCP, *mcpName, port)

	printNextSteps(port)
	return nil
}

// readOrCreateConfig loads the existing global config or creates a fresh
// one. The API key is preserved when present; otherwise a 32-byte hex key
// is generated. Returns (apiKey, cfg, err).
func readOrCreateConfig() (string, WbtConfig, error) {
	cfgPath, err := ConfigPath()
	if err != nil {
		return "", WbtConfig{}, fmt.Errorf("resolving config path: %w", err)
	}
	cfg, _ := loadConfigBytes(cfgPath) // missing-file → empty struct, no error
	// Default the storage backend before we test for missing API key so
	// downstream writers see a complete struct.
	if cfg.StorageBackend == "" {
		cfg.StorageBackend = sqliteBackend
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		key, err := RandomHex(32)
		if err != nil {
			return "", WbtConfig{}, fmt.Errorf("generating API key: %w", err)
		}
		cfg.APIKey = key
	}
	if cfg.Port == "" {
		cfg.Port = "8080"
	}
	if cfg.StorageBackend == sqliteBackend && cfg.SQLitePath == "" {
		cfg.SQLitePath = defaultSQLitePath()
	}
	if err := WriteGlobalConfig(cfg); err != nil {
		return "", WbtConfig{}, fmt.Errorf("writing config: %w", err)
	}
	return cfg.APIKey, cfg, nil
}

// loadConfigBytes reads and parses cfgPath, returning empty WbtConfig +
// nil err when the file is absent so the caller can treat it as a fresh
// install. Other errors are surfaced.
func loadConfigBytes(cfgPath string) (WbtConfig, error) {
	b, err := os.ReadFile(cfgPath) //nolint:gosec // G304: cfgPath from ConfigPath() in user config dir, not user input
	if errors.Is(err, os.ErrNotExist) {
		return WbtConfig{}, nil
	}
	if err != nil {
		return WbtConfig{}, fmt.Errorf("reading %s: %w", cfgPath, err)
	}
	var cfg WbtConfig
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return WbtConfig{}, fmt.Errorf("parsing %s: %w", cfgPath, err)
	}
	return cfg, nil
}

// ensureSQLite makes the SQLite parent directory + touches the DB file (mode
// 0600). It does NOT apply schema — the server itself does that on first
// connect (see internal/storage/sqlite/db.go). This avoids a race where the
// CLI and server both try to ALTER TABLE.
func ensureSQLite(cfg WbtConfig) error {
	if cfg.StorageBackend != sqliteBackend {
		return nil // Postgres backend has no local file
	}
	path := cfg.SQLitePath
	if path == "" {
		path = defaultSQLitePath()
	}
	if path == ":memory:" {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating SQLite dir %s: %w", dir, err)
	}
	// Touch the file with 0600 perms if it doesn't already exist.
	f, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE, 0o600) //nolint:gosec // G304: path resolved from config / XDG, not user input
	if err != nil {
		return fmt.Errorf("touching SQLite file %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing SQLite file %s: %w", path, err)
	}
	return nil
}

// defaultSQLitePath returns $XDG_DATA_HOME/wayneblacktea/wbt.db with the
// usual $HOME/.local/share fallback. WBT_DB_PATH overrides everything.
func defaultSQLitePath() string {
	if v := strings.TrimSpace(os.Getenv("WBT_DB_PATH")); v != "" {
		return v
	}
	base := strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
	if base == "" {
		if h, err := os.UserHomeDir(); err == nil {
			base = filepath.Join(h, ".local", "share")
		} else {
			base = filepath.Join(".", ".local", "share")
		}
	}
	return filepath.Join(base, "wayneblacktea", "wbt.db")
}

// resolvePort picks WBT_PORT > --port flag > cfg.Port > 8080.
// The order favours explicit env override so CI scripts win.
func resolvePort(flagPort int, cfg WbtConfig) (int, error) {
	if v := strings.TrimSpace(os.Getenv("WBT_PORT")); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("WBT_PORT=%q: %w", v, err)
		}
		return validatePort(p)
	}
	if flagPort > 0 {
		return validatePort(flagPort)
	}
	if cfg.Port != "" {
		p, err := strconv.Atoi(cfg.Port)
		if err != nil {
			return 0, fmt.Errorf("config port %q: %w", cfg.Port, err)
		}
		return validatePort(p)
	}
	return 8080, nil
}

// validatePort enforces 1-65535.
func validatePort(p int) (int, error) {
	if p < 1 || p > 65535 {
		return 0, fmt.Errorf("port %d out of range 1-65535", p)
	}
	return p, nil
}

// reuseExistingServer returns true when the on-disk PID file references a
// live process AND /health responds 200. In that case we skip the kill +
// spawn dance.
func reuseExistingServer(port int) bool {
	pidPath := lifecycle.PIDPath("")
	pid, err := lifecycle.ReadPID(pidPath)
	if err != nil {
		return false
	}
	sup := lifecycle.NewNohupSupervisor()
	status, err := sup.Status(pid)
	if err != nil || !status.Running {
		// Process is gone; remove the stale PID file so subsequent writes succeed cleanly.
		_ = lifecycle.RemovePID(pidPath)
		return false
	}
	return probeHealth(port)
}

// HealthOK is the exported probeHealth helper for tests. It performs one
// GET /health and returns true when the response is 200. Not intended for
// production callers; setup.go uses probeHealth directly.
func HealthOK(port int) bool { return probeHealth(port) }

// probeHealth issues one GET /health and returns whether the response is 200.
func probeHealth(port int) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	url := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	return resp.StatusCode == http.StatusOK
}

// resolveServerBinary returns either the --server-bin flag value (when set,
// for tests) or the first PATH match for "wayneblacktea-server".
func resolveServerBinary(override string) (string, error) {
	if strings.TrimSpace(override) != "" {
		if !filepath.IsAbs(override) {
			abs, err := filepath.Abs(override)
			if err != nil {
				return "", fmt.Errorf("resolving --server-bin %q: %w", override, err)
			}
			override = abs
		}
		return override, nil
	}
	binPath, err := exec.LookPath("wayneblacktea-server")
	if err != nil {
		return "", fmt.Errorf("wayneblacktea-server not found in PATH: %w", err)
	}
	return binPath, nil
}

// spawnServer hands off to the lifecycle nohup supervisor with paths
// resolved from XDG.
func spawnServer(binPath, apiKey string, port int, cfg WbtConfig) (*lifecycle.Handle, error) {
	sup := lifecycle.NewNohupSupervisor()
	pidPath := lifecycle.PIDPath("")
	logPath := defaultLogPath()
	env := buildServerEnv(apiKey, port, cfg)
	handle, err := sup.Start(context.Background(), lifecycle.StartOptions{
		BinaryPath: binPath,
		LogPath:    logPath,
		PIDPath:    pidPath,
		Env:        env,
	})
	if err != nil {
		return nil, fmt.Errorf("starting server: %w", err)
	}
	return handle, nil
}

// defaultLogPath returns $XDG_STATE_HOME/wayneblacktea/server.log with the
// $HOME/.local/state fallback.
func defaultLogPath() string {
	base := strings.TrimSpace(os.Getenv("XDG_STATE_HOME"))
	if base == "" {
		if h, err := os.UserHomeDir(); err == nil {
			base = filepath.Join(h, ".local", "state")
		} else {
			base = filepath.Join(".", ".local", "state")
		}
	}
	return filepath.Join(base, "wayneblacktea", "server.log")
}

// buildServerEnv composes the environment slice handed to the spawned
// server. We start from os.Environ() so the user's PATH / LANG / HOME /
// timezone settings are preserved, then layer in the values the CLI knows.
func buildServerEnv(apiKey string, port int, cfg WbtConfig) []string {
	env := append([]string{}, os.Environ()...)
	env = setEnvKV(env, "API_KEY", apiKey)
	env = setEnvKV(env, "PORT", strconv.Itoa(port))
	env = setEnvKV(env, "STORAGE_BACKEND", cfg.StorageBackend)
	if cfg.SQLitePath != "" {
		env = setEnvKV(env, "SQLITE_PATH", cfg.SQLitePath)
	}
	if cfg.DatabaseURL != "" {
		env = setEnvKV(env, "DATABASE_URL", cfg.DatabaseURL)
	}
	return env
}

// setEnvKV replaces (or appends) KEY=VALUE in env.
func setEnvKV(env []string, key, value string) []string {
	prefix := key + "="
	for i, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

// waitForHealth polls /health every healthPollInterval until success or
// the deadline expires.
func waitForHealth(port int, deadline time.Duration) error {
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if probeHealth(port) {
			return nil
		}
		time.Sleep(healthPollInterval)
	}
	return fmt.Errorf("/health did not return 200 within %s", deadline)
}

// registerMCPIfRequested invokes the claude-mcp registrar unless --no-mcp.
// Any error from registration is logged but non-fatal so the user can fix
// claude separately and re-run setup.
func registerMCPIfRequested(skip bool, name string, port int) {
	if skip {
		fmt.Fprintln(os.Stdout, "wbt: --no-mcp set; skipping claude mcp registration")
		return
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
	if err := RegisterClaudeMCP(name, url, MCPTransportHTTP); err != nil {
		fmt.Fprintf(os.Stderr, "wbt: claude mcp registration failed: %v\n", err)
		fmt.Fprintln(os.Stderr, "wbt: setup continues; fix claude and re-run `wbt setup` to retry MCP wiring.")
	}
}

// printNextSteps prints final user-facing hints.
func printNextSteps(port int) {
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	fmt.Fprintln(os.Stdout)
	fmt.Fprintf(os.Stdout, "All set. wayneblacktea is running at %s\n", url)
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "Next commands:")
	fmt.Fprintln(os.Stdout, "  wbt status        - show server state")
	fmt.Fprintln(os.Stdout, "  wbt stop          - stop the background server")
	fmt.Fprintln(os.Stdout, "  wbt restart       - stop + setup")
}

// step prints a step prefix line. Kept very small so the surrounding
// orchestration reads top-to-bottom.
func step(msg string) {
	fmt.Fprintf(os.Stdout, "==> %s\n", msg)
}

// check prints a success line for the last step.
func check(msg string) {
	fmt.Fprintf(os.Stdout, "  [ok] %s\n", msg)
}
