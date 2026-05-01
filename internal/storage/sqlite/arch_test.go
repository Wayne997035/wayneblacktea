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

func TestArchStore_UpsertAndGet(t *testing.T) {
	s := openArchDB(t, ":memory:")
	ctx := context.Background()

	snap, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:          "wayneblacktea",
		Summary:       "Personal OS built with Go and MCP.",
		FileMap:       map[string]string{"internal/arch/arch.go": "arch domain types"},
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
		FileMap: map[string]string{"main.go": "entrypoint"},
	}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	updated, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:          "repo-x",
		Summary:       "second summary",
		FileMap:       map[string]string{"main.go": "entrypoint", "handler.go": "handlers"},
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

	// Store a nil file map — should marshal to "{}" and unmarshal cleanly.
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
