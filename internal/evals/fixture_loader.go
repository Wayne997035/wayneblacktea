package evals

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validateFixtureName rejects any fixture name that could escape the
// testdata/ directory: path separators, ".." segments, or an empty name.
// Split out from LoadFixtures so the guard is unit-testable on its own
// without needing to trigger t.Fatalf (backend-security-design.md §2.2).
func validateFixtureName(name string) error {
	if name == "" || strings.Contains(name, "..") || strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("fixture name %q must be a bare filename with no path separators", name)
	}
	return nil
}

// LoadFixtures reads and JSON-decodes a fixture file from internal/evals/testdata
// into a slice of T. name MUST be a bare filename with no path separators or
// ".." segments — this guards against a fixture name turning into a
// path-traversal read of arbitrary files on disk (backend-security-design.md §2.2).
func LoadFixtures[T any](t *testing.T, name string) []T {
	t.Helper()

	if err := validateFixtureName(name); err != nil {
		t.Fatalf("evals: %v", err)
	}

	path := filepath.Join("testdata", name)
	//nolint:gosec // G304: name is rejected above by validateFixtureName (no separators, no ".."), so path can only resolve inside testdata/
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("evals: read fixture %s: %v", path, err)
	}

	var fixtures []T
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatalf("evals: unmarshal fixture %s: %v", path, err)
	}
	return fixtures
}
