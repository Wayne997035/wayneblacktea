package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/google/uuid"
)

// fakePostToolUseGTDStore records LogActivity calls and can be configured to fail.
// It also satisfies the Tasks / CreateTask parts of autologGTDStore (unused by PostToolUseHandler).
type fakePostToolUseGTDStore struct {
	mu     sync.Mutex
	logged []ptuLogCall
	logErr error
}

type ptuLogCall struct {
	Actor  string
	Action string
	Notes  string
}

func (f *fakePostToolUseGTDStore) LogActivity(_ context.Context, actor, action string, _ *uuid.UUID, notes string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logged = append(f.logged, ptuLogCall{Actor: actor, Action: action, Notes: notes})
	return f.logErr
}

func (f *fakePostToolUseGTDStore) Tasks(_ context.Context, _ *uuid.UUID) ([]db.Task, error) {
	return nil, nil
}

func (f *fakePostToolUseGTDStore) CreateTask(_ context.Context, _ gtd.CreateTaskParams) (*db.Task, error) {
	return nil, nil
}

// ---- test helpers ----

func waitForLogged(store *fakePostToolUseGTDStore, n int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		store.mu.Lock()
		count := len(store.logged)
		store.mu.Unlock()
		if count >= n {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// ---- test cases ----

func TestPostToolUseHandler_Success(t *testing.T) {
	store := &fakePostToolUseGTDStore{}
	h := handler.NewPostToolUseHandler(store)
	defer h.Stop()

	e := newEcho()
	e.POST("/api/activity/posttooluse", h.PostToolUse)

	rec := performRequest(e, http.MethodPost, "/api/activity/posttooluse",
		`{"tool_name":"Bash","notes":"sha256:abc123"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("got %d, want 202 (body: %s)", rec.Code, rec.Body.String())
	}

	if !waitForLogged(store, 1, 500*time.Millisecond) {
		t.Fatal("expected activity to be logged, got 0 within 500ms")
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	got := store.logged[0]
	if got.Actor != "claude-code" {
		t.Errorf("actor = %q, want claude-code", got.Actor)
	}
	if got.Action != "Bash" {
		t.Errorf("action = %q, want Bash", got.Action)
	}
	if got.Notes != "sha256:abc123" {
		t.Errorf("notes = %q, want sha256:abc123", got.Notes)
	}
}

func TestPostToolUseHandler_MissingToolName_400(t *testing.T) {
	store := &fakePostToolUseGTDStore{}
	h := handler.NewPostToolUseHandler(store)
	defer h.Stop()

	e := newEcho()
	e.POST("/api/activity/posttooluse", h.PostToolUse)

	rec := performRequest(e, http.MethodPost, "/api/activity/posttooluse",
		`{"actor":"claude-code","notes":"sha256:abc"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestPostToolUseHandler_InvalidJSON_400(t *testing.T) {
	store := &fakePostToolUseGTDStore{}
	h := handler.NewPostToolUseHandler(store)
	defer h.Stop()

	e := newEcho()
	e.POST("/api/activity/posttooluse", h.PostToolUse)

	rec := performRequest(e, http.MethodPost, "/api/activity/posttooluse", `{bad json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", rec.Code)
	}
}

func TestPostToolUseHandler_StoreError_Returns202(t *testing.T) {
	// When LogActivity returns an error the HTTP response MUST still be 202
	// (fire-and-forget; the error is logged as a slog.Warn, not surfaced).
	store := &fakePostToolUseGTDStore{logErr: errors.New("db write fail")}
	h := handler.NewPostToolUseHandler(store)
	defer h.Stop()

	e := newEcho()
	e.POST("/api/activity/posttooluse", h.PostToolUse)

	rec := performRequest(e, http.MethodPost, "/api/activity/posttooluse",
		`{"tool_name":"Edit","notes":"sha256:deadbeef"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("got %d, want 202 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestPostToolUseHandler_NotesLengthCapped(t *testing.T) {
	// Notes longer than maxNotesLen (2000) must be silently truncated before enqueue.
	// We use notes of exactly 3000 chars — long enough to exceed 2000 but short enough
	// to fit inside the 4 KB body limit after JSON encoding.
	store := &fakePostToolUseGTDStore{}
	h := handler.NewPostToolUseHandler(store)
	defer h.Stop()

	e := newEcho()
	e.POST("/api/activity/posttooluse", h.PostToolUse)

	longNotes := strings.Repeat("a", 3000)
	bodyMap := map[string]string{"tool_name": "Read", "notes": longNotes}
	bodyBytes, err := json.Marshal(bodyMap)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	rec := performRequest(e, http.MethodPost, "/api/activity/posttooluse", string(bodyBytes))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("got %d, want 202 (body: %s)", rec.Code, rec.Body.String())
	}

	if !waitForLogged(store, 1, 500*time.Millisecond) {
		t.Fatal("expected activity to be logged")
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.logged[0].Notes) > 2000 {
		t.Errorf("notes length %d exceeds maxNotesLen 2000", len(store.logged[0].Notes))
	}
}

func TestPostToolUseHandler_ChannelFull_DropsGracefully(t *testing.T) {
	// Enqueue more events than channel capacity (1000).
	// The handler MUST return 202 for every request and MUST NOT block or panic.
	store := &fakePostToolUseGTDStore{}
	h := handler.NewPostToolUseHandler(store)
	defer h.Stop()

	e := newEcho()
	e.POST("/api/activity/posttooluse", h.PostToolUse)

	for i := 0; i < 1100; i++ {
		rec := performRequest(e, http.MethodPost, "/api/activity/posttooluse",
			`{"tool_name":"Bash","notes":"sha256:flood"}`)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("request %d: got %d, want 202", i, rec.Code)
		}
	}
}
