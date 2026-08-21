package main

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/buildinfo"
	"github.com/Wayne997035/wayneblacktea/internal/cli"
	"gopkg.in/yaml.v3"
)

// Fake DSNs used as test fixtures. Centralised so gosec G101 only needs to be
// silenced once and individual call sites stay within the line-length budget.
const (
	fakePostgresDSN      = "postgres://user:pass@host/db" //nolint:gosec // G101: test fixture
	fakePostgresDSNShort = "postgres://u:p@host/db"       //nolint:gosec // G101: test fixture
)

// TestIsVersionArg covers every flag form the install scripts may pass.
func TestIsVersionArg(t *testing.T) {
	t.Parallel()

	tests := []struct {
		arg  string
		want bool
	}{
		{"version", true},
		{"--version", true},
		{"-v", true},
		{"init", false},
		{"serve", false},
		{"", false},
		{"VERSION", false},   // case-sensitive on purpose
		{"--Version", false}, // case-sensitive on purpose
	}
	for _, tc := range tests {
		t.Run(tc.arg, func(t *testing.T) {
			t.Parallel()
			if got := isVersionArg(tc.arg); got != tc.want {
				t.Errorf("isVersionArg(%q) = %v, want %v", tc.arg, got, tc.want)
			}
		})
	}
}

// TestPrintVersion_Format verifies the version line matches the install
// scripts' parser expectation: "<binary> <version> (<commit>)".
// Install.sh extracts the version with `awk '{print $2}'`.
func TestPrintVersion_Format(t *testing.T) {
	// Save & restore process-global state mutated by this test.
	origArgs := os.Args
	origStdout := os.Stdout
	origVersion := version
	origCommit := commit
	origDate := date
	t.Cleanup(func() {
		os.Args = origArgs
		os.Stdout = origStdout
		version = origVersion
		commit = origCommit
		date = origDate
	})

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	os.Args = []string{"/usr/local/bin/wbt"}
	// 版本來源是 buildinfo.EffectiveVersion(),不是 main.version —— 這支 CLI 用
	// `go install <module>@<version>` 散佈,那條路徑不注入 ldflags,所以
	// main.version 永遠停在哨兵。這裡設的是 EffectiveVersion 真正會讀的那個。
	// 本測試原本設 main.version 並期望它被印出來,那是在釘一條已經沒有人餵值的路。
	origBIVersion := buildinfo.Version
	buildinfo.Version = "1.2.3"
	t.Cleanup(func() { buildinfo.Version = origBIVersion })
	version = "should-not-be-printed"
	commit = "abc123"
	date = "unknown" // suppress the second line for parser test

	printVersion()
	_ = w.Close()

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	got := string(out)
	want := "wbt 1.2.3 (abc123)\n"
	if got != want {
		t.Errorf("printVersion stdout = %q, want %q", got, want)
	}

	// Simulate install.sh awk parser to make sure the second whitespace
	// token is the version (regression guard for R-M1).
	fields := strings.Fields(strings.SplitN(got, "\n", 2)[0])
	if len(fields) < 2 || fields[1] != "1.2.3" {
		t.Errorf("printVersion: awk-style parse of %q did not yield version 1.2.3 (fields=%v)", got, fields)
	}
}

// TestBuildEnvFile verifies .env generation for SQLite and Postgres configs.
func TestBuildEnvFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		apiKey    string
		port      string
		db        cli.DBConfig
		wantLines []string
		noLines   []string
	}{
		{
			name:   "sqlite config",
			apiKey: "myapikey123",
			port:   "8080",
			db:     cli.DBConfig{StorageBackend: "sqlite", SQLitePath: "/home/user/.wayneblacktea/data.db"},
			wantLines: []string{
				"API_KEY=myapikey123",
				"PORT=8080",
				"STORAGE_BACKEND=sqlite",
				"SQLITE_PATH=/home/user/.wayneblacktea/data.db",
			},
			// CLAUDE_API_KEY must NOT be written as an active line — only
			// referenced in the commented hint. Regression guard against the
			// init wizard re-introducing a mandatory prompt.
			noLines: []string{"\nCLAUDE_API_KEY="},
		},
		{
			name:   "postgres config",
			apiKey: "prodapikey456",
			port:   "9090",
			db:     cli.DBConfig{StorageBackend: "postgres", DatabaseURL: fakePostgresDSN},
			wantLines: []string{
				"API_KEY=prodapikey456",
				"PORT=9090",
				"STORAGE_BACKEND=postgres",
				"DATABASE_URL=" + fakePostgresDSN,
			},
			noLines: []string{"\nCLAUDE_API_KEY="},
		},
		{
			name:      "sqlite config omits DATABASE_URL",
			apiKey:    "akey",
			port:      "8080",
			db:        cli.DBConfig{StorageBackend: "sqlite", SQLitePath: "./data.db"},
			wantLines: []string{"STORAGE_BACKEND=sqlite"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := cli.GenerateEnvFile(tc.apiKey, tc.port, tc.db)
			for _, want := range tc.wantLines {
				if !strings.Contains(got, want) {
					t.Errorf("GenerateEnvFile output missing %q; got:\n%s", want, got)
				}
			}
			for _, deny := range tc.noLines {
				if strings.Contains(got, deny) {
					t.Errorf("GenerateEnvFile output should not contain %q; got:\n%s", deny, got)
				}
			}
		})
	}
}

// TestBuildEnvFile_NoURLForSQLite verifies DATABASE_URL is absent for sqlite backend.
func TestBuildEnvFile_NoURLForSQLite(t *testing.T) {
	t.Parallel()
	got := cli.GenerateEnvFile("k", "8080", cli.DBConfig{StorageBackend: "sqlite", SQLitePath: "./data.db"})
	if strings.Contains(got, "DATABASE_URL") {
		t.Errorf("expected no DATABASE_URL in sqlite env, got:\n%s", got)
	}
}

// TestBuildMCPJSON verifies .mcp.json generation for both backends.
func TestBuildMCPJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		db       cli.DBConfig
		wantKeys []string
		noKeys   []string
	}{
		{
			name: "sqlite backend",
			db:   cli.DBConfig{StorageBackend: "sqlite", SQLitePath: "/tmp/test.db"},
			wantKeys: []string{
				"wayneblacktea",
				`"command": "wbt"`,
				`"args": [`,
				`"mcp"`,
				"SQLITE_PATH",
				"STORAGE_BACKEND",
			},
			// Regression guard: legacy binary name must not appear — `go install
			// .../cmd/wbt@latest` does not produce a `wayneblacktea-mcp` binary,
			// so the .mcp.json must point at `wbt mcp` instead.
			noKeys: []string{"CLAUDE_API_KEY", "DATABASE_URL", "wayneblacktea-mcp"},
		},
		{
			name: "postgres backend",
			db:   cli.DBConfig{StorageBackend: "postgres", DatabaseURL: fakePostgresDSNShort},
			wantKeys: []string{
				"wayneblacktea",
				`"command": "wbt"`,
				`"args": [`,
				`"mcp"`,
				"DATABASE_URL",
				"STORAGE_BACKEND",
			},
			noKeys: []string{"CLAUDE_API_KEY", "SQLITE_PATH", "wayneblacktea-mcp"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b, err := cli.WriteMCPJSON(tc.db)
			if err != nil {
				t.Fatalf("WriteMCPJSON returned error: %v", err)
			}
			got := string(b)
			for _, key := range tc.wantKeys {
				if !strings.Contains(got, key) {
					t.Errorf("WriteMCPJSON missing %q in output:\n%s", key, got)
				}
			}
			for _, key := range tc.noKeys {
				if strings.Contains(got, key) {
					t.Errorf("WriteMCPJSON should not contain %q in output:\n%s", key, got)
				}
			}
		})
	}
}

// TestRandomHex verifies random key generation.
func TestRandomHex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		n       int
		wantLen int
	}{
		{name: "32 bytes → 64 hex chars", n: 32, wantLen: 64},
		{name: "16 bytes → 32 hex chars", n: 16, wantLen: 32},
		{name: "1 byte → 2 hex chars", n: 1, wantLen: 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := cli.RandomHex(tc.n)
			if err != nil {
				t.Fatalf("RandomHex(%d) error: %v", tc.n, err)
			}
			if len(got) != tc.wantLen {
				t.Errorf("RandomHex(%d) len = %d, want %d", tc.n, len(got), tc.wantLen)
			}
			// Verify all chars are valid hex.
			for _, c := range got {
				if !strings.ContainsRune("0123456789abcdef", c) {
					t.Errorf("RandomHex produced non-hex char %q in %q", c, got)
				}
			}
		})
	}
}

// TestRandomHex_Uniqueness verifies two calls produce different values.
func TestRandomHex_Uniqueness(t *testing.T) {
	t.Parallel()
	a, err := cli.RandomHex(32)
	if err != nil {
		t.Fatalf("RandomHex error: %v", err)
	}
	b, err := cli.RandomHex(32)
	if err != nil {
		t.Fatalf("RandomHex error: %v", err)
	}
	if a == b {
		t.Error("RandomHex produced identical values on two calls")
	}
}

// TestRunServe_MissingBinary verifies RunServe returns an error when
// wayneblacktea-server is not in PATH.
// Cannot use t.Parallel because t.Setenv mutates process-global state.
func TestRunServe_MissingBinary(t *testing.T) {
	// Override PATH to an empty directory so lookup fails.
	emptyDir := t.TempDir()
	t.Setenv("PATH", emptyDir)
	// Set a required env var so we don't fail on validation.
	t.Setenv("API_KEY", "testkey")
	// Point godotenv at a non-existent file so Load is a no-op.
	_ = os.Remove(filepath.Join(".", ".env"))

	err := cli.RunServe([]string{})
	if err == nil {
		t.Fatal("RunServe: expected error when binary not in PATH, got nil")
	}
	if !strings.Contains(err.Error(), "wayneblacktea-server not found") {
		t.Errorf("RunServe error = %q, want substring 'wayneblacktea-server not found'", err.Error())
	}
}

// TestRunServe_MissingEnvVars verifies RunServe returns an error when API_KEY
// is not set. CLAUDE_API_KEY is optional and must not satisfy HTTP auth config.
// Cannot use t.Parallel because t.Setenv mutates process-global state.
func TestRunServe_MissingEnvVars(t *testing.T) {
	// Point XDG_CONFIG_HOME at an empty dir so no global config can supply API_KEY.
	emptyConfig := t.TempDir()
	setXDGConfigHome(t, emptyConfig)
	t.Setenv("API_KEY", "")
	t.Setenv("CLAUDE_API_KEY", "")

	err := cli.RunServe([]string{})
	if err == nil {
		t.Fatal("RunServe: expected error when env vars missing, got nil")
	}
	if !strings.Contains(err.Error(), "API_KEY must be set") {
		t.Errorf("RunServe error = %q, want substring about API_KEY", err.Error())
	}
}

// TestRunInit_DoesNotPromptForClaudeAPIKey proves the init wizard can complete
// with only database, port, and HTTP API key answers. CLAUDE_API_KEY remains an
// optional feature flag the user may add later.
func TestRunInit_DoesNotPromptForClaudeAPIKey(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	// Redirect XDG_CONFIG_HOME so we don't write to the real user config dir.
	setXDGConfigHome(t, tmp)

	oldStdin := os.Stdin
	oldStdout := os.Stdout
	defer func() {
		os.Stdin = oldStdin
		os.Stdout = oldStdout
	}()

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe stdin: %v", err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe stdout: %v", err)
	}
	os.Stdin = stdinR
	os.Stdout = stdoutW

	_, _ = stdinW.WriteString("\n\n8080\n\n")
	_ = stdinW.Close()

	err = cli.RunInit()
	_ = stdoutW.Close()
	if err != nil {
		t.Fatalf("RunInit returned error: %v", err)
	}

	out, err := io.ReadAll(stdoutR)
	if err != nil {
		t.Fatalf("reading stdout: %v", err)
	}
	if strings.Contains(string(out), "API key for Claude") {
		t.Fatalf("RunInit prompted for CLAUDE_API_KEY:\n%s", string(out))
	}
	// Phase 1-4 of docs/openrouter-fallback.md: the optional OpenRouter
	// provider MUST NOT be prompted in the default install flow either.
	if strings.Contains(string(out), "OpenRouter") || strings.Contains(string(out), "OPENROUTER") {
		t.Fatalf("RunInit prompted for OPENROUTER_API_KEY:\n%s", string(out))
	}

	envContent, err := os.ReadFile(".env")
	if err != nil {
		t.Fatalf("reading generated .env: %v", err)
	}
	if strings.Contains(string(envContent), "\nCLAUDE_API_KEY=") {
		t.Fatalf(".env should not contain active CLAUDE_API_KEY:\n%s", string(envContent))
	}
	if strings.Contains(string(envContent), "\nOPENROUTER_API_KEY=") {
		t.Fatalf(".env should not contain active OPENROUTER_API_KEY:\n%s", string(envContent))
	}

	mcpContent, err := os.ReadFile(".mcp.json")
	if err != nil {
		t.Fatalf("reading generated .mcp.json: %v", err)
	}
	if strings.Contains(string(mcpContent), "CLAUDE_API_KEY") {
		t.Fatalf(".mcp.json should not contain CLAUDE_API_KEY:\n%s", string(mcpContent))
	}
	if strings.Contains(string(mcpContent), "OPENROUTER_API_KEY") {
		t.Fatalf(".mcp.json should not contain OPENROUTER_API_KEY:\n%s", string(mcpContent))
	}
}

// TestPromptWithDefault_Empty verifies the default is used when input is empty.
func TestPromptWithDefault_Empty(t *testing.T) {
	t.Parallel()
	r := strings.NewReader("\n") // simulates user pressing Enter
	got, err := cli.PromptWithDefault(newBufReader(r), "question: ", "mydefault")
	if err != nil {
		t.Fatalf("PromptWithDefault error: %v", err)
	}
	if got != "mydefault" {
		t.Errorf("PromptWithDefault = %q, want %q", got, "mydefault")
	}
}

// TestPromptRequired_Empty verifies an error is returned when input is empty.
func TestPromptRequired_Empty(t *testing.T) {
	t.Parallel()
	r := strings.NewReader("\n")
	_, err := cli.PromptRequired(newBufReader(r), "question: ", "must not be empty")
	if err == nil {
		t.Fatal("PromptRequired: expected error on empty input, got nil")
	}
	if !strings.Contains(err.Error(), "must not be empty") {
		t.Errorf("PromptRequired error = %q, want substring 'must not be empty'", err.Error())
	}
}

// TestCollectDBConfig_Postgres verifies Postgres branch returns correct config.
func TestCollectDBConfig_Postgres(t *testing.T) {
	t.Parallel()
	// Simulate: choose "2" then enter the DSN.
	input := "2\n" + fakePostgresDSNShort + "\n"
	r := newBufReader(strings.NewReader(input))
	got, err := cli.CollectDBConfig(r)
	if err != nil {
		t.Fatalf("CollectDBConfig error: %v", err)
	}
	if got.StorageBackend != "postgres" {
		t.Errorf("StorageBackend = %q, want %q", got.StorageBackend, "postgres")
	}
	if got.DatabaseURL != fakePostgresDSNShort {
		t.Errorf("DatabaseURL = %q, want %q", got.DatabaseURL, fakePostgresDSNShort)
	}
}

// TestCollectDBConfig_SQLiteDefault verifies SQLite is selected when user presses Enter.
func TestCollectDBConfig_SQLiteDefault(t *testing.T) {
	t.Parallel()
	// Simulate: choose "" (default SQLite) then default path.
	input := "\n\n"
	r := newBufReader(strings.NewReader(input))
	got, err := cli.CollectDBConfig(r)
	if err != nil {
		t.Fatalf("CollectDBConfig error: %v", err)
	}
	if got.StorageBackend != sqliteBackend {
		t.Errorf("StorageBackend = %q, want %q", got.StorageBackend, sqliteBackend)
	}
	if got.SQLitePath == "" {
		t.Error("SQLitePath should not be empty for sqlite backend")
	}
}

// TestCollectDBConfig_PostgresEmptyDSN verifies an error for blank Postgres DSN.
func TestCollectDBConfig_PostgresEmptyDSN(t *testing.T) {
	t.Parallel()
	input := "2\n\n" // choose Postgres, then empty DSN
	r := newBufReader(strings.NewReader(input))
	_, err := cli.CollectDBConfig(r)
	if err == nil {
		t.Fatal("CollectDBConfig: expected error for empty Postgres DSN, got nil")
	}
}

// newBufReader is a test helper to create a *bufio.Reader from an io.Reader.
func newBufReader(r io.Reader) *bufio.Reader {
	return bufio.NewReader(r)
}

// sqliteBackend is used in multiple test cases; named constant avoids goconst.
const sqliteBackend = "sqlite"

// setXDGConfigHome overrides XDG_CONFIG_HOME (used by os.UserConfigDir on
// Linux) and HOME (used as fallback on macOS) to point at a temp dir.
// It restores both on test cleanup.
func setXDGConfigHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir) // macOS fallback: os.UserConfigDir → $HOME/.config
}

// TestConfigPath_UserConfigDir verifies ConfigPath() returns a path inside the
// OS user-config directory with the expected sub-path and that the directory
// is created automatically.
func TestConfigPath_UserConfigDir(t *testing.T) {
	// Override XDG_CONFIG_HOME so we don't pollute the real config directory.
	tmp := t.TempDir()
	setXDGConfigHome(t, tmp)

	got, err := cli.ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath() error: %v", err)
	}
	// Path must end with wayneblacktea/config.yaml
	if !strings.HasSuffix(got, filepath.Join("wayneblacktea", "config.yaml")) {
		t.Errorf("ConfigPath() = %q; want suffix %q", got, filepath.Join("wayneblacktea", "config.yaml"))
	}
	// The parent directory must exist after the call.
	dir := filepath.Dir(got)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Errorf("ConfigPath() did not create directory %q", dir)
	}
}

// TestConfigPath_UserConfigDir_AlreadyExists verifies ConfigPath() is
// idempotent when the directory already exists (no EEXIST error).
func TestConfigPath_UserConfigDir_AlreadyExists(t *testing.T) {
	tmp := t.TempDir()
	setXDGConfigHome(t, tmp)

	// Call twice; second call must not error.
	if _, err := cli.ConfigPath(); err != nil {
		t.Fatalf("first ConfigPath() error: %v", err)
	}
	if _, err := cli.ConfigPath(); err != nil {
		t.Fatalf("second ConfigPath() error: %v", err)
	}
}

// TestWriteGlobalConfig_RoundTrip verifies that WriteGlobalConfig writes a
// valid YAML file that can be parsed back by LoadGlobalConfig.
func TestWriteGlobalConfig_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	setXDGConfigHome(t, tmp)

	cfg := cli.WbtConfig{
		APIKey:         "testapikey",
		DatabaseURL:    fakePostgresDSN,
		Port:           "9090",
		StorageBackend: "postgres",
	}
	if err := cli.WriteGlobalConfig(cfg); err != nil {
		t.Fatalf("WriteGlobalConfig error: %v", err)
	}

	// Verify file permissions: must be 0600 (credential-bearing file).
	p, _ := cli.ConfigPath()
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat config file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config file mode = %o, want 0600", perm)
	}

	// Parse back and verify fields.
	b, err := os.ReadFile(p) //nolint:gosec // G304: p is derived from ConfigPath() pointing at a test-controlled temp dir
	if err != nil {
		t.Fatalf("reading config file: %v", err)
	}
	var got cli.WbtConfig
	if err := yaml.Unmarshal(b, &got); err != nil {
		t.Fatalf("yaml.Unmarshal error: %v", err)
	}
	if got.APIKey != cfg.APIKey {
		t.Errorf("api_key = %q, want %q", got.APIKey, cfg.APIKey)
	}
	if got.Port != cfg.Port {
		t.Errorf("port = %q, want %q", got.Port, cfg.Port)
	}
	if got.DatabaseURL != cfg.DatabaseURL {
		t.Errorf("database_url = %q, want %q", got.DatabaseURL, cfg.DatabaseURL)
	}
}

// TestRunInit_WritesConfigFile verifies that RunInit writes the global config
// file at ~/.config/wayneblacktea/config.yaml in addition to .env.
// Cannot use t.Parallel because RunInit uses os.Stdin/os.Stdout globals and
// t.Chdir.
func TestRunInit_WritesConfigFile(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	setXDGConfigHome(t, tmp)

	oldStdin := os.Stdin
	oldStdout := os.Stdout
	defer func() {
		os.Stdin = oldStdin
		os.Stdout = oldStdout
	}()

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe stdin: %v", err)
	}
	_, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe stdout: %v", err)
	}
	os.Stdin = stdinR
	os.Stdout = stdoutW

	// Inputs: default SQLite, default path, port=8080, empty (generate key).
	_, _ = stdinW.WriteString("\n\n8080\n\n")
	_ = stdinW.Close()

	if err := cli.RunInit(); err != nil {
		_ = stdoutW.Close()
		t.Fatalf("RunInit error: %v", err)
	}
	_ = stdoutW.Close()

	// Verify the global config file exists.
	cfgPath, err := cli.ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath error: %v", err)
	}
	b, err := os.ReadFile(cfgPath) //nolint:gosec // G304: cfgPath is derived from ConfigPath() pointing at a test-controlled temp dir
	if err != nil {
		t.Fatalf("global config not created at %s: %v", cfgPath, err)
	}
	var cfg cli.WbtConfig
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("parsing global config: %v", err)
	}
	if cfg.APIKey == "" {
		t.Error("global config api_key must not be empty after wbt init")
	}
	if cfg.Port != "8080" {
		t.Errorf("global config port = %q, want 8080", cfg.Port)
	}
	if cfg.StorageBackend != sqliteBackend {
		t.Errorf("global config storage_backend = %q, want %q", cfg.StorageBackend, sqliteBackend)
	}
}

// TestRunServe_LoadsGlobalConfig verifies that RunServe reads API_KEY from
// ~/.config/wayneblacktea/config.yaml when the env var is not set and .env
// does not exist. The binary lookup will fail, but the config loading path
// (which happens before the binary lookup) must succeed.
// Cannot use t.Parallel because t.Setenv mutates process-global state.
func TestRunServe_LoadsGlobalConfig(t *testing.T) {
	tmp := t.TempDir()
	setXDGConfigHome(t, tmp)

	// Clear API_KEY from the environment so LoadGlobalConfig must supply it.
	t.Setenv("API_KEY", "")

	// Write a global config with an API key.
	cfg := cli.WbtConfig{
		APIKey: "globalconfigkey",
		Port:   "8080",
	}
	if err := cli.WriteGlobalConfig(cfg); err != nil {
		t.Fatalf("WriteGlobalConfig: %v", err)
	}

	// Override PATH so wayneblacktea-server is not found — ensures the test
	// doesn't accidentally start a real server.
	emptyDir := t.TempDir()
	t.Setenv("PATH", emptyDir)

	err := cli.RunServe([]string{})
	// We expect "not found in PATH" error — NOT "API_KEY must be set".
	if err == nil {
		t.Fatal("RunServe: expected error (binary not in PATH), got nil")
	}
	if strings.Contains(err.Error(), "API_KEY must be set") {
		t.Errorf("RunServe should have loaded API_KEY from global config, got: %v", err)
	}
	if !strings.Contains(err.Error(), "wayneblacktea-server not found") {
		t.Errorf("RunServe error = %q; want 'wayneblacktea-server not found'", err.Error())
	}
}

// TestRunServe_GlobalConfigFallsBackToDotEnv verifies that when global config
// is absent, .env values are still loaded (backward compat).
// Cannot use t.Parallel because t.Setenv mutates process-global state.
func TestRunServe_GlobalConfigFallsBackToDotEnv(t *testing.T) {
	// Point XDG_CONFIG_HOME at an empty dir so no global config file exists.
	empty := t.TempDir()
	setXDGConfigHome(t, empty)
	t.Setenv("API_KEY", "")

	// Create a .env file in CWD.
	cwd := t.TempDir()
	t.Chdir(cwd)
	dotEnv := filepath.Join(cwd, ".env")
	if err := os.WriteFile(dotEnv, []byte("API_KEY=envfilekey\n"), 0o600); err != nil {
		t.Fatalf("writing .env: %v", err)
	}

	emptyDir := t.TempDir()
	t.Setenv("PATH", emptyDir)

	err := cli.RunServe([]string{})
	// Should reach "binary not found", not "API_KEY must be set".
	if err == nil {
		t.Fatal("RunServe: expected error, got nil")
	}
	if strings.Contains(err.Error(), "API_KEY must be set") {
		t.Errorf("RunServe should have loaded API_KEY from .env fallback, got: %v", err)
	}
}

// TestLoadGlobalConfig_MissingFile verifies LoadGlobalConfig is a no-op when
// the config file does not exist.
func TestLoadGlobalConfig_MissingFile(t *testing.T) {
	tmp := t.TempDir()
	setXDGConfigHome(t, tmp)
	t.Setenv("API_KEY", "")

	if err := cli.LoadGlobalConfig(); err != nil {
		t.Fatalf("LoadGlobalConfig: unexpected error for missing file: %v", err)
	}
	// API_KEY must remain unset.
	if v := os.Getenv("API_KEY"); v != "" {
		t.Errorf("API_KEY = %q after LoadGlobalConfig on missing file; want empty", v)
	}
}

// TestLoadGlobalConfig_DoesNotOverrideExisting verifies that SetIfAbsent does
// not clobber an env var that was already set before LoadGlobalConfig runs.
func TestLoadGlobalConfig_DoesNotOverrideExisting(t *testing.T) {
	tmp := t.TempDir()
	setXDGConfigHome(t, tmp)

	// Pre-set the env var.
	t.Setenv("API_KEY", "presharedkey")

	// Write a global config with a different key.
	if err := cli.WriteGlobalConfig(cli.WbtConfig{APIKey: "configkey"}); err != nil {
		t.Fatalf("WriteGlobalConfig: %v", err)
	}
	if err := cli.LoadGlobalConfig(); err != nil {
		t.Fatalf("LoadGlobalConfig: %v", err)
	}
	if got := os.Getenv("API_KEY"); got != "presharedkey" {
		t.Errorf("API_KEY = %q; want presharedkey (existing value should not be overridden)", got)
	}
}

// TestSetIfAbsent exercises the three branches: empty value, already set, and unset.
func TestSetIfAbsent(t *testing.T) {
	// Case 1: value is empty — no change even if env is unset.
	t.Setenv("WBT_TEST_KEY1", "")
	cli.SetIfAbsent("WBT_TEST_KEY1", "")
	if got := os.Getenv("WBT_TEST_KEY1"); got != "" {
		t.Errorf("SetIfAbsent with empty value: WBT_TEST_KEY1 = %q, want empty", got)
	}

	// Case 2: env var already has a value — must not overwrite.
	t.Setenv("WBT_TEST_KEY2", "existing")
	cli.SetIfAbsent("WBT_TEST_KEY2", "newvalue")
	if got := os.Getenv("WBT_TEST_KEY2"); got != "existing" {
		t.Errorf("SetIfAbsent should not overwrite: WBT_TEST_KEY2 = %q, want existing", got)
	}

	// Case 3: env var is unset — must set it.
	t.Setenv("WBT_TEST_KEY3", "")
	cli.SetIfAbsent("WBT_TEST_KEY3", "injected")
	if got := os.Getenv("WBT_TEST_KEY3"); got != "injected" {
		t.Errorf("SetIfAbsent on unset key: WBT_TEST_KEY3 = %q, want injected", got)
	}
}

// TestDbConfigToWbtConfig verifies the struct conversion covers all fields.
func TestDbConfigToWbtConfig(t *testing.T) {
	t.Parallel()
	db := cli.DBConfig{StorageBackend: sqliteBackend, SQLitePath: "/tmp/test.db"}
	got := cli.DBConfigToWbtConfig("mykey", "9090", db)
	if got.APIKey != "mykey" {
		t.Errorf("APIKey = %q, want mykey", got.APIKey)
	}
	if got.Port != "9090" {
		t.Errorf("Port = %q, want 9090", got.Port)
	}
	if got.StorageBackend != sqliteBackend {
		t.Errorf("StorageBackend = %q, want %q", got.StorageBackend, sqliteBackend)
	}
	if got.SQLitePath != "/tmp/test.db" {
		t.Errorf("SQLitePath = %q, want /tmp/test.db", got.SQLitePath)
	}
}

// TestValidateGuardBypassFlags exercises every accept/reject branch of the
// client-side bypass flag validator. Done as a single table-driven test so
// future contributors can see the full matrix at a glance.
func TestValidateGuardBypassFlags(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		scope     string
		target    string
		reason    string
		iUnderG   bool
		wantErr   bool
		wantInErr string
	}{
		// Happy paths.
		{name: "repo scope", scope: "repo", target: "wayneblacktea", reason: "trial", wantErr: false},
		{name: "file scope abs", scope: "file", target: "/Users/me/repo/foo.go", reason: "trial", wantErr: false},
		{name: "dir scope abs", scope: "dir", target: "/Users/me/repo", reason: "trial", wantErr: false},
		{
			name:  "global scope with confirmation",
			scope: "global", target: "global", reason: "trial", iUnderG: true,
			wantErr: false,
		},

		// Reject: missing.
		{name: "missing scope", scope: "", target: "x", reason: "r", wantErr: true, wantInErr: "--scope is required"},
		{name: "missing target", scope: "repo", target: "", reason: "r", wantErr: true, wantInErr: "--target is required"},
		{
			name:  "missing reason",
			scope: "repo", target: "x", reason: "",
			wantErr: true, wantInErr: "--reason is required",
		},
		{
			name:  "whitespace reason",
			scope: "repo", target: "x", reason: "   \t",
			wantErr: true, wantInErr: "--reason is required",
		},

		// Reject: invalid scope value.
		{name: "unknown scope", scope: "team", target: "x", reason: "r", wantErr: true, wantInErr: "invalid"},

		// Reject: global without confirmation / wrong target.
		{
			name:  "global without confirmation",
			scope: "global", target: "global", reason: "r", iUnderG: false,
			wantErr: true, wantInErr: "i-understand-this-is-global",
		},
		{
			name:  "global with non-literal target",
			scope: "global", target: "everything", reason: "r", iUnderG: true,
			wantErr: true, wantInErr: "literal",
		},

		// Reject: file/dir with relative path.
		{
			name:  "file relative path",
			scope: "file", target: "foo.go", reason: "r",
			wantErr: true, wantInErr: "absolute",
		},
		{
			name:  "dir relative path",
			scope: "dir", target: "./sub", reason: "r",
			wantErr: true, wantInErr: "absolute",
		},

		// Reject: overly-broad targets.
		{
			name:  "dir target /",
			scope: "dir", target: "/", reason: "r",
			wantErr: true, wantInErr: "too broadly",
		},
		{
			name:  "dir target /home",
			scope: "dir", target: "/home", reason: "r",
			wantErr: true, wantInErr: "too broadly",
		},
		{
			name:  "dir target /Users",
			scope: "dir", target: "/Users", reason: "r",
			wantErr: true, wantInErr: "too broadly",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := cli.ValidateGuardBypassFlags(tc.scope, tc.target, tc.reason, tc.iUnderG)
			if tc.wantErr && err == nil {
				t.Fatalf("ValidateGuardBypassFlags(%q,%q) expected error, got nil", tc.scope, tc.target)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidateGuardBypassFlags(%q,%q) unexpected error: %v", tc.scope, tc.target, err)
			}
			if tc.wantErr && tc.wantInErr != "" && !strings.Contains(err.Error(), tc.wantInErr) {
				t.Errorf("ValidateGuardBypassFlags error %q missing substring %q", err.Error(), tc.wantInErr)
			}
		})
	}
}
