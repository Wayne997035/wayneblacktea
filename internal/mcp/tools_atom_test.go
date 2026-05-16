package mcp

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/atom"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
	"github.com/google/uuid"
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
