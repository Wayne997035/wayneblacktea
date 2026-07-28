package sqlite_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
)

// TestOpen_FileBackedDBIsChmod0600 proves the security fix for PR16's Major
// finding: the SQLite driver creates new files at 0666&~umask (commonly
// 0644, group/world-readable), but this DB holds handoffs/decisions/
// knowledge (personal data) — backend-security-design.md §4.1. Open MUST
// restrict the file to 0600 regardless of the caller (hook/server/mcp all
// share this function).
func TestOpen_FileBackedDBIsChmod0600(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "perm-check.db")

	d, err := sqlite.Open(context.Background(), dbPath, "")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat %q: %v", dbPath, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("DB file mode = %o, want 0600", perm)
	}
}

// TestOpen_PreExistingPermissiveFileGetsTightened proves Open actively
// corrects an already-permissive file (e.g. one created by a pre-fix binary,
// like the three stray 0644 wayneblacktea.db files found on disk) rather
// than only warning about it — stronger than the read-time warn-only pattern
// in backend-security-design.md §4.1 since Open is also the file's writer.
func TestOpen_PreExistingPermissiveFileGetsTightened(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "already-permissive.db")

	// Create+open once, then manually widen permissions to simulate a
	// pre-fix-created file, then re-open and confirm it's tightened back.
	d1, err := sqlite.Open(context.Background(), dbPath, "")
	if err != nil {
		t.Fatalf("sqlite.Open (first): %v", err)
	}
	if err := d1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	//nolint:gosec // G302: deliberately widening perms to simulate a pre-fix-created file, not creating credentials
	if err := os.Chmod(dbPath, 0o644); err != nil {
		t.Fatalf("chmod to simulate pre-fix file: %v", err)
	}

	d2, err := sqlite.Open(context.Background(), dbPath, "")
	if err != nil {
		t.Fatalf("sqlite.Open (second): %v", err)
	}
	t.Cleanup(func() { _ = d2.Close() })

	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat %q: %v", dbPath, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("DB file mode after re-Open = %o, want 0600 (Open must re-tighten permissive files)", perm)
	}
}

// TestOpen_InMemorySkipsChmod proves ":memory:" (no backing file) doesn't
// make Open fail — os.Chmod on a non-existent ":memory:" path would error
// if not explicitly skipped.
func TestOpen_InMemorySkipsChmod(t *testing.T) {
	d, err := sqlite.Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("sqlite.Open(:memory:): %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
}
