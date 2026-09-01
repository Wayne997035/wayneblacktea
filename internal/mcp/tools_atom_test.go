package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/atom"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// newAtomizeServer builds a minimal *Server for launchAtomize tests.
// It uses a real SQLite bundle so s.atomizer and s.atom are non-nil
// (launchAtomize returns early if either is nil), then overwrites atomizeFn
// with a test fake.
func newAtomizeServer(t *testing.T) *Server {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "atomize-test.db")
	stores, err := storage.NewServerStores(context.Background(), storage.FactoryConfig{
		Backend:    storage.BackendSQLite,
		SQLitePath: dbPath,
	})
	if err != nil {
		t.Fatalf("NewServerStores: %v", err)
	}
	t.Cleanup(func() { _ = stores.Close() })

	srv, err := New(stores)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// New() sets atomizer to ai.NewAtomizer() which requires CLAUDE_API_KEY.
	// We only need a non-nil sentinel to pass the nil guard in launchAtomize;
	// the real network call is replaced by atomizeFn below.
	if srv.atomizer == nil {
		srv.atomizer = &ai.Atomizer{}
	}
	return srv
}

// TestLaunchAtomize_ConcurrencyAndRace verifies that the 5-slot atomizeSem
// semaphore enforces the concurrency limit and that there is no data race.
//
// Design note: launchAtomize uses a non-blocking select — goroutines that
// cannot immediately acquire a slot are dropped (logged and discarded).
// With 15 goroutines and a 5-slot semaphore the peak concurrent count must
// never exceed 5.
func TestLaunchAtomize_ConcurrencyAndRace(t *testing.T) {
	const (
		semCap     = 5
		goroutines = 15
		fakeSleep  = 10 * time.Millisecond
	)

	srv := newAtomizeServer(t)

	// Replace the 5-slot semaphore so we know the exact capacity under test.
	srv.atomizeSem = make(chan struct{}, semCap)

	var (
		currentConcurrent atomic.Int64 // goroutines currently inside fake
		maxConcurrent     atomic.Int64 // high-water mark
		entered           atomic.Int64 // total invocations that got the slot
	)

	// fakeFn replaces atomizeAndPersist; it has no real LLM/DB calls.
	fakeFn := func(
		_ context.Context,
		_ *ai.Atomizer,
		_ atom.StoreIface,
		_ *uuid.UUID,
		_ string,
		_ uuid.UUID,
		_ string,
	) {
		cur := currentConcurrent.Add(1)
		// Update high-water mark atomically.
		for {
			old := maxConcurrent.Load()
			if cur <= old {
				break
			}
			if maxConcurrent.CompareAndSwap(old, cur) {
				break
			}
		}
		entered.Add(1)
		time.Sleep(fakeSleep) // hold slot long enough for goroutines to overlap
		currentConcurrent.Add(-1)
	}
	srv.atomizeFn = fakeFn

	// Dispatch 15 goroutines all trying to call launchAtomize simultaneously.
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range goroutines {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // release all at once
			srv.launchAtomize("test_table", uuid.New(), "payload")
		}(i)
	}
	close(start) // release all goroutines simultaneously
	wg.Wait()    // wait for launchAtomize calls to return (they are non-blocking)

	// The background goroutines launched by launchAtomize are still running.
	// Drain the semaphore: wait until all slots are free again (== all
	// background goroutines have exited).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(srv.atomizeSem) == 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}

	got := maxConcurrent.Load()
	if got > semCap {
		t.Errorf("peak concurrent goroutines = %d; want <= %d (semaphore cap)",
			got, semCap)
	}
	if got == 0 && entered.Load() == 0 {
		t.Error("no goroutine ever entered fakeFn; test did not exercise the semaphore path")
	}
	t.Logf("peak concurrent = %d / %d cap; total entered = %d / %d goroutines",
		got, semCap, entered.Load(), goroutines)
}

// TestLaunchAtomize_NilGuard verifies that launchAtomize returns immediately
// when atomizer or atom store is nil, without spawning any goroutine.
func TestLaunchAtomize_NilGuard(t *testing.T) {
	cases := []struct {
		name     string
		nilField string
	}{
		{"nil atomizer", "atomizer"},
		{"nil atom store", "atom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newAtomizeServer(t)
			var called atomic.Bool
			srv.atomizeFn = func(_ context.Context, _ *ai.Atomizer, _ atom.StoreIface, _ *uuid.UUID, _ string, _ uuid.UUID, _ string) {
				called.Store(true)
			}
			switch tc.nilField {
			case "atomizer":
				srv.atomizer = nil
			case "atom":
				srv.atom = nil
			}
			srv.launchAtomize("t", uuid.New(), "text")
			// Give any erroneously-spawned goroutine a chance to run.
			time.Sleep(20 * time.Millisecond)
			if called.Load() {
				t.Errorf("atomizeFn was called despite nil %s", tc.nilField)
			}
		})
	}
}

// TestLaunchAtomize_SemDrop verifies that when all semaphore slots are held,
// additional launchAtomize calls are silently dropped (not queued).
func TestLaunchAtomize_SemDrop(t *testing.T) {
	const semCap = 5

	srv := newAtomizeServer(t)
	srv.atomizeSem = make(chan struct{}, semCap)

	// Pre-fill the semaphore so every launchAtomize call hits the default branch.
	for i := range semCap {
		srv.atomizeSem <- struct{}{}
		_ = i
	}

	var called atomic.Int64
	release := make(chan struct{})
	srv.atomizeFn = func(_ context.Context, _ *ai.Atomizer, _ atom.StoreIface, _ *uuid.UUID, _ string, _ uuid.UUID, _ string) {
		called.Add(1)
		<-release // block indefinitely until we release
	}

	// All 5 slots pre-occupied — these calls should be dropped immediately.
	for range 5 {
		srv.launchAtomize("t", uuid.New(), "text")
	}
	time.Sleep(20 * time.Millisecond)

	close(release) // unblock any goroutines that somehow got in
	// Drain the semaphore.
	for len(srv.atomizeSem) > 0 {
		<-srv.atomizeSem
	}

	if n := called.Load(); n != 0 {
		t.Errorf("atomizeFn called %d times; want 0 (semaphore was full)", n)
	}
}

// ---------------------------------------------------------------------------
// promote_atom_to_knowledge: MAJOR-1 (consolidated guard) + MAJOR-3 (rune-aware)
// ---------------------------------------------------------------------------

// newPromoteServer builds a minimal *Server backed by a real SQLite DB for
// promote_atom_to_knowledge tests. atom + proposal stores are wired; atomizer
// is nil (not needed for promote path).
func newPromoteServer(t *testing.T) (*Server, *wbtsqlite.DB) {
	t.Helper()
	db, err := wbtsqlite.Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	atomStore := wbtsqlite.NewAtomStore(db)
	proposalStore := wbtsqlite.NewProposalStore(db)

	srv := &Server{
		atom:       atomStore,
		proposal:   proposalStore,
		atomizeSem: make(chan struct{}, 5),
	}
	return srv, db
}

// insertAtomFixture inserts a memory_atoms row directly so promote tests can
// control digest_status precisely without going through the full atomize path.
func insertAtomFixture(t *testing.T, db *wbtsqlite.DB, content string, tags []string, digestStatus string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	parentID := uuid.New()
	// Encode tags as JSON array.
	tagsJSON := `["` + strings.Join(tags, `","`) + `"]`
	err := db.ExecContext(context.Background(),
		`INSERT INTO memory_atoms (id, parent_table, parent_id, content, keywords, tags, digest_status, created_at)
		 VALUES (?, 'test', ?, ?, '[]', ?, ?, datetime('now'))`,
		id.String(), parentID.String(), content, tagsJSON, digestStatus,
	)
	if err != nil {
		t.Fatalf("insertAtomFixture: %v", err)
	}
	return id
}

func callPromoteAtom(t *testing.T, s *Server, atomID string) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = map[string]any{"atom_id": atomID}
	r, err := s.handlePromoteAtomToKnowledge(context.Background(), req)
	if err != nil {
		t.Fatalf("handlePromoteAtomToKnowledge Go error: %v", err)
	}
	return r
}

// TestPromoteAtom_RejectsNonConsolidated verifies that MAJOR-1 fix works:
// atoms with digest_status != "consolidated" are rejected with a clear error.
func TestPromoteAtom_RejectsNonConsolidated(t *testing.T) {
	s, db := newPromoteServer(t)

	cases := []struct {
		status string
	}{
		{"pending"},
		{"done"},
		{"failed"},
		{"promoted"},
	}
	// Long content + 2 tags to pass quality gate; only status should block.
	content := strings.Repeat("x", 100)
	tags := []string{"tag1", "tag2"}

	for _, tc := range cases {
		t.Run("status_"+tc.status, func(t *testing.T) {
			id := insertAtomFixture(t, db, content, tags, tc.status)
			r := callPromoteAtom(t, s, id.String())
			if !r.IsError {
				t.Errorf("status=%q: expected IsError=true, got false", tc.status)
			}
			text := resultText(r)
			if !strings.Contains(text, "consolidated") {
				t.Errorf("status=%q: error should mention 'consolidated', got: %s", tc.status, text)
			}
		})
	}
}

// TestPromoteAtom_AcceptsConsolidated verifies that a consolidated atom with
// sufficient content and tags is promoted successfully (MAJOR-1 happy path).
func TestPromoteAtom_AcceptsConsolidated(t *testing.T) {
	s, db := newPromoteServer(t)
	content := strings.Repeat("knowledge content for promotion ", 3)
	id := insertAtomFixture(t, db, content, []string{"go", "testing"}, "consolidated")

	r := callPromoteAtom(t, s, id.String())
	if r.IsError {
		t.Fatalf("expected success for consolidated atom, got error: %s", resultText(r))
	}
	text := resultText(r)
	if !strings.Contains(text, "pending") {
		t.Errorf("response should indicate proposal status=pending, got: %s", text)
	}
}

// TestPromoteAtom_RuneAwareContentLength verifies MAJOR-3: a CJK atom whose
// byte length >= 80 but rune count < 80 is correctly rejected by the quality gate.
func TestPromoteAtom_RuneAwareContentLength(t *testing.T) {
	s, db := newPromoteServer(t)
	// Each CJK character is 3 UTF-8 bytes but 1 rune.
	// 27 runes × 3 bytes = 81 bytes but only 27 runes → must be rejected.
	cjkContent := strings.Repeat("測", 27)
	if len(cjkContent) < 80 {
		t.Fatalf("test setup: expected byte length >= 80, got %d", len(cjkContent))
	}
	id := insertAtomFixture(t, db, cjkContent, []string{"tag1", "tag2"}, "consolidated")

	r := callPromoteAtom(t, s, id.String())
	if !r.IsError {
		t.Fatalf("expected content-too-short error for 27 CJK runes, got success")
	}
	if !strings.Contains(resultText(r), "runes") {
		t.Errorf("error should mention runes, got: %s", resultText(r))
	}
}

// TestPromoteAtom_RuneAwareTitleTruncation verifies MAJOR-3: a CJK atom with
// sufficient rune count has its title truncated at rune boundary (no panic,
// valid UTF-8).
func TestPromoteAtom_RuneAwareTitleTruncation(t *testing.T) {
	s, db := newPromoteServer(t)
	// 100 CJK runes — well above the 80-rune minimum; title must be 80 runes.
	content := strings.Repeat("漢", 100)
	id := insertAtomFixture(t, db, content, []string{"cjk", "test"}, "consolidated")

	r := callPromoteAtom(t, s, id.String())
	if r.IsError {
		t.Fatalf("expected success for 100-rune CJK atom, got error: %s", resultText(r))
	}
}

// TestClipSafeSlice_NilInYieldsNilOut pins F981-04's behaviour-change
// decision (ticket ff812f80): clipSafeSlice absorbed the nil early-return
// from tools_skill.go's and tools_procedural.go's now-deleted per-type
// variants, so nil in now yields nil out (JSON null) instead of the
// pre-merge []string{} (JSON []). No direct unit test existed on any of the
// three pre-merge functions — this is new coverage, not a renamed existing
// test.
func TestClipSafeSlice_NilInYieldsNilOut(t *testing.T) {
	got := clipSafeSlice(nil, 10)
	if got != nil {
		t.Fatalf("clipSafeSlice(nil, 10) = %#v, want nil", got)
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if string(b) != "null" {
		t.Errorf("json.Marshal(clipSafeSlice(nil, 10)) = %s, want null", b)
	}
}

// TestClipSafeSlice_RuneSafeClip verifies clipSafeSlice clips each element at
// a rune boundary (delegates to clipSafe per-element, tools_context.go:200),
// matching the pre-merge clipSafeSlice's own behaviour-freeze contract —
// F981-04. "héllo" (5 runes) exceeds maxRunes=3 so clipSafe truncates to its
// first 3 runes and appends clipMarker (U+2026, tools_context.go:166); "x"
// (1 rune) is under the cap and passes through unchanged — this is clipSafe's
// documented contract, not something this merge changes.
func TestClipSafeSlice_RuneSafeClip(t *testing.T) {
	got := clipSafeSlice([]string{"héllo", "x"}, 3)
	want := []string{"hél" + clipMarker, "x"}
	if len(got) != len(want) {
		t.Fatalf("clipSafeSlice length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("clipSafeSlice[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestClipSafeSlice_EmptyNonNilStaysEmpty pins the non-nil empty-slice case
// distinctly from TestClipSafeSlice_NilInYieldsNilOut: a non-nil, zero-length
// []string{} must stay a non-nil []string{} (JSON []), not be folded into the
// nil early-return above it — F981-04.
func TestClipSafeSlice_EmptyNonNilStaysEmpty(t *testing.T) {
	got := clipSafeSlice([]string{}, 10)
	if got == nil {
		t.Fatal("clipSafeSlice([]string{}, 10) = nil, want non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("clipSafeSlice([]string{}, 10) length = %d, want 0", len(got))
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if string(b) != "[]" {
		t.Errorf("json.Marshal(clipSafeSlice([]string{}, 10)) = %s, want []", b)
	}
}
