package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/discipline"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

// fakeRestoreProjectStore embeds noopGTDStore (tools_contextpack_fakes_test.go)
// for every gtd.StoreIface method restore_project's handler never calls, and
// overrides RestoreProject so each test controls what it returns and
// captures the id/actor the handler actually passed through — [F191-07]
// tests exercise only the MCP handler's own logic (routing, error
// translation, actor provenance) against a scripted result, leaving the
// real tombstone-backed restore logic to the store-layer tests instead.
type fakeRestoreProjectStore struct {
	noopGTDStore
	project       *db.Project
	tasksRestored int
	err           error

	called   bool
	gotID    uuid.UUID
	gotActor string
}

func (f *fakeRestoreProjectStore) RestoreProject(_ context.Context, id uuid.UUID, actor string) (*db.Project, int, error) {
	f.called = true
	f.gotID = id
	f.gotActor = actor
	return f.project, f.tasksRestored, f.err
}

// TestRestoreProjectTool_IsInHiddenGTDGroup pins the rule that
// restore_project must not be advertised in tools/list by default, only
// reachable via expand_tools(group="gtd").
func TestRestoreProjectTool_IsInHiddenGTDGroup(t *testing.T) {
	if coreToolSet["restore_project"] {
		t.Error("restore_project must not be in coreToolNames — it belongs in the hidden \"gtd\" group only")
	}
	inGTDGroup := false
	for _, name := range toolGroupIndex["gtd"] {
		if name == "restore_project" {
			inGTDGroup = true
			break
		}
	}
	if !inGTDGroup {
		t.Error(`restore_project must be listed in toolgroups.go's "gtd" toolGroup`)
	}
}

// TestRestoreProjectTool_IsMutating pins design 5: restore_project writes
// project/task rows back from deletion_tombstones and must trip drift
// detection like every other write tool.
func TestRestoreProjectTool_IsMutating(t *testing.T) {
	if !discipline.MutatingTools["restore_project"] {
		t.Error("restore_project must be registered in discipline.MutatingTools")
	}
}

// TestRestoreProjectTool_ClipsProjectName proves the response's name field
// is routed through the same clipSafe(_, gtdTitleMaxRunes) boundary
// renderer handleDeleteProject's preview uses (U13) — not the fake store's
// raw, unbounded value.
func TestRestoreProjectTool_ClipsProjectName(t *testing.T) {
	long := strings.Repeat("x", gtdTitleMaxRunes+500)
	id := uuid.New()
	fake := &fakeRestoreProjectStore{project: &db.Project{ID: id, Name: long}, tasksRestored: 2}
	s := &Server{gtd: fake, sessionID: "test-session"}

	r, err := s.handleRestoreProject(context.Background(), RestoreProjectArgs{ProjectID: id})
	if err != nil {
		t.Fatalf("handleRestoreProject: %v", err)
	}
	if r.IsError {
		t.Fatalf("expected success, got error: %s", resultText(r))
	}

	var payload struct {
		RestoredProjectID string `json:"restored_project_id"`
		Name              string `json:"name"`
		TasksRestored     int    `json:"tasks_restored"`
	}
	if err := json.Unmarshal([]byte(resultText(r)), &payload); err != nil {
		t.Fatalf("unmarshal: %v\nraw=%s", err, resultText(r))
	}
	want := clipSafe(long, gtdTitleMaxRunes)
	if payload.Name != want {
		t.Errorf("name = %q (%d runes), want clipSafe output %q (%d runes)",
			payload.Name, utf8.RuneCountInString(payload.Name), want, utf8.RuneCountInString(want))
	}
	if !strings.HasSuffix(payload.Name, clipMarker) {
		t.Errorf("expected the over-cap name to end with clipMarker %q, got %q", clipMarker, payload.Name)
	}
	if payload.TasksRestored != 2 {
		t.Errorf("tasks_restored = %d, want 2", payload.TasksRestored)
	}
	if payload.RestoredProjectID != id.String() {
		t.Errorf("restored_project_id = %q, want %q", payload.RestoredProjectID, id.String())
	}
}

// TestRestoreProjectTool_NotFoundMessage pins design 2's exact wording for
// gtd.ErrNotFound — the message must not echo any caller- or store-supplied
// text.
func TestRestoreProjectTool_NotFoundMessage(t *testing.T) {
	id := uuid.New()
	fake := &fakeRestoreProjectStore{err: gtd.ErrNotFound}
	s := &Server{gtd: fake, sessionID: "test-session"}

	r, err := s.handleRestoreProject(context.Background(), RestoreProjectArgs{ProjectID: id})
	if err != nil {
		t.Fatalf("handleRestoreProject: %v", err)
	}
	if !r.IsError {
		t.Fatalf("expected an error result, got success: %s", resultText(r))
	}
	const want = "no deleted project with that id within the retention window"
	if resultText(r) != want {
		t.Errorf("message = %q, want exactly %q", resultText(r), want)
	}
}

// TestRestoreProjectTool_ConflictMessage pins design 2's exact wording for
// gtd.ErrConflict, and that it never echoes the underlying error text (the
// mutation described in the dispatch's acceptance list — deleting this
// branch so it falls through to the generic storeErrorResult path — turns
// this test red).
func TestRestoreProjectTool_ConflictMessage(t *testing.T) {
	id := uuid.New()
	wrapped := fmt.Errorf("duplicate key value violates unique constraint %q: %w", "projects_name_key", gtd.ErrConflict)
	fake := &fakeRestoreProjectStore{err: wrapped}
	s := &Server{gtd: fake, sessionID: "test-session"}

	r, err := s.handleRestoreProject(context.Background(), RestoreProjectArgs{ProjectID: id})
	if err != nil {
		t.Fatalf("handleRestoreProject: %v", err)
	}
	if !r.IsError {
		t.Fatalf("expected an error result, got success: %s", resultText(r))
	}
	const want = "a project with that id or name already exists; nothing was restored"
	if resultText(r) != want {
		t.Errorf("message = %q, want exactly %q", resultText(r), want)
	}
	if strings.Contains(resultText(r), "projects_name_key") || strings.Contains(resultText(r), "unique constraint") {
		t.Errorf("conflict message leaked the underlying store error text: %s", resultText(r))
	}
}

// TestRestoreProjectTool_NotImplementedIsAnError guards the fallback error
// path: any store error that is neither ErrNotFound nor ErrConflict MUST be
// reported as a plain error, never as success and never mistranslated into
// the not-found message above. gtd.ErrNotImplemented (a sentinel test fakes
// use to model exactly such an uncategorised error) does not satisfy
// errors.Is(err, ErrNotFound), so it exercises that fallback path here.
func TestRestoreProjectTool_NotImplementedIsAnError(t *testing.T) {
	id := uuid.New()
	fake := &fakeRestoreProjectStore{err: gtd.ErrNotImplemented}
	s := &Server{gtd: fake, sessionID: "test-session"}

	r, err := s.handleRestoreProject(context.Background(), RestoreProjectArgs{ProjectID: id})
	if err != nil {
		t.Fatalf("handleRestoreProject: %v", err)
	}
	if !r.IsError {
		t.Fatalf("ErrNotImplemented must be reported as an error, not translated into success: %s", resultText(r))
	}
	if resultText(r) == "no deleted project with that id within the retention window" {
		t.Error("ErrNotImplemented was mistranslated into the not-found message")
	}
}

// TestRestoreProjectTool_ActorComesFromSession proves actor is always
// s.auditSessionID(ctx) — RestoreProjectArgs has no field that could carry
// a caller-supplied actor at all, so this pins the property at the one
// place it could still leak in: the handler reading something other than
// auditSessionID's return value.
func TestRestoreProjectTool_ActorComesFromSession(t *testing.T) {
	id := uuid.New()
	fake := &fakeRestoreProjectStore{project: &db.Project{ID: id, Name: "p"}, tasksRestored: 0}
	s := &Server{gtd: fake, sessionID: "session-identity-not-a-tool-arg"}

	if _, err := s.handleRestoreProject(context.Background(), RestoreProjectArgs{ProjectID: id}); err != nil {
		t.Fatalf("handleRestoreProject: %v", err)
	}
	if !fake.called {
		t.Fatal("RestoreProject was never called")
	}
	if fake.gotActor != "session-identity-not-a-tool-arg" {
		t.Errorf("actor = %q, want the session identity %q", fake.gotActor, "session-identity-not-a-tool-arg")
	}
	if fake.gotID != id {
		t.Errorf("id passed to the store = %s, want %s", fake.gotID, id)
	}
}
