package gtd_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// statusInProgress mirrors gtd.TaskStatusInProgress as a plain string for
// comparing against db.Task.Status (a bare string field), shared across this
// package's test files (goconst threshold: min-occurrences 3).
const statusInProgress = "in_progress"

// TestStore_CreateTask_InvalidAssignee_PG verifies the p6-7 domain-layer gate
// (sunk from the MCP-only guard in p6-6) rejects an unrecognized assignee
// value at CreateTask, regardless of caller. Paired with the SQLite variant.
func TestStore_CreateTask_InvalidAssignee_PG(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	_, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "bad assignee", Assignee: "gemini"})
	if err == nil {
		t.Fatal("CreateTask with unrecognized assignee must error")
	}
	if !errors.Is(err, gtd.ErrInvalidAssignee) {
		t.Errorf("expected gtd.ErrInvalidAssignee, got: %v", err)
	}
}

// TestStore_CreateTask_ValidAssigneeNormalized_PG verifies an alias spelling
// ("claude-code") is normalized to its canonical form ("claude") before
// persisting.
func TestStore_CreateTask_ValidAssigneeNormalized_PG(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "aliased assignee", Assignee: "claude-code"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if !task.Assignee.Valid || task.Assignee.String != "claude" {
		t.Errorf("assignee persisted = %+v, want normalized \"claude\"", task.Assignee)
	}
}

// TestStore_CreateTask_EmptyAssigneeAllowed_PG verifies an unowned (empty)
// assignee is still permitted — new tasks always insert as pending, so the
// in_progress guard never applies at creation time.
func TestStore_CreateTask_EmptyAssigneeAllowed_PG(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "unowned task"})
	if err != nil {
		t.Fatalf("CreateTask with no assignee must succeed: %v", err)
	}
	if task.Assignee.Valid {
		t.Errorf("expected NULL assignee, got %+v", task.Assignee)
	}
}

// TestStore_BeginTask_RequiresAssignee_PG verifies BeginTask rejects a task
// with no existing assignee — the domain-layer gate applied to a path that
// has no assignee argument of its own.
func TestStore_BeginTask_RequiresAssignee_PG(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "unowned begin"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	_, err = store.BeginTask(ctx, task.ID)
	if err == nil {
		t.Fatal("BeginTask on an unowned task must error")
	}
	if !errors.Is(err, gtd.ErrAssigneeRequiredForInProgress) {
		t.Errorf("expected gtd.ErrAssigneeRequiredForInProgress, got: %v", err)
	}

	reread, rerr := store.GetTaskByID(ctx, task.ID)
	if rerr != nil {
		t.Fatalf("GetTaskByID: %v", rerr)
	}
	if reread.Status != statusPending {
		t.Errorf("status changed to %q despite rejected BeginTask", reread.Status)
	}
}

// TestStore_BeginTask_ExistingAssigneeNotBlocked_PG verifies a task that
// already has an assignee begins normally with no extra plumbing.
func TestStore_BeginTask_ExistingAssigneeNotBlocked_PG(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "owned begin", Assignee: "human"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	got, err := store.BeginTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("BeginTask on an owned task must succeed: %v", err)
	}
	if got.Status != statusInProgress {
		t.Errorf("status = %q, want in_progress", got.Status)
	}
}

// TestStore_UpdateTaskStatus_RequiresAssignee_PG verifies UpdateTaskStatus
// rejects a pending→in_progress transition when the task has no assignee.
func TestStore_UpdateTaskStatus_RequiresAssignee_PG(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "unowned status"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	_, err = store.UpdateTaskStatus(ctx, task.ID, gtd.TaskStatusInProgress)
	if err == nil {
		t.Fatal("UpdateTaskStatus to in_progress on an unowned task must error")
	}
	if !errors.Is(err, gtd.ErrAssigneeRequiredForInProgress) {
		t.Errorf("expected gtd.ErrAssigneeRequiredForInProgress, got: %v", err)
	}
}

// TestStore_UpdateTaskStatus_NonInProgress_NoAssigneeNeeded_PG verifies the
// guard is scoped to in_progress only — cancelling an unowned task must not
// require an assignee.
func TestStore_UpdateTaskStatus_NonInProgress_NoAssigneeNeeded_PG(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "unowned cancel"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	got, err := store.UpdateTaskStatus(ctx, task.ID, gtd.TaskStatusCancelled)
	if err != nil {
		t.Fatalf("UpdateTaskStatus to cancelled on an unowned task must succeed: %v", err)
	}
	if got.Status != "cancelled" {
		t.Errorf("status = %q, want cancelled", got.Status)
	}
}

// TestStore_UpdateTask_InvalidAssignee_PG verifies UpdateTask rejects an
// unrecognized assignee value supplied on the call.
func TestStore_UpdateTask_InvalidAssignee_PG(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "bad update assignee"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	bad := "gemini"
	_, err = store.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{Assignee: &bad})
	if err == nil {
		t.Fatal("UpdateTask with unrecognized assignee must error")
	}
	if !errors.Is(err, gtd.ErrInvalidAssignee) {
		t.Errorf("expected gtd.ErrInvalidAssignee, got: %v", err)
	}
	for _, canonical := range gtd.CanonicalActors {
		if !strings.Contains(err.Error(), canonical) {
			t.Errorf("error should list canonical value %q, got: %v", canonical, err)
		}
	}
}

// TestStore_UpdateTask_InProgressNoAssignee_PG verifies UpdateTask rejects a
// status=in_progress patch that leaves the merged assignee empty.
func TestStore_UpdateTask_InProgressNoAssignee_PG(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "unowned update"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	status := string(gtd.TaskStatusInProgress)
	_, err = store.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{Status: &status})
	if err == nil {
		t.Fatal("UpdateTask to in_progress on an unowned task must error")
	}
	if !errors.Is(err, gtd.ErrAssigneeRequiredForInProgress) {
		t.Errorf("expected gtd.ErrAssigneeRequiredForInProgress, got: %v", err)
	}
}

// TestStore_UpdateTask_InProgressClearingAssignee_PG verifies UpdateTask
// rejects a call that simultaneously sets status=in_progress AND clears an
// EXISTING assignee to empty.
func TestStore_UpdateTask_InProgressClearingAssignee_PG(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "clearing assignee", Assignee: "claude"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	status := string(gtd.TaskStatusInProgress)
	empty := ""
	_, err = store.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{Status: &status, Assignee: &empty})
	if err == nil {
		t.Fatal("UpdateTask to in_progress while clearing assignee must error")
	}
	if !errors.Is(err, gtd.ErrAssigneeRequiredForInProgress) {
		t.Errorf("expected gtd.ErrAssigneeRequiredForInProgress, got: %v", err)
	}
}

// TestStore_UpdateTask_InProgressWithNewAssignee_PG verifies UpdateTask
// succeeds when assignee is supplied on the SAME call that sets in_progress.
func TestStore_UpdateTask_InProgressWithNewAssignee_PG(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "new assignee this call"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	status := string(gtd.TaskStatusInProgress)
	assignee := "codex"
	updated, err := store.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{Status: &status, Assignee: &assignee})
	if err != nil {
		t.Fatalf("UpdateTask with assignee supplied this call must succeed: %v", err)
	}
	if updated.Status != statusInProgress {
		t.Errorf("status = %q, want in_progress", updated.Status)
	}
	if !updated.Assignee.Valid || updated.Assignee.String != "codex" {
		t.Errorf("assignee = %+v, want \"codex\"", updated.Assignee)
	}
}

// TestStore_UpdateTask_InProgressWithExistingAssignee_PG verifies UpdateTask
// succeeds when the task already has an assignee and this call only changes
// status.
func TestStore_UpdateTask_InProgressWithExistingAssignee_PG(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "existing assignee", Assignee: "human"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	status := string(gtd.TaskStatusInProgress)
	updated, err := store.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{Status: &status})
	if err != nil {
		t.Fatalf("UpdateTask to in_progress on an already-owned task must succeed: %v", err)
	}
	if updated.Status != statusInProgress {
		t.Errorf("status = %q, want in_progress", updated.Status)
	}
	if !updated.Assignee.Valid || updated.Assignee.String != "human" {
		t.Errorf("assignee = %+v, want preserved \"human\"", updated.Assignee)
	}
}

// assertGuardedUpdateRejectsBlankPG seeds title as a pending task, writes
// assignee via raw SQL (bypassing CreateTask/UpdateTask normalization, so a
// nil pointer stores SQL NULL and any string — including whitespace-only —
// stores unchanged), then asserts the guarded UPDATE's own WHERE clause
// alone rejects it: db.New(pool).UpdateTaskStatusGuarded returns
// pgx.ErrNoRows and the row stays pending. Shared by
// TestStore_UpdateTaskStatusGuarded_SQLRejectsBlankAssignee_PG's fixed case
// table and its full-AssigneeSpaceChars-charset sweep so gocyclo counts one
// function instead of two duplicated branch chains.
func assertGuardedUpdateRejectsBlankPG(
	t *testing.T, pool *pgxpool.Pool, store *gtd.Store, ctx context.Context, title string, assignee *string,
) {
	t.Helper()
	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: title})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE tasks SET assignee = $1 WHERE id = $2`, assignee, task.ID); err != nil {
		t.Fatalf("seed raw assignee: %v", err)
	}

	_, err = db.New(pool).UpdateTaskStatusGuarded(ctx, db.UpdateTaskStatusGuardedParams{
		Status:         statusInProgress,
		ID:             task.ID,
		ExpectedStatus: statusPending,
		SpaceChars:     gtd.AssigneeSpaceChars,
		WorkspaceID:    store.WorkspaceID(),
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("UpdateTaskStatusGuarded err = %v, want pgx.ErrNoRows", err)
	}

	reread, rerr := store.GetTaskByID(ctx, task.ID)
	if rerr != nil {
		t.Fatalf("GetTaskByID: %v", rerr)
	}
	if reread.Status != statusPending {
		t.Errorf("status = %q, want pending (blank assignee must not reach in_progress)", reread.Status)
	}
}

// TestStore_UpdateTaskStatusGuarded_SQLRejectsBlankAssignee_PG verifies the
// guarded UPDATE's own WHERE clause — not just the Go pre-read in
// UpdateTaskStatusGuarded — rejects every blank-assignee variant
// RequireAssigneeForInProgress rejects, plus (in the full-charset sweep
// below) every single rune in AssigneeSpaceChars individually, since SV r3
// only verified the charset against the system sqlite3 CLI, not the actual
// Postgres btrim this backend runs in production.
func TestStore_UpdateTaskStatusGuarded_SQLRejectsBlankAssignee_PG(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	emptyStr := ""
	asciiSpace := " "
	tab := string(rune(0x09))
	ideographicSpace := string(rune(0x3000)) // U+3000, CJK fullwidth space

	cases := []struct {
		name     string
		assignee *string // nil = SQL NULL
	}{
		{name: "null", assignee: nil},
		{name: "empty string", assignee: &emptyStr},
		{name: "tab", assignee: &tab},
		{name: "ascii space", assignee: &asciiSpace},
		{name: "ideographic space U+3000", assignee: &ideographicSpace},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertGuardedUpdateRejectsBlankPG(t, pool, store, ctx, "blank assignee sql "+tc.name, tc.assignee)
		})
	}

	t.Run("legit actor succeeds", func(t *testing.T) {
		task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "legit assignee sql", Assignee: "claude"})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		row, err := db.New(pool).UpdateTaskStatusGuarded(ctx, db.UpdateTaskStatusGuardedParams{
			Status:         statusInProgress,
			ID:             task.ID,
			ExpectedStatus: statusPending,
			SpaceChars:     gtd.AssigneeSpaceChars,
			WorkspaceID:    store.WorkspaceID(),
		})
		if err != nil {
			t.Fatalf("UpdateTaskStatusGuarded with legit actor: %v", err)
		}
		if row.Status != statusInProgress {
			t.Errorf("status = %q, want in_progress", row.Status)
		}
	})

	// Full-charset sweep: SV r3 verified all 25 unicode.IsSpace runes trim
	// correctly against the system sqlite3 CLI, but production runs Postgres
	// btrim (this side) / modernc.org/sqlite (SQLite side), neither of which
	// had been exercised against the full AssigneeSpaceChars set on the
	// actual driver/engine wayneblacktea ships. One rune at a time so a
	// single divergent rune fails its own subtest instead of being masked by
	// the others.
	t.Run("full AssigneeSpaceChars charset", func(t *testing.T) {
		for _, r := range gtd.AssigneeSpaceChars {
			t.Run(fmt.Sprintf("U+%04X", r), func(t *testing.T) {
				raw := string(r)
				assertGuardedUpdateRejectsBlankPG(t, pool, store, ctx, fmt.Sprintf("charset sweep U+%04X", r), &raw)
			})
		}
	})
}

// TestStore_UpdateTaskStatusGuarded_RequiresAssignee_PG verifies the method
// itself (Go pre-read + SQL layer together) rejects a pending->in_progress
// transition when the task has no assignee, and succeeds when it does.
func TestStore_UpdateTaskStatusGuarded_RequiresAssignee_PG(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	t.Run("no assignee rejected", func(t *testing.T) {
		task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "guarded unowned"})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}

		_, err = store.UpdateTaskStatusGuarded(ctx, task.ID, gtd.TaskStatusInProgress, gtd.TaskStatusPending)
		if !errors.Is(err, gtd.ErrAssigneeRequiredForInProgress) {
			t.Fatalf("UpdateTaskStatusGuarded err = %v, want gtd.ErrAssigneeRequiredForInProgress", err)
		}

		reread, rerr := store.GetTaskByID(ctx, task.ID)
		if rerr != nil {
			t.Fatalf("GetTaskByID: %v", rerr)
		}
		if reread.Status != statusPending {
			t.Errorf("status = %q, want pending", reread.Status)
		}
	})

	t.Run("with assignee succeeds", func(t *testing.T) {
		task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "guarded owned", Assignee: "human"})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}

		got, err := store.UpdateTaskStatusGuarded(ctx, task.ID, gtd.TaskStatusInProgress, gtd.TaskStatusPending)
		if err != nil {
			t.Fatalf("UpdateTaskStatusGuarded: %v", err)
		}
		if got.Status != statusInProgress {
			t.Errorf("status = %q, want in_progress", got.Status)
		}
	})
}

// TestUpdateTask_WhitespaceOnlyAssigneeClears is [F0930-08]'s (D7) regression
// row: a whitespace-only assignee value (tab, NBSP, ideographic space — any
// unicode.IsSpace rune, not just literal "") must clear the assignee to
// NULL, the same as an explicit "". Before the fix, resolveAssigneeForUpdate
// skipped NormalizeActor for a TrimSpace-empty value (correctly) but then
// stored the raw whitespace string verbatim instead of collapsing it to "".
func TestUpdateTask_WhitespaceOnlyAssigneeClears(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	cases := []struct {
		name     string
		assignee string
	}{
		{name: "tab", assignee: "\t"},
		{name: "NBSP", assignee: " "},
		{name: "ideographic space U+3000", assignee: "　"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "whitespace clear " + tc.name, Assignee: "claude"})
			if err != nil {
				t.Fatalf("CreateTask: %v", err)
			}

			whitespace := tc.assignee
			updated, err := store.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{Assignee: &whitespace})
			if err != nil {
				t.Fatalf("UpdateTask with whitespace-only assignee must succeed (clear): %v", err)
			}
			if updated.Assignee.Valid {
				t.Errorf("assignee = %+v, want NULL (whitespace-only must clear, not persist raw)", updated.Assignee)
			}
		})
	}

	// Acceptance row 4: whitespace assignee AND simultaneously requesting
	// in_progress must still hit the P6.7 gate — the fix must not
	// accidentally bypass it by, say, treating a TrimSpace-empty value as
	// "no assignee supplied" (nil) instead of "explicit clear" ("").
	t.Run("in_progress with whitespace assignee still requires assignee", func(t *testing.T) {
		task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "whitespace in_progress"})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}

		status := string(gtd.TaskStatusInProgress)
		tab := "\t"
		_, err = store.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{Status: &status, Assignee: &tab})
		if err == nil {
			t.Fatal("UpdateTask to in_progress with a whitespace-only assignee must error")
		}
		if !errors.Is(err, gtd.ErrAssigneeRequiredForInProgress) {
			t.Errorf("expected gtd.ErrAssigneeRequiredForInProgress, got: %v", err)
		}
	})
}

// assertUpdateTaskStatusRejectsBlankPG mirrors assertGuardedUpdateRejectsBlankPG
// but exercises plain (non-guarded) UpdateTaskStatus's [F0930-09] assignee
// clause: seeds a pending task, writes assignee via raw SQL (bypassing
// CreateTask/UpdateTask normalization), then asserts the SQL WHERE clause
// alone — not any Go-layer pre-read — rejects a target status of
// in_progress when the row's assignee is blank.
func assertUpdateTaskStatusRejectsBlankPG(
	t *testing.T, pool *pgxpool.Pool, store *gtd.Store, ctx context.Context, title string, assignee *string,
) {
	t.Helper()
	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: title})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE tasks SET assignee = $1 WHERE id = $2`, assignee, task.ID); err != nil {
		t.Fatalf("seed raw assignee: %v", err)
	}

	_, err = db.New(pool).UpdateTaskStatus(ctx, db.UpdateTaskStatusParams{
		Status:      statusInProgress,
		ID:          task.ID,
		SpaceChars:  gtd.AssigneeSpaceChars,
		WorkspaceID: store.WorkspaceID(),
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("UpdateTaskStatus err = %v, want pgx.ErrNoRows", err)
	}

	reread, rerr := store.GetTaskByID(ctx, task.ID)
	if rerr != nil {
		t.Fatalf("GetTaskByID: %v", rerr)
	}
	if reread.Status != statusPending {
		t.Errorf("status = %q, want pending (blank assignee must not reach in_progress)", reread.Status)
	}
}

// TestUpdateTaskStatus_SQLRejectsBlankAssignee is [F0930-09/7abfae44]'s
// regression row: the plain (non-guarded) UpdateTaskStatus SQL must reject a
// target status of in_progress when the row's assignee is blank — the same
// write-time TOCTOU window SEC-196-02 closed for UpdateTaskStatusGuarded,
// applied here since plain UpdateTaskStatus's own Go-layer pre-read has the
// identical gap between read and write.
func TestUpdateTaskStatus_SQLRejectsBlankAssignee(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	emptyStr := ""
	tab := string(rune(0x09))
	ideographicSpace := string(rune(0x3000)) // U+3000, CJK fullwidth space

	cases := []struct {
		name     string
		assignee *string // nil = SQL NULL
	}{
		{name: "null", assignee: nil},
		{name: "empty string", assignee: &emptyStr},
		{name: "tab", assignee: &tab},
		{name: "ideographic space U+3000", assignee: &ideographicSpace},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertUpdateTaskStatusRejectsBlankPG(t, pool, store, ctx, "blank status sql "+tc.name, tc.assignee)
		})
	}

	t.Run("legit actor succeeds", func(t *testing.T) {
		task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "legit status sql", Assignee: "claude"})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		row, err := db.New(pool).UpdateTaskStatus(ctx, db.UpdateTaskStatusParams{
			Status:      statusInProgress,
			ID:          task.ID,
			SpaceChars:  gtd.AssigneeSpaceChars,
			WorkspaceID: store.WorkspaceID(),
		})
		if err != nil {
			t.Fatalf("UpdateTaskStatus with legit actor: %v", err)
		}
		if row.Status != statusInProgress {
			t.Errorf("status = %q, want in_progress", row.Status)
		}
	})

	// The assignee clause is conditional on the target status — every other
	// status must remain unaffected by a blank assignee (unlike
	// UpdateTaskStatusGuarded, plain UpdateTaskStatus also writes
	// non-in_progress targets).
	t.Run("non-in_progress target ignores assignee clause", func(t *testing.T) {
		task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "blank status sql non-in_progress"})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		row, err := db.New(pool).UpdateTaskStatus(ctx, db.UpdateTaskStatusParams{
			Status:      string(gtd.TaskStatusCancelled),
			ID:          task.ID,
			SpaceChars:  gtd.AssigneeSpaceChars,
			WorkspaceID: store.WorkspaceID(),
		})
		if err != nil {
			t.Fatalf("UpdateTaskStatus to cancelled with blank assignee must succeed: %v", err)
		}
		if row.Status != string(gtd.TaskStatusCancelled) {
			t.Errorf("status = %q, want cancelled", row.Status)
		}
	})
}

// assertBeginTaskStatusRejectsBlankPG seeds a pending task, writes assignee
// via raw SQL (bypassing CreateTask/UpdateTask normalization), then asserts
// BeginTaskStatus's [F0930-10] assignee clause — not any Go-layer pre-tx
// check — alone rejects it: 0 rows affected, row stays pending.
func assertBeginTaskStatusRejectsBlankPG(
	t *testing.T, pool *pgxpool.Pool, store *gtd.Store, ctx context.Context, title string, assignee *string,
) {
	t.Helper()
	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: title})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE tasks SET assignee = $1 WHERE id = $2`, assignee, task.ID); err != nil {
		t.Fatalf("seed raw assignee: %v", err)
	}

	_, err = db.New(pool).BeginTaskStatus(ctx, db.BeginTaskStatusParams{
		ID:          task.ID,
		WorkspaceID: uuid.UUID(store.WorkspaceID().Bytes),
		SpaceChars:  gtd.AssigneeSpaceChars,
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("BeginTaskStatus err = %v, want pgx.ErrNoRows", err)
	}

	reread, rerr := store.GetTaskByID(ctx, task.ID)
	if rerr != nil {
		t.Fatalf("GetTaskByID: %v", rerr)
	}
	if reread.Status != statusPending {
		t.Errorf("status = %q, want pending (blank assignee must not reach in_progress)", reread.Status)
	}
}

// TestBeginTaskStatus_SQLRejectsBlankAssignee is [F0930-10/7abfae44]'s
// regression row: BeginTaskStatus's guarded UPDATE must reject a task whose
// assignee is blank — the same write-time TOCTOU window SEC-196-02 closed
// for UpdateTaskStatusGuarded, applied here since BeginTask's pre-tx
// assignee check (ReadExisting, in begintask_orchestration.go) only sees the
// row as of that read, not as of this write.
func TestBeginTaskStatus_SQLRejectsBlankAssignee(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	emptyStr := ""
	tab := string(rune(0x09))
	ideographicSpace := string(rune(0x3000)) // U+3000, CJK fullwidth space

	cases := []struct {
		name     string
		assignee *string // nil = SQL NULL
	}{
		{name: "null", assignee: nil},
		{name: "empty string", assignee: &emptyStr},
		{name: "tab", assignee: &tab},
		{name: "ideographic space U+3000", assignee: &ideographicSpace},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertBeginTaskStatusRejectsBlankPG(t, pool, store, ctx, "begin blank sql "+tc.name, tc.assignee)
		})
	}

	t.Run("legit actor succeeds", func(t *testing.T) {
		task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "begin legit sql", Assignee: "claude"})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		row, err := db.New(pool).BeginTaskStatus(ctx, db.BeginTaskStatusParams{
			ID:          task.ID,
			WorkspaceID: uuid.UUID(store.WorkspaceID().Bytes),
			SpaceChars:  gtd.AssigneeSpaceChars,
		})
		if err != nil {
			t.Fatalf("BeginTaskStatus with legit actor: %v", err)
		}
		if row.Status != statusInProgress {
			t.Errorf("status = %q, want in_progress", row.Status)
		}
	})
}
