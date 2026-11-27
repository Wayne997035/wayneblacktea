package mcp

// [F1127-08] Server.waitBackground must wait for the background audit writes
// started by disciplineMiddleware and both autoLogMiddleware launch paths, and
// newTestMCPServer must wait for them before closing SQLite (otherwise the
// write races the close and logs "database is closed").

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/discipline"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

// bgBlockingDiscipline blocks Insert until release is closed.
type bgBlockingDiscipline struct {
	discipline.Store
	started chan struct{}
	release chan struct{}
}

func (b *bgBlockingDiscipline) Insert(ctx context.Context, p discipline.InsertParams) error {
	select {
	case b.started <- struct{}{}:
	default:
	}
	<-b.release
	return b.Store.Insert(ctx, p)
}

// bgBlockingGTD blocks LogActivity until release is closed.
type bgBlockingGTD struct {
	gtd.StoreIface
	started chan struct{}
	release chan struct{}
}

func (b *bgBlockingGTD) LogActivity(
	ctx context.Context, actor, action string, projectID *uuid.UUID, notes string,
) error {
	select {
	case b.started <- struct{}{}:
	default:
	}
	<-b.release
	return b.StoreIface.LogActivity(ctx, actor, action, projectID, notes)
}

// bgSlowDiscipline sleeps before Insert and records completion + error.
type bgSlowDiscipline struct {
	discipline.Store
	done   atomic.Bool
	errPtr atomic.Pointer[error]
}

func (b *bgSlowDiscipline) Insert(ctx context.Context, p discipline.InsertParams) error {
	time.Sleep(300 * time.Millisecond)
	err := b.Store.Insert(ctx, p)
	b.errPtr.Store(&err)
	b.done.Store(true)
	return err
}

// assertWaitBlocksUntilRelease checks that waitBackground does not return
// while the background write (signalled via started) is still blocked, and
// does return once release is closed. t.Fatal is only called on the test
// goroutine; the helper goroutine only calls waitBackground.
func assertWaitBlocksUntilRelease(
	t *testing.T, srv *Server, started <-chan struct{}, releaseOnce func(),
) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("background insert never started")
	}

	done := make(chan struct{})
	go func() {
		srv.waitBackground()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("waitBackground returned while an audit insert was still running")
	case <-time.After(200 * time.Millisecond):
	}

	releaseOnce()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("waitBackground did not return after the audit insert was released")
	}
}

func TestBackgroundWait_DisciplineInsert(t *testing.T) {
	srv, ms := newTestMCPServer(t)
	w := &bgBlockingDiscipline{
		Store:   srv.discipline,
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	srv.discipline = w
	var once sync.Once
	releaseOnce := func() { once.Do(func() { close(w.release) }) }
	t.Cleanup(releaseOnce) // LIFO: runs before newTestMCPServer's wait, so a failing test cannot hang cleanup

	isErr, text := unknownArgsRPCResult(t, ms, context.Background(), "get_today_context", "{}")
	if isErr {
		t.Fatalf("get_today_context returned isError: %s", text)
	}
	assertWaitBlocksUntilRelease(t, srv, w.started, releaseOnce)
}

func TestBackgroundWait_AutologSemaphorePath(t *testing.T) {
	srv, ms := newTestMCPServer(t)
	if srv.autologSem == nil {
		t.Fatal("precondition: New() must give a non-nil autologSem")
	}
	w := &bgBlockingGTD{
		StoreIface: srv.gtd,
		started:    make(chan struct{}, 1),
		release:    make(chan struct{}),
	}
	srv.gtd = w
	var once sync.Once
	releaseOnce := func() { once.Do(func() { close(w.release) }) }
	t.Cleanup(releaseOnce)

	isErr, text := unknownArgsRPCResult(t, ms, context.Background(), "set_session_handoff", `{"intent":"i"}`)
	if isErr {
		t.Fatalf("set_session_handoff returned isError: %s", text)
	}
	assertWaitBlocksUntilRelease(t, srv, w.started, releaseOnce)
}

func TestBackgroundWait_AutologUncappedPath(t *testing.T) {
	srv, ms := newTestMCPServer(t)
	srv.autologSem = nil // launchAutolog's no-semaphore branch
	w := &bgBlockingGTD{
		StoreIface: srv.gtd,
		started:    make(chan struct{}, 1),
		release:    make(chan struct{}),
	}
	srv.gtd = w
	var once sync.Once
	releaseOnce := func() { once.Do(func() { close(w.release) }) }
	t.Cleanup(releaseOnce)

	isErr, text := unknownArgsRPCResult(t, ms, context.Background(), "set_session_handoff", `{"intent":"i"}`)
	if isErr {
		t.Fatalf("set_session_handoff returned isError: %s", text)
	}
	assertWaitBlocksUntilRelease(t, srv, w.started, releaseOnce)
}

func TestBackgroundWait_TestServerCleanupWaits(t *testing.T) {
	var slow *bgSlowDiscipline
	t.Run("inner", func(t *testing.T) {
		srv, ms := newTestMCPServer(t)
		slow = &bgSlowDiscipline{Store: srv.discipline}
		srv.discipline = slow
		isErr, text := unknownArgsRPCResult(t, ms, context.Background(), "get_today_context", "{}")
		if isErr {
			t.Fatalf("get_today_context returned isError: %s", text)
		}
	})
	// inner's cleanups (wait, then close the store) have run by now.
	if !slow.done.Load() {
		t.Fatal("audit insert had not finished when the test server's store was closed")
	}
	p := slow.errPtr.Load()
	if p == nil {
		t.Fatal("audit insert recorded no result")
	}
	if *p != nil {
		t.Fatalf("audit insert failed: %v", *p)
	}
}
