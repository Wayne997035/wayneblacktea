package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/arch"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
)

func openArchDB(t *testing.T, dsn string) *sqlite.ArchStore {
	t.Helper()
	d, err := sqlite.Open(context.Background(), dsn, "")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return sqlite.NewArchStore(d)
}

// fileMapPtr is the *map[string]string literal helper for
// arch.UpsertParams.FileMap's patch semantics: a non-nil pointer (even to
// an empty map) is an explicit value, as opposed to the Go zero value nil
// which means "field omitted, keep whatever is stored".
func fileMapPtr(m map[string]string) *map[string]string { return &m }

func TestArchStore_UpsertAndGet(t *testing.T) {
	s := openArchDB(t, ":memory:")
	ctx := context.Background()

	snap, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:          "wayneblacktea",
		Summary:       "Personal OS built with Go and MCP.",
		FileMap:       fileMapPtr(map[string]string{"internal/arch/arch.go": "arch domain types"}),
		LastCommitSHA: "abc123",
	})
	if err != nil {
		t.Fatalf("UpsertSnapshot: %v", err)
	}
	if snap.Slug != "wayneblacktea" {
		t.Fatalf("got slug %q, want %q", snap.Slug, "wayneblacktea")
	}
	if snap.Summary != "Personal OS built with Go and MCP." {
		t.Fatalf("unexpected summary: %q", snap.Summary)
	}
	if snap.LastCommitSHA != "abc123" {
		t.Fatalf("unexpected commit sha: %q", snap.LastCommitSHA)
	}
	if len(snap.FileMap) != 1 || snap.FileMap["internal/arch/arch.go"] != "arch domain types" {
		t.Fatalf("unexpected file_map: %v", snap.FileMap)
	}

	got, err := s.GetSnapshot(ctx, "wayneblacktea")
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}
	if got.ID != snap.ID {
		t.Fatalf("id mismatch: got %q, want %q", got.ID, snap.ID)
	}
}

func TestArchStore_UpsertUpdatesExisting(t *testing.T) {
	s := openArchDB(t, ":memory:")
	ctx := context.Background()

	if _, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "repo-x",
		Summary: "first summary",
		FileMap: fileMapPtr(map[string]string{"main.go": "entrypoint"}),
	}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	updated, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:          "repo-x",
		Summary:       "second summary",
		FileMap:       fileMapPtr(map[string]string{"main.go": "entrypoint", "handler.go": "handlers"}),
		LastCommitSHA: "deadbeef",
	})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	if updated.Summary != "second summary" {
		t.Fatalf("expected updated summary, got %q", updated.Summary)
	}
	if len(updated.FileMap) != 2 {
		t.Fatalf("expected 2 file_map entries, got %d", len(updated.FileMap))
	}
	if updated.LastCommitSHA != "deadbeef" {
		t.Fatalf("expected updated commit sha, got %q", updated.LastCommitSHA)
	}
}

func TestArchStore_GetNotFound(t *testing.T) {
	s := openArchDB(t, ":memory:")
	_, err := s.GetSnapshot(context.Background(), "nonexistent-slug")
	if !errors.Is(err, arch.ErrNotFound) {
		t.Fatalf("expected arch.ErrNotFound, got %v", err)
	}
}

func TestArchStore_EmptyFileMap(t *testing.T) {
	s := openArchDB(t, ":memory:")
	ctx := context.Background()

	// FileMap omitted (nil pointer) on a slug with NO existing row: there
	// is nothing to preserve, so the store must default to {}, not leave
	// the column unset (it is NOT NULL).
	snap, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "empty-map",
		Summary: "no files yet",
	})
	if err != nil {
		t.Fatalf("UpsertSnapshot: %v", err)
	}
	if snap.FileMap == nil {
		t.Fatal("file_map should not be nil")
	}
	if len(snap.FileMap) != 0 {
		t.Fatalf("expected empty file_map, got %v", snap.FileMap)
	}
}

func TestArchStore_ContextCanceled(t *testing.T) {
	s := openArchDB(t, ":memory:")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.GetSnapshot(ctx, "any-slug")
	if err == nil {
		t.Fatal("expected error with canceled context, got nil")
	}
}

func TestArchStore_InvalidJSONFileMap_Roundtrip(t *testing.T) {
	s := openArchDB(t, ":memory:")
	ctx := context.Background()

	// Store a nil (omitted) file map on a brand-new slug — should default
	// to "{}" and unmarshal cleanly, same as TestArchStore_EmptyFileMap.
	snap, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "nil-map",
		Summary: "nil map test",
		FileMap: nil,
	})
	if err != nil {
		t.Fatalf("UpsertSnapshot with nil FileMap: %v", err)
	}
	if snap.FileMap == nil {
		t.Fatal("expected non-nil file_map after round-trip")
	}
}

// --- M-3 patch-semantics three-state matrix (security review PR #157) ---
//
// These three tests pin the contract that closes the file_map data-loss
// hole: FileMap field ABSENT (nil pointer) preserves whatever is already
// stored; FileMap present pointing at an EMPTY map explicitly clears it;
// FileMap present with CONTENT replaces the stored map outright (not a
// merge). Mirrored in internal/arch/store_postgres_test.go for parity.

func TestArchStore_UpsertFileMapAbsent_PreservesExisting(t *testing.T) {
	s := openArchDB(t, ":memory:")
	ctx := context.Background()

	seeded := map[string]string{
		"cmd/server/main.go":     "entrypoint",
		"internal/mcp/server.go": "mcp wiring",
		"internal/arch/arch.go":  "arch domain types",
	}
	if _, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "patch-absent",
		Summary: "first",
		FileMap: fileMapPtr(seeded),
	}); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}

	// Simulate an agent that follows the read-then-write protocol but
	// never saw the existing file_map (get_project_arch's default omits
	// it, W2) and therefore omits the argument entirely on write-back.
	got, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "patch-absent",
		Summary: "second, file_map omitted",
		FileMap: nil,
	})
	if err != nil {
		t.Fatalf("map-less upsert: %v", err)
	}
	if len(got.FileMap) != len(seeded) {
		t.Fatalf("DATA LOSS: file_map omitted on upsert wiped stored map: got %d entries, want %d preserved: %v",
			len(got.FileMap), len(seeded), got.FileMap)
	}
	for path, purpose := range seeded {
		if got.FileMap[path] != purpose {
			t.Errorf("file_map[%q] = %q, want preserved %q", path, got.FileMap[path], purpose)
		}
	}
	if got.Summary != "second, file_map omitted" {
		t.Errorf("summary should still update independently of file_map: got %q", got.Summary)
	}
}

func TestArchStore_UpsertFileMapExplicitEmpty_Clears(t *testing.T) {
	s := openArchDB(t, ":memory:")
	ctx := context.Background()

	if _, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "patch-explicit-empty",
		Summary: "first",
		FileMap: fileMapPtr(map[string]string{"a.go": "a", "b.go": "b", "c.go": "c"}),
	}); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}

	got, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "patch-explicit-empty",
		Summary: "second, explicit clear",
		FileMap: fileMapPtr(map[string]string{}),
	})
	if err != nil {
		t.Fatalf("explicit-clear upsert: %v", err)
	}
	if len(got.FileMap) != 0 {
		t.Fatalf("explicit {} should clear file_map, got %d entries: %v", len(got.FileMap), got.FileMap)
	}
}

func TestArchStore_UpsertFileMapContent_Replaces(t *testing.T) {
	s := openArchDB(t, ":memory:")
	ctx := context.Background()

	if _, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "patch-replace",
		Summary: "first",
		FileMap: fileMapPtr(map[string]string{"old.go": "old", "also-old.go": "also old"}),
	}); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}

	got, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "patch-replace",
		Summary: "second, replaced",
		FileMap: fileMapPtr(map[string]string{"new.go": "new"}),
	})
	if err != nil {
		t.Fatalf("replace upsert: %v", err)
	}
	if len(got.FileMap) != 1 || got.FileMap["new.go"] != "new" {
		t.Fatalf("expected file_map replaced (not merged) with exactly {new.go: new}, got %v", got.FileMap)
	}
	if _, stillThere := got.FileMap["old.go"]; stillThere {
		t.Errorf("old.go should have been replaced away, still present: %v", got.FileMap)
	}
}
