package cli_test

import (
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/cli"
)

// fakePostgresDSNEnv is a test fixture DSN. Defined as a constant so the
// single //nolint suppresses gosec G101 for all uses in this file.
const fakePostgresDSNEnv = "postgres://user:pass@host/db" //nolint:gosec // G101: test fixture

// TestGenerateEnvFile verifies .env generation for both SQLite and Postgres configs.
func TestGenerateEnvFile(t *testing.T) {
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
			name:   "sqlite config contains required keys",
			apiKey: "testapikey",
			port:   "8080",
			db:     cli.DBConfig{StorageBackend: "sqlite", SQLitePath: "/home/user/.wayneblacktea/data.db"},
			wantLines: []string{
				"API_KEY=testapikey",
				"PORT=8080",
				"STORAGE_BACKEND=sqlite",
				"SQLITE_PATH=/home/user/.wayneblacktea/data.db",
				"ALLOWED_ORIGINS=http://localhost:8080,http://127.0.0.1:8080",
			},
			// CLAUDE_API_KEY must not appear as an active assignment.
			noLines: []string{"\nCLAUDE_API_KEY="},
		},
		{
			name:   "postgres config contains DATABASE_URL",
			apiKey: "pgapikey",
			port:   "9090",
			db: cli.DBConfig{
				StorageBackend: "postgres",
				DatabaseURL:    fakePostgresDSNEnv,
			},
			wantLines: []string{
				"API_KEY=pgapikey",
				"PORT=9090",
				"STORAGE_BACKEND=postgres",
				"DATABASE_URL=" + fakePostgresDSNEnv,
				"ALLOWED_ORIGINS=http://localhost:9090,http://127.0.0.1:9090",
			},
			noLines: []string{"SQLITE_PATH", "\nCLAUDE_API_KEY="},
		},
		{
			name:   "sqlite config omits DATABASE_URL",
			apiKey: "akey",
			port:   "8080",
			db:     cli.DBConfig{StorageBackend: "sqlite", SQLitePath: "./data.db"},
			wantLines: []string{
				"STORAGE_BACKEND=sqlite",
			},
			noLines: []string{"DATABASE_URL"},
		},
		{
			name:   "api key with special chars is quoted",
			apiKey: "key#with#hashes",
			port:   "8080",
			db:     cli.DBConfig{StorageBackend: "sqlite", SQLitePath: "/tmp/x.db"},
			// writeEnvLine should quote when '#' is present.
			wantLines: []string{`API_KEY="key#with#hashes"`},
		},
		{
			name:   "generated env contains optional ai key comments",
			apiKey: "k",
			port:   "8080",
			db:     cli.DBConfig{StorageBackend: "sqlite", SQLitePath: "/tmp/x.db"},
			// Optional keys must appear only as comments.
			wantLines: []string{"# CLAUDE_API_KEY=", "# GEMINI_API_KEY="},
			noLines:   []string{"\nCLAUDE_API_KEY=", "\nGEMINI_API_KEY="},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := cli.GenerateEnvFile(tc.apiKey, tc.port, tc.db)
			for _, want := range tc.wantLines {
				if !strings.Contains(got, want) {
					t.Errorf("GenerateEnvFile missing %q; got:\n%s", want, got)
				}
			}
			for _, deny := range tc.noLines {
				if strings.Contains(got, deny) {
					t.Errorf("GenerateEnvFile must not contain %q; got:\n%s", deny, got)
				}
			}
		})
	}
}

// TestResolveAllowedOrigins covers all branches of the CORS origin resolution logic.
func TestResolveAllowedOrigins(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		allowedOrigins string
		appEnv         string
		port           string
		wantResult     string
		wantErr        bool
	}{
		{
			name:           "wildcard always errors regardless of APP_ENV",
			allowedOrigins: "*",
			appEnv:         "",
			port:           "8080",
			wantErr:        true,
		},
		{
			name:           "wildcard errors even in production",
			allowedOrigins: "*",
			appEnv:         "production",
			port:           "8080",
			wantErr:        true,
		},
		{
			name:           "production with empty origins errors",
			allowedOrigins: "",
			appEnv:         "production",
			port:           "8080",
			wantErr:        true,
		},
		{
			name:           "local dev empty origins defaults to localhost",
			allowedOrigins: "",
			appEnv:         "",
			port:           "8080",
			wantResult:     "http://localhost:8080,http://127.0.0.1:8080",
			wantErr:        false,
		},
		{
			name:           "non-production APP_ENV defaults to localhost on given port",
			allowedOrigins: "",
			appEnv:         "development",
			port:           "3000",
			wantResult:     "http://localhost:3000,http://127.0.0.1:3000",
			wantErr:        false,
		},
		{
			name:           "explicit value returned as-is in production",
			allowedOrigins: "https://app.example.com",
			appEnv:         "production",
			port:           "8080",
			wantResult:     "https://app.example.com",
			wantErr:        false,
		},
		{
			name:           "explicit multi-origin returned as-is",
			allowedOrigins: "https://app.example.com,https://staging.example.com",
			appEnv:         "",
			port:           "8080",
			wantResult:     "https://app.example.com,https://staging.example.com",
			wantErr:        false,
		},
		{
			name:           "whitespace-only treated as empty in local mode",
			allowedOrigins: "   ",
			appEnv:         "",
			port:           "9090",
			wantResult:     "http://localhost:9090,http://127.0.0.1:9090",
			wantErr:        false,
		},
		{
			name:           "whitespace-only treated as empty in production",
			allowedOrigins: "   ",
			appEnv:         "production",
			port:           "8080",
			wantErr:        true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := cli.ResolveAllowedOrigins(tc.allowedOrigins, tc.appEnv, tc.port)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ResolveAllowedOrigins: expected error, got nil (result=%q)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveAllowedOrigins: unexpected error: %v", err)
			}
			if got != tc.wantResult {
				t.Errorf("ResolveAllowedOrigins = %q, want %q", got, tc.wantResult)
			}
		})
	}
}

// TestRandomHex verifies output is valid hex with the correct length.
func TestRandomHex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		n       int
		wantLen int
	}{
		{name: "32 bytes produces 64 hex chars", n: 32, wantLen: 64},
		{name: "16 bytes produces 32 hex chars", n: 16, wantLen: 32},
		{name: "1 byte produces 2 hex chars", n: 1, wantLen: 2},
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
		t.Fatalf("first RandomHex error: %v", err)
	}
	b, err := cli.RandomHex(32)
	if err != nil {
		t.Fatalf("second RandomHex error: %v", err)
	}
	if a == b {
		t.Error("RandomHex produced identical values on two consecutive calls")
	}
}
