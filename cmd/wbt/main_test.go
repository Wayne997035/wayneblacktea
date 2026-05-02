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
		claudeKey string
		apiKey    string
		port      string
		db        dbConfig
		wantLines []string
	}{
		{
			name:      "sqlite config",
			claudeKey: "sk-claude-test",
			apiKey:    "myapikey123",
			port:      "8080",
			db:        dbConfig{storageBackend: "sqlite", sqlitePath: "/home/user/.wayneblacktea/data.db"},
			wantLines: []string{
				"CLAUDE_API_KEY=sk-claude-test",
				"API_KEY=myapikey123",
				"PORT=8080",
				"STORAGE_BACKEND=sqlite",
				"SQLITE_PATH=/home/user/.wayneblacktea/data.db",
			},
		},
		{
			name:      "postgres config",
			claudeKey: "sk-claude-prod",
			apiKey:    "prodapikey456",
			port:      "9090",
			db:        dbConfig{storageBackend: "postgres", databaseURL: fakePostgresDSN},
			wantLines: []string{
				"CLAUDE_API_KEY=sk-claude-prod",
				"API_KEY=prodapikey456",
				"PORT=9090",
				"STORAGE_BACKEND=postgres",
				"DATABASE_URL=" + fakePostgresDSN,
			},
		},
		{
			name:      "sqlite config omits DATABASE_URL",
			claudeKey: "sk-test",
			apiKey:    "akey",
			port:      "8080",
			db:        dbConfig{storageBackend: "sqlite", sqlitePath: "./data.db"},
			wantLines: []string{"STORAGE_BACKEND=sqlite"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := buildEnvFile(tc.claudeKey, tc.apiKey, tc.port, tc.db)
			for _, want := range tc.wantLines {
				if !strings.Contains(got, want) {
					t.Errorf("buildEnvFile output missing %q; got:\n%s", want, got)
				}
			}
		})
	}
}

// TestBuildEnvFile_NoURLForSQLite verifies DATABASE_URL is absent for sqlite backend.
func TestBuildEnvFile_NoURLForSQLite(t *testing.T) {
	t.Parallel()
	got := buildEnvFile("k", "k", "8080", dbConfig{storageBackend: "sqlite", sqlitePath: "./data.db"})
	if strings.Contains(got, "DATABASE_URL") {
		t.Errorf("expected no DATABASE_URL in sqlite env, got:\n%s", got)
	}
}

// TestBuildMCPJSON verifies .mcp.json generation for both backends.
func TestBuildMCPJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		claudeKey string
		db        dbConfig
		wantKeys  []string
		noKeys    []string
	}{
		{
			name:      "sqlite backend",
			claudeKey: "sk-test",
			db:        dbConfig{storageBackend: "sqlite", sqlitePath: "/tmp/test.db"},
			wantKeys: []string{
				"wayneblacktea",
				`"command": "wbt"`,
				`"args": [`,
				`"mcp"`,
				"CLAUDE_API_KEY",
				"SQLITE_PATH",
				"STORAGE_BACKEND",
			},
			// Regression guard: legacy binary name must not appear — `go install
			// .../cmd/wbt@latest` does not produce a `wayneblacktea-mcp` binary,
			// so the .mcp.json must point at `wbt mcp` instead.
			noKeys: []string{"DATABASE_URL", "wayneblacktea-mcp"},
		},
		{
			name:      "postgres backend",
			claudeKey: "sk-prod",
			db:        dbConfig{storageBackend: "postgres", databaseURL: fakePostgresDSNShort},
			wantKeys: []string{
				"wayneblacktea",
				`"command": "wbt"`,
				`"args": [`,
				`"mcp"`,
				"CLAUDE_API_KEY",
				"DATABASE_URL",
				"STORAGE_BACKEND",
			},
			noKeys: []string{"SQLITE_PATH", "wayneblacktea-mcp"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b, err := buildMCPJSON(tc.claudeKey, tc.db)
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

// TestRunServe_MissingEnvVars verifies runServe returns an error when neither
// API_KEY nor CLAUDE_API_KEY is set.
// Cannot use t.Parallel because t.Setenv mutates process-global state.
func TestRunServe_MissingEnvVars(t *testing.T) {
	t.Setenv("API_KEY", "")
	t.Setenv("CLAUDE_API_KEY", "")

	err := runServe()
	if err == nil {
		t.Fatal("runServe: expected error when env vars missing, got nil")
	}
	if !strings.Contains(err.Error(), "API_KEY or CLAUDE_API_KEY must be set") {
		t.Errorf("runServe error = %q, want substring about API_KEY/CLAUDE_API_KEY", err.Error())
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
