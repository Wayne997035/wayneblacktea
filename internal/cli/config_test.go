package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/cli"
	"gopkg.in/yaml.v3"
)

// fakePostgresDSN is used as a test fixture. Defined as a constant so the
// single //nolint suppresses all uses rather than annotating every call site.
const fakePostgresDSN = "postgres://u:p@host/db" //nolint:gosec // G101: test fixture

// setXDGConfigHome overrides XDG_CONFIG_HOME and HOME to point at a temp dir.
func setXDGConfigHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir) // macOS fallback: os.UserConfigDir → $HOME/.config
}

// TestConfigPath verifies ConfigPath returns the expected sub-path and creates the dir.
func TestConfigPath(t *testing.T) {
	tmp := t.TempDir()
	setXDGConfigHome(t, tmp)

	got, err := cli.ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath() error: %v", err)
	}
	if !strings.HasSuffix(got, filepath.Join("wayneblacktea", "config.yaml")) {
		t.Errorf("ConfigPath() = %q; want suffix wayneblacktea/config.yaml", got)
	}
	dir := filepath.Dir(got)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Errorf("ConfigPath() did not create directory %q", dir)
	}
}

// TestConfigPath_Idempotent verifies repeated calls do not error.
func TestConfigPath_Idempotent(t *testing.T) {
	tmp := t.TempDir()
	setXDGConfigHome(t, tmp)

	if _, err := cli.ConfigPath(); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if _, err := cli.ConfigPath(); err != nil {
		t.Fatalf("second call: %v", err)
	}
}

// TestWriteGlobalConfig_RoundTrip verifies write + read round-trip and 0600 permissions.
func TestWriteGlobalConfig_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	setXDGConfigHome(t, tmp)

	in := cli.WbtConfig{
		APIKey:         "roundtripapikey",
		DatabaseURL:    fakePostgresDSN,
		Port:           "9090",
		StorageBackend: "postgres",
	}
	if err := cli.WriteGlobalConfig(in); err != nil {
		t.Fatalf("WriteGlobalConfig: %v", err)
	}

	p, err := cli.ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath: %v", err)
	}

	// Permissions MUST be 0600.
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %o, want 0600", perm)
	}

	// Content round-trip.
	b, err := os.ReadFile(p) //nolint:gosec // G304: p is from ConfigPath() in a temp dir
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var out cli.WbtConfig
	if err := yaml.Unmarshal(b, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.APIKey != in.APIKey {
		t.Errorf("APIKey: got %q want %q", out.APIKey, in.APIKey)
	}
	if out.Port != in.Port {
		t.Errorf("Port: got %q want %q", out.Port, in.Port)
	}
	if out.DatabaseURL != in.DatabaseURL {
		t.Errorf("DatabaseURL: got %q want %q", out.DatabaseURL, in.DatabaseURL)
	}
	if out.StorageBackend != in.StorageBackend {
		t.Errorf("StorageBackend: got %q want %q", out.StorageBackend, in.StorageBackend)
	}
}

// TestLoadGlobalConfig_MissingFile verifies no-op for absent config.
func TestLoadGlobalConfig_MissingFile(t *testing.T) {
	tmp := t.TempDir()
	setXDGConfigHome(t, tmp)
	t.Setenv("API_KEY", "")

	if err := cli.LoadGlobalConfig(); err != nil {
		t.Fatalf("LoadGlobalConfig on missing file: %v", err)
	}
	if v := os.Getenv("API_KEY"); v != "" {
		t.Errorf("API_KEY = %q after LoadGlobalConfig on absent file; want empty", v)
	}
}

// TestLoadGlobalConfig_DoesNotOverride verifies SetIfAbsent semantics.
func TestLoadGlobalConfig_DoesNotOverride(t *testing.T) {
	tmp := t.TempDir()
	setXDGConfigHome(t, tmp)
	t.Setenv("API_KEY", "pre-existing")

	if err := cli.WriteGlobalConfig(cli.WbtConfig{APIKey: "from-config"}); err != nil {
		t.Fatalf("WriteGlobalConfig: %v", err)
	}
	if err := cli.LoadGlobalConfig(); err != nil {
		t.Fatalf("LoadGlobalConfig: %v", err)
	}
	if got := os.Getenv("API_KEY"); got != "pre-existing" {
		t.Errorf("API_KEY = %q; want pre-existing (must not be overridden)", got)
	}
}

// TestSetIfAbsent covers empty-value, already-set, and unset branches.
func TestSetIfAbsent(t *testing.T) {
	// Case 1: value empty → no mutation.
	t.Setenv("CLI_TEST_KEY1", "")
	cli.SetIfAbsent("CLI_TEST_KEY1", "")
	if v := os.Getenv("CLI_TEST_KEY1"); v != "" {
		t.Errorf("SetIfAbsent(empty): CLI_TEST_KEY1 = %q, want empty", v)
	}

	// Case 2: env already set → must not overwrite.
	t.Setenv("CLI_TEST_KEY2", "original")
	cli.SetIfAbsent("CLI_TEST_KEY2", "replacement")
	if v := os.Getenv("CLI_TEST_KEY2"); v != "original" {
		t.Errorf("SetIfAbsent(pre-set): CLI_TEST_KEY2 = %q, want original", v)
	}

	// Case 3: env unset → must set.
	t.Setenv("CLI_TEST_KEY3", "")
	cli.SetIfAbsent("CLI_TEST_KEY3", "injected")
	if v := os.Getenv("CLI_TEST_KEY3"); v != "injected" {
		t.Errorf("SetIfAbsent(unset): CLI_TEST_KEY3 = %q, want injected", v)
	}
}

// TestDBConfigToWbtConfig verifies all fields are copied correctly.
func TestDBConfigToWbtConfig(t *testing.T) {
	t.Parallel()
	db := cli.DBConfig{StorageBackend: "sqlite", SQLitePath: "/tmp/test.db"}
	got := cli.DBConfigToWbtConfig("mykey", "9090", db)

	if got.APIKey != "mykey" {
		t.Errorf("APIKey = %q, want mykey", got.APIKey)
	}
	if got.Port != "9090" {
		t.Errorf("Port = %q, want 9090", got.Port)
	}
	if got.StorageBackend != "sqlite" {
		t.Errorf("StorageBackend = %q, want sqlite", got.StorageBackend)
	}
	if got.SQLitePath != "/tmp/test.db" {
		t.Errorf("SQLitePath = %q, want /tmp/test.db", got.SQLitePath)
	}
}

// TestWriteMCPJSON verifies .mcp.json generation for SQLite and Postgres.
func TestWriteMCPJSON(t *testing.T) {
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
				`"mcp"`,
				"SQLITE_PATH",
				"STORAGE_BACKEND",
			},
			noKeys: []string{"DATABASE_URL", "wayneblacktea-mcp"},
		},
		{
			name: "postgres backend",
			db: cli.DBConfig{
				StorageBackend: "postgres",
				DatabaseURL:    fakePostgresDSN,
			},
			wantKeys: []string{
				"wayneblacktea",
				`"command": "wbt"`,
				`"mcp"`,
				"DATABASE_URL",
				"STORAGE_BACKEND",
			},
			noKeys: []string{"SQLITE_PATH", "wayneblacktea-mcp"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b, err := cli.WriteMCPJSON(tc.db)
			if err != nil {
				t.Fatalf("WriteMCPJSON error: %v", err)
			}
			got := string(b)
			for _, key := range tc.wantKeys {
				if !strings.Contains(got, key) {
					t.Errorf("WriteMCPJSON missing %q:\n%s", key, got)
				}
			}
			for _, key := range tc.noKeys {
				if strings.Contains(got, key) {
					t.Errorf("WriteMCPJSON must not contain %q:\n%s", key, got)
				}
			}
		})
	}
}

// TestReadEnvPort verifies PORT env-var fallback behaviour.
func TestReadEnvPort(t *testing.T) {
	tests := []struct {
		name    string
		portEnv string
		want    string
	}{
		{name: "env set", portEnv: "3000", want: "3000"},
		{name: "env empty defaults to 8420", portEnv: "", want: "8420"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PORT", tc.portEnv)
			if got := cli.ReadEnvPort(); got != tc.want {
				t.Errorf("ReadEnvPort() = %q, want %q", got, tc.want)
			}
		})
	}
}
