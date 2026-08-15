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

// strPtr is the *string literal helper for arch.UpsertParams.Summary and
// LastCommitSHA's patch semantics (security review PR #157 round 3,
// unifying what M-3 originally gave FileMap alone): a non-nil pointer —
// even to "" — is an explicit value, as opposed to the Go zero value nil
// which means "field omitted, keep whatever is stored".
func strPtr(s string) *string { return &s }

func TestArchStore_UpsertAndGet(t *testing.T) {
	s := openArchDB(t, ":memory:")
	ctx := context.Background()

	snap, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:          "wayneblacktea",
		Summary:       strPtr("Personal OS built with Go and MCP."),
		FileMap:       fileMapPtr(map[string]string{"internal/arch/arch.go": "arch domain types"}),
		LastCommitSHA: strPtr("abc123"),
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
		Summary: strPtr("first summary"),
		FileMap: fileMapPtr(map[string]string{"main.go": "entrypoint"}),
	}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	updated, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:          "repo-x",
		Summary:       strPtr("second summary"),
		FileMap:       fileMapPtr(map[string]string{"main.go": "entrypoint", "handler.go": "handlers"}),
		LastCommitSHA: strPtr("deadbeef"),
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
		Summary: strPtr("no files yet"),
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
		Summary: strPtr("nil map test"),
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
		Summary: strPtr("first"),
		FileMap: fileMapPtr(seeded),
	}); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}

	// Simulate an agent that follows the read-then-write protocol but
	// never saw the existing file_map (get_project_arch's default omits
	// it, W2) and therefore omits the argument entirely on write-back.
	got, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "patch-absent",
		Summary: strPtr("second, file_map omitted"),
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
		Summary: strPtr("first"),
		FileMap: fileMapPtr(map[string]string{"a.go": "a", "b.go": "b", "c.go": "c"}),
	}); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}

	got, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "patch-explicit-empty",
		Summary: strPtr("second, explicit clear"),
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
		Summary: strPtr("first"),
		FileMap: fileMapPtr(map[string]string{"old.go": "old", "also-old.go": "also old"}),
	}); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}

	got, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "patch-replace",
		Summary: strPtr("second, replaced"),
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

// --- round-3 patch-semantics unification: summary / last_commit_sha -----
//
// Before security review PR #157 round 3, summary and last_commit_sha were
// unconditionally EXCLUDED.<col> in the upsert SQL — an agent that followed
// the read-then-write protocol but omitted either field (stringArg folds
// "absent" and "present-but-empty" into "") silently wiped it. These mirror
// the file_map three-state matrix above for the other two patch-semantics
// fields. Mirrored in internal/arch/store_postgres_test.go for parity.

func TestArchStore_UpsertSummaryAbsent_PreservesExisting(t *testing.T) {
	s := openArchDB(t, ":memory:")
	ctx := context.Background()

	if _, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "summary-patch-absent",
		Summary: strPtr("original summary, must survive"),
	}); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}

	got, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:          "summary-patch-absent",
		LastCommitSHA: strPtr("sha-only-update"),
		// Summary omitted entirely.
	})
	if err != nil {
		t.Fatalf("summary-less upsert: %v", err)
	}
	if got.Summary != "original summary, must survive" {
		t.Fatalf("DATA LOSS: summary omitted on upsert wiped the stored value: got %q, want preserved %q",
			got.Summary, "original summary, must survive")
	}
	if got.LastCommitSHA != "sha-only-update" {
		t.Errorf("last_commit_sha should still update independently of summary: got %q", got.LastCommitSHA)
	}
}

func TestArchStore_UpsertSummaryExplicitEmpty_Clears(t *testing.T) {
	s := openArchDB(t, ":memory:")
	ctx := context.Background()

	if _, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "summary-patch-explicit-empty",
		Summary: strPtr("first"),
	}); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}

	got, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "summary-patch-explicit-empty",
		Summary: strPtr(""),
	})
	if err != nil {
		t.Fatalf("explicit-clear upsert: %v", err)
	}
	if got.Summary != "" {
		t.Fatalf("explicit \"\" should clear summary, got %q", got.Summary)
	}
}

func TestArchStore_UpsertSummaryContent_Replaces(t *testing.T) {
	s := openArchDB(t, ":memory:")
	ctx := context.Background()

	if _, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "summary-patch-replace",
		Summary: strPtr("old summary"),
	}); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}

	got, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "summary-patch-replace",
		Summary: strPtr("new summary"),
	})
	if err != nil {
		t.Fatalf("replace upsert: %v", err)
	}
	if got.Summary != "new summary" {
		t.Fatalf("expected summary replaced, got %q", got.Summary)
	}
}

func TestArchStore_UpsertLastCommitSHAAbsent_PreservesExisting(t *testing.T) {
	s := openArchDB(t, ":memory:")
	ctx := context.Background()

	if _, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:          "sha-patch-absent",
		Summary:       strPtr("first"),
		LastCommitSHA: strPtr("original-sha"),
	}); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}

	got, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "sha-patch-absent",
		Summary: strPtr("summary-only update"),
		// LastCommitSHA omitted entirely.
	})
	if err != nil {
		t.Fatalf("sha-less upsert: %v", err)
	}
	if got.LastCommitSHA != "original-sha" {
		t.Fatalf("DATA LOSS: last_commit_sha omitted on upsert wiped the stored value: got %q, want preserved %q",
			got.LastCommitSHA, "original-sha")
	}
}

func TestArchStore_UpsertLastCommitSHAExplicitEmpty_Clears(t *testing.T) {
	s := openArchDB(t, ":memory:")
	ctx := context.Background()

	if _, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:          "sha-patch-explicit-empty",
		Summary:       strPtr("first"),
		LastCommitSHA: strPtr("abc123"),
	}); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}

	got, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:          "sha-patch-explicit-empty",
		Summary:       strPtr("second"),
		LastCommitSHA: strPtr(""),
	})
	if err != nil {
		t.Fatalf("explicit-clear upsert: %v", err)
	}
	if got.LastCommitSHA != "" {
		t.Fatalf("explicit \"\" should clear last_commit_sha, got %q", got.LastCommitSHA)
	}
}

func TestArchStore_UpsertLastCommitSHAContent_Replaces(t *testing.T) {
	s := openArchDB(t, ":memory:")
	ctx := context.Background()

	if _, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:          "sha-patch-replace",
		Summary:       strPtr("first"),
		LastCommitSHA: strPtr("old-sha"),
	}); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}

	got, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:          "sha-patch-replace",
		Summary:       strPtr("second"),
		LastCommitSHA: strPtr("new-sha"),
	})
	if err != nil {
		t.Fatalf("replace upsert: %v", err)
	}
	if got.LastCommitSHA != "new-sha" {
		t.Fatalf("expected last_commit_sha replaced, got %q", got.LastCommitSHA)
	}
}

// TestArchStore_StalenessNotBrokenByOmittedSHA is the "new problem #2"
// regression guard from the round-3 dispatch: unifying the patch semantics
// must not silently trade "data loss" for "staleness check permanently
// wrong". The core protocol (mcpInstructions, server.go) compares
// last_commit_sha against `git rev-parse HEAD` — if omitting the field ever
// resolved to something other than "keep the existing SHA", that
// comparison would be permanently right or permanently wrong regardless of
// the real HEAD.
func TestArchStore_StalenessNotBrokenByOmittedSHA(t *testing.T) {
	s := openArchDB(t, ":memory:")
	ctx := context.Background()

	if _, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:          "staleness",
		Summary:       strPtr("v1"),
		LastCommitSHA: strPtr("sha-A"),
	}); err != nil {
		t.Fatalf("seed upsert (sha=A): %v", err)
	}

	// Upsert that omits last_commit_sha (e.g. an agent only refreshing
	// summary/file_map) must leave sha-A in place for staleness detection.
	afterOmit, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "staleness",
		Summary: strPtr("v2, sha omitted"),
	})
	if err != nil {
		t.Fatalf("sha-omitted upsert: %v", err)
	}
	if afterOmit.LastCommitSHA != "sha-A" {
		t.Fatalf("staleness check broken: omitting last_commit_sha changed it from %q to %q, want preserved %q",
			"sha-A", afterOmit.LastCommitSHA, "sha-A")
	}

	// An explicit new SHA must still take effect — proves "omit preserves"
	// was not implemented as "always ignore explicit values too".
	afterExplicit, err := s.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:          "staleness",
		Summary:       strPtr("v3, explicit sha"),
		LastCommitSHA: strPtr("sha-B"),
	})
	if err != nil {
		t.Fatalf("explicit-sha upsert: %v", err)
	}
	if afterExplicit.LastCommitSHA != "sha-B" {
		t.Fatalf("explicit last_commit_sha did not take effect: got %q, want %q", afterExplicit.LastCommitSHA, "sha-B")
	}
}
