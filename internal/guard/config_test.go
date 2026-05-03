package guard

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// captureSlogWarn redirects slog default output to a buffer for the duration
// of the test. The buffer is returned for substring assertions. Restores the
// previous default handler on cleanup.
func captureSlogWarn(t *testing.T) *bytes.Buffer {
	t.Helper()
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	buf := &bytes.Buffer{}
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	return buf
}

// TestLoadConfig_MarkerAbsent verifies ErrMarkerAbsent when no marker exists.
func TestLoadConfig_MarkerAbsent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := LoadConfig(dir)
	if !errors.Is(err, ErrMarkerAbsent) {
		t.Errorf("LoadConfig(empty dir) = %v, want ErrMarkerAbsent", err)
	}
}

// TestLoadConfig_ValidMarker verifies successful load of a valid marker file.
func TestLoadConfig_ValidMarker(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	markerDir := filepath.Join(dir, ".wayneblacktea")
	if err := os.MkdirAll(markerDir, 0o750); err != nil {
		t.Fatal(err)
	}
	content := `{"version":1,"observe":true,"db_url":"postgres://localhost/test"}`
	if err := os.WriteFile(filepath.Join(markerDir, "config.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: unexpected error: %v", err)
	}
	if cfg.Version != 1 {
		t.Errorf("Version = %d, want 1", cfg.Version)
	}
	if !cfg.Observe {
		t.Error("Observe = false, want true")
	}
	if cfg.DBURL != "postgres://localhost/test" {
		t.Errorf("DBURL = %q, want postgres://localhost/test", cfg.DBURL)
	}
}

// TestLoadConfig_ObserveFalse verifies observe=false is respected.
func TestLoadConfig_ObserveFalse(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	markerDir := filepath.Join(dir, ".wayneblacktea")
	if err := os.MkdirAll(markerDir, 0o750); err != nil {
		t.Fatal(err)
	}
	content := `{"version":1,"observe":false}`
	if err := os.WriteFile(filepath.Join(markerDir, "config.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: unexpected error: %v", err)
	}
	if cfg.Observe {
		t.Error("Observe = true, want false")
	}
}

// TestLoadConfig_MalformedJSON verifies an error on malformed config JSON.
func TestLoadConfig_MalformedJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	markerDir := filepath.Join(dir, ".wayneblacktea")
	if err := os.MkdirAll(markerDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "config.json"), []byte(`{invalid`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfig(dir)
	if err == nil {
		t.Fatal("LoadConfig: expected error for malformed JSON, got nil")
	}
	if errors.Is(err, ErrMarkerAbsent) {
		t.Error("LoadConfig: got ErrMarkerAbsent for malformed JSON, want parse error")
	}
}

// TestResolveDBURL_EnvFallback verifies DATABASE_URL env is used when marker has no db_url.
func TestResolveDBURL_EnvFallback(t *testing.T) {
	// Cannot t.Parallel because t.Setenv mutates process state.
	t.Setenv("DATABASE_URL", "postgres://env-host/db")

	url := ResolveDBURL(Config{Version: 1, Observe: true})
	if url != "postgres://env-host/db" {
		t.Errorf("ResolveDBURL = %q, want env value", url)
	}
}

// TestResolveDBURL_MarkerIgnored verifies the deprecated marker db_url field
// is IGNORED — env wins regardless. The field is kept in the struct so
// LoadConfig can still parse legacy configs and surface a deprecation warn,
// but resolve always returns the env value.
func TestResolveDBURL_MarkerIgnored(t *testing.T) {
	// Cannot t.Parallel because t.Setenv mutates process state.
	t.Setenv("DATABASE_URL", "postgres://env-host/db")

	cfg := Config{Version: 1, Observe: true, DBURL: "postgres://marker-host/db"}
	url := ResolveDBURL(cfg)
	if url != "postgres://env-host/db" {
		t.Errorf("ResolveDBURL = %q, want env value (marker DBURL must be ignored)", url)
	}
}

// TestResolveDBURL_MarkerOnlyReturnsEmpty verifies that with no env var set,
// even a non-empty marker DBURL produces an empty result.
func TestResolveDBURL_MarkerOnlyReturnsEmpty(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	cfg := Config{Version: 1, Observe: true, DBURL: "postgres://marker-host/db"}
	url := ResolveDBURL(cfg)
	if url != "" {
		t.Errorf("ResolveDBURL with env='' and marker set = %q, want empty (db_url is deprecated)", url)
	}
}

// TestResolveDBURL_NeitherSet verifies empty string is returned when neither is set.
func TestResolveDBURL_NeitherSet(t *testing.T) {
	// Cannot t.Parallel because t.Setenv mutates process state.
	t.Setenv("DATABASE_URL", "")

	url := ResolveDBURL(Config{Version: 1, Observe: true})
	if url != "" {
		t.Errorf("ResolveDBURL = %q, want empty", url)
	}
}

// TestLoadConfig_PermissionWarn0644 verifies that a marker file with mode
// 0o644 (group/world readable) emits an slog.Warn so the operator chmod's
// it down to 0o600.
func TestLoadConfig_PermissionWarn0644(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission test relies on POSIX file modes")
	}
	// captureSlogWarn replaces the default handler — cannot run in parallel.
	buf := captureSlogWarn(t)

	dir := t.TempDir()
	markerDir := filepath.Join(dir, ".wayneblacktea")
	if err := os.MkdirAll(markerDir, 0o750); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(markerDir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"version":1,"observe":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Re-chmod in case the umask raised the bits.
	if err := os.Chmod(cfgPath, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadConfig(dir); err != nil {
		t.Fatalf("LoadConfig: unexpected error: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "group/world-readable") {
		t.Errorf("LoadConfig with mode 0644 should emit perm warning; slog output:\n%s", got)
	}
}

// TestLoadConfig_PermissionNoWarn0600 verifies that a marker file with the
// recommended mode 0o600 emits no permission warning.
func TestLoadConfig_PermissionNoWarn0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission test relies on POSIX file modes")
	}
	buf := captureSlogWarn(t)

	dir := t.TempDir()
	markerDir := filepath.Join(dir, ".wayneblacktea")
	if err := os.MkdirAll(markerDir, 0o750); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(markerDir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"version":1,"observe":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cfgPath, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadConfig(dir); err != nil {
		t.Fatalf("LoadConfig: unexpected error: %v", err)
	}
	got := buf.String()
	if strings.Contains(got, "group/world-readable") {
		t.Errorf("LoadConfig with mode 0600 should NOT emit perm warning; slog output:\n%s", got)
	}
}

// TestLoadConfig_DeprecatedDBURLWarn verifies the legacy db_url field
// triggers a deprecation warning so operators migrate to env-only.
func TestLoadConfig_DeprecatedDBURLWarn(t *testing.T) {
	buf := captureSlogWarn(t)

	dir := t.TempDir()
	markerDir := filepath.Join(dir, ".wayneblacktea")
	if err := os.MkdirAll(markerDir, 0o750); err != nil {
		t.Fatal(err)
	}
	content := `{"version":1,"observe":true,"db_url":"postgres://marker-host/db"}`
	if err := os.WriteFile(filepath.Join(markerDir, "config.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: unexpected error: %v", err)
	}
	// The struct still parses the value (backward-compat); but the operator
	// is warned that it'll be ignored at resolve time.
	if cfg.DBURL == "" {
		t.Error("LoadConfig should still parse the deprecated db_url field for backward compat")
	}
	got := buf.String()
	if !strings.Contains(got, "DEPRECATED") || !strings.Contains(got, "DATABASE_URL") {
		t.Errorf("LoadConfig with db_url field should emit deprecation warning; slog output:\n%s", got)
	}
}
