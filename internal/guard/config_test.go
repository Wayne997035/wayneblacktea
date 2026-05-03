package guard

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

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

// TestResolveDBURL_MarkerOverridesEnv verifies marker db_url takes precedence.
func TestResolveDBURL_MarkerOverridesEnv(t *testing.T) {
	// Cannot t.Parallel because t.Setenv mutates process state.
	t.Setenv("DATABASE_URL", "postgres://env-host/db")

	cfg := Config{Version: 1, Observe: true, DBURL: "postgres://marker-host/db"}
	url := ResolveDBURL(cfg)
	if url != "postgres://marker-host/db" {
		t.Errorf("ResolveDBURL = %q, want marker value", url)
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
