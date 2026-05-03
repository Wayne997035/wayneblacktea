package main

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fake DSNs used as test fixtures. Centralised so gosec G101 only needs to be
// silenced once and individual call sites stay within the line-length budget.
const (
	fakePostgresDSN      = "postgres://user:pass@host/db" //nolint:gosec // G101: test fixture
	fakePostgresDSNShort = "postgres://u:p@host/db"       //nolint:gosec // G101: test fixture
)

// TestBuildEnvFile verifies .env generation for SQLite and Postgres configs.
func TestBuildEnvFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		apiKey    string
		port      string
		db        dbConfig
		wantLines []string
		noLines   []string
	}{
		{
			name:   "sqlite config",
			apiKey: "myapikey123",
			port:   "8080",
			db:     dbConfig{storageBackend: "sqlite", sqlitePath: "/home/user/.wayneblacktea/data.db"},
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
			db:     dbConfig{storageBackend: "postgres", databaseURL: fakePostgresDSN},
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
			db:        dbConfig{storageBackend: "sqlite", sqlitePath: "./data.db"},
			wantLines: []string{"STORAGE_BACKEND=sqlite"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := buildEnvFile(tc.apiKey, tc.port, tc.db)
			for _, want := range tc.wantLines {
				if !strings.Contains(got, want) {
					t.Errorf("buildEnvFile output missing %q; got:\n%s", want, got)
				}
			}
			for _, deny := range tc.noLines {
				if strings.Contains(got, deny) {
					t.Errorf("buildEnvFile output should not contain %q; got:\n%s", deny, got)
				}
			}
		})
	}
}

// TestBuildEnvFile_NoURLForSQLite verifies DATABASE_URL is absent for sqlite backend.
func TestBuildEnvFile_NoURLForSQLite(t *testing.T) {
	t.Parallel()
	got := buildEnvFile("k", "8080", dbConfig{storageBackend: "sqlite", sqlitePath: "./data.db"})
	if strings.Contains(got, "DATABASE_URL") {
		t.Errorf("expected no DATABASE_URL in sqlite env, got:\n%s", got)
	}
}

// TestBuildMCPJSON verifies .mcp.json generation for both backends.
func TestBuildMCPJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		db       dbConfig
		wantKeys []string
		noKeys   []string
	}{
		{
			name: "sqlite backend",
			db:   dbConfig{storageBackend: "sqlite", sqlitePath: "/tmp/test.db"},
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
			db:   dbConfig{storageBackend: "postgres", databaseURL: fakePostgresDSNShort},
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
			b, err := buildMCPJSON(tc.db)
			if err != nil {
				t.Fatalf("buildMCPJSON returned error: %v", err)
			}
			got := string(b)
			for _, key := range tc.wantKeys {
				if !strings.Contains(got, key) {
					t.Errorf("buildMCPJSON missing %q in output:\n%s", key, got)
				}
			}
			for _, key := range tc.noKeys {
				if strings.Contains(got, key) {
					t.Errorf("buildMCPJSON should not contain %q in output:\n%s", key, got)
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
			got, err := randomHex(tc.n)
			if err != nil {
				t.Fatalf("randomHex(%d) error: %v", tc.n, err)
			}
			if len(got) != tc.wantLen {
				t.Errorf("randomHex(%d) len = %d, want %d", tc.n, len(got), tc.wantLen)
			}
			// Verify all chars are valid hex.
			for _, c := range got {
				if !strings.ContainsRune("0123456789abcdef", c) {
					t.Errorf("randomHex produced non-hex char %q in %q", c, got)
				}
			}
		})
	}
}

// TestRandomHex_Uniqueness verifies two calls produce different values.
func TestRandomHex_Uniqueness(t *testing.T) {
	t.Parallel()
	a, err := randomHex(32)
	if err != nil {
		t.Fatalf("randomHex error: %v", err)
	}
	b, err := randomHex(32)
	if err != nil {
		t.Fatalf("randomHex error: %v", err)
	}
	if a == b {
		t.Error("randomHex produced identical values on two calls")
	}
}

// TestWriteEnvLine_QuotesSpaces verifies values with spaces are quoted.
func TestWriteEnvLine_QuotesSpaces(t *testing.T) {
	t.Parallel()
	var sb strings.Builder
	writeEnvLine(&sb, "FOO", "hello world")
	got := sb.String()
	if !strings.Contains(got, `"hello world"`) {
		t.Errorf("writeEnvLine should quote value with space, got: %q", got)
	}
}

// TestWriteEnvLine_NoQuoteNeeded verifies plain values are not quoted.
func TestWriteEnvLine_NoQuoteNeeded(t *testing.T) {
	t.Parallel()
	var sb strings.Builder
	writeEnvLine(&sb, "KEY", "plainvalue")
	got := sb.String()
	if got != "KEY=plainvalue\n" {
		t.Errorf("writeEnvLine got %q, want %q", got, "KEY=plainvalue\n")
	}
}

// TestRunServe_MissingBinary verifies runServe returns an error when
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

	err := runServe()
	if err == nil {
		t.Fatal("runServe: expected error when binary not in PATH, got nil")
	}
	if !strings.Contains(err.Error(), "wayneblacktea-server not found") {
		t.Errorf("runServe error = %q, want substring 'wayneblacktea-server not found'", err.Error())
	}
}

// TestRunServe_MissingEnvVars verifies runServe returns an error when API_KEY
// is not set. CLAUDE_API_KEY is optional and must not satisfy HTTP auth config.
// Cannot use t.Parallel because t.Setenv mutates process-global state.
func TestRunServe_MissingEnvVars(t *testing.T) {
	t.Setenv("API_KEY", "")
	t.Setenv("CLAUDE_API_KEY", "")

	err := runServe()
	if err == nil {
		t.Fatal("runServe: expected error when env vars missing, got nil")
	}
	if !strings.Contains(err.Error(), "API_KEY must be set") {
		t.Errorf("runServe error = %q, want substring about API_KEY", err.Error())
	}
}

// TestRunInit_DoesNotPromptForClaudeAPIKey proves the init wizard can complete
// with only database, port, and HTTP API key answers. CLAUDE_API_KEY remains an
// optional feature flag the user may add later.
func TestRunInit_DoesNotPromptForClaudeAPIKey(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

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

	err = runInit()
	_ = stdoutW.Close()
	if err != nil {
		t.Fatalf("runInit returned error: %v", err)
	}

	out, err := io.ReadAll(stdoutR)
	if err != nil {
		t.Fatalf("reading stdout: %v", err)
	}
	if strings.Contains(string(out), "API key for Claude") {
		t.Fatalf("runInit prompted for CLAUDE_API_KEY:\n%s", string(out))
	}
	// Phase 1-4 of docs/openrouter-fallback.md: the optional OpenRouter
	// provider MUST NOT be prompted in the default install flow either.
	if strings.Contains(string(out), "OpenRouter") || strings.Contains(string(out), "OPENROUTER") {
		t.Fatalf("runInit prompted for OPENROUTER_API_KEY:\n%s", string(out))
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
	got, err := promptWithDefault(newBufReader(r), "question: ", "mydefault")
	if err != nil {
		t.Fatalf("promptWithDefault error: %v", err)
	}
	if got != "mydefault" {
		t.Errorf("promptWithDefault = %q, want %q", got, "mydefault")
	}
}

// TestPromptRequired_Empty verifies an error is returned when input is empty.
func TestPromptRequired_Empty(t *testing.T) {
	t.Parallel()
	r := strings.NewReader("\n")
	_, err := promptRequired(newBufReader(r), "question: ", "must not be empty")
	if err == nil {
		t.Fatal("promptRequired: expected error on empty input, got nil")
	}
	if !strings.Contains(err.Error(), "must not be empty") {
		t.Errorf("promptRequired error = %q, want substring 'must not be empty'", err.Error())
	}
}

// TestCollectDBConfig_Postgres verifies Postgres branch returns correct config.
func TestCollectDBConfig_Postgres(t *testing.T) {
	t.Parallel()
	// Simulate: choose "2" then enter the DSN.
	input := "2\n" + fakePostgresDSNShort + "\n"
	r := newBufReader(strings.NewReader(input))
	got, err := collectDBConfig(r)
	if err != nil {
		t.Fatalf("collectDBConfig error: %v", err)
	}
	if got.storageBackend != "postgres" {
		t.Errorf("storageBackend = %q, want %q", got.storageBackend, "postgres")
	}
	if got.databaseURL != fakePostgresDSNShort {
		t.Errorf("databaseURL = %q, want %q", got.databaseURL, fakePostgresDSNShort)
	}
}

// TestCollectDBConfig_SQLiteDefault verifies SQLite is selected when user presses Enter.
func TestCollectDBConfig_SQLiteDefault(t *testing.T) {
	t.Parallel()
	// Simulate: choose "" (default SQLite) then default path.
	input := "\n\n"
	r := newBufReader(strings.NewReader(input))
	got, err := collectDBConfig(r)
	if err != nil {
		t.Fatalf("collectDBConfig error: %v", err)
	}
	if got.storageBackend != "sqlite" {
		t.Errorf("storageBackend = %q, want %q", got.storageBackend, "sqlite")
	}
	if got.sqlitePath == "" {
		t.Error("sqlitePath should not be empty for sqlite backend")
	}
}

// TestCollectDBConfig_PostgresEmptyDSN verifies an error for blank Postgres DSN.
func TestCollectDBConfig_PostgresEmptyDSN(t *testing.T) {
	t.Parallel()
	input := "2\n\n" // choose Postgres, then empty DSN
	r := newBufReader(strings.NewReader(input))
	_, err := collectDBConfig(r)
	if err == nil {
		t.Fatal("collectDBConfig: expected error for empty Postgres DSN, got nil")
	}
}

// newBufReader is a test helper to create a *bufio.Reader from an io.Reader.
func newBufReader(r io.Reader) *bufio.Reader {
	return bufio.NewReader(r)
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
			err := validateGuardBypassFlags(tc.scope, tc.target, tc.reason, tc.iUnderG)
			if tc.wantErr && err == nil {
				t.Fatalf("validateGuardBypassFlags(%q,%q) expected error, got nil", tc.scope, tc.target)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateGuardBypassFlags(%q,%q) unexpected error: %v", tc.scope, tc.target, err)
			}
			if tc.wantErr && tc.wantInErr != "" && !strings.Contains(err.Error(), tc.wantInErr) {
				t.Errorf("validateGuardBypassFlags error %q missing substring %q", err.Error(), tc.wantInErr)
			}
		})
	}
}
