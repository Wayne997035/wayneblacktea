//go:build integration

package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

// captureStdout redirects os.Stdout for the duration of fn and returns
// everything written to it. RunDoctor's emitDoctor writes JSON via
// fmt.Println (doctor_cmd.go's emitDoctor), which is the only way to
// observe it.
func captureStdout(t *testing.T, fn func()) []byte {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	return out
}

// TestRunDoctor_WiresAllFourCollectors closes the testing-reality-checker
// Minor finding that deleting the collectDeliveryVisibility wiring line
// (doctor_cmd.go:138) changes neither the build nor any existing test:
// seeds one row per collector against a real, reachable Postgres
// testcontainer, then asserts RunDoctor's emitted snapshot reflects every
// seeded row rather than the fail-soft default — an unreachable DATABASE_URL
// produces the exact same zero-value JSON whether or not any given
// collector's wiring line is present, so this needs real data to be a
// meaningful wiring test. [F0929-64]
func TestRunDoctor_WiresAllFourCollectors(t *testing.T) {
	pool, dsn := newTestPgPoolAndDSN(t)
	ws := uuid.New()
	ctx := context.Background()

	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("WORKSPACE_ID", ws.String())
	t.Setenv("APP_ENV", "")
	t.Setenv("PGSSLROOTCERT", "")
	t.Setenv("ANTHROPIC_API_KEY", "") // skip the session-summary AI call path

	// stdin: empty pipe, closed immediately — guarantees RunDoctor's
	// processDoctorSummary sees len(raw)==0 regardless of how `go test`
	// itself was invoked (a live TTY on stdin would otherwise hang it).
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe (stdin): %v", err)
	}
	if err := stdinW.Close(); err != nil {
		t.Fatalf("close stdin pipe writer: %v", err)
	}
	origStdin := os.Stdin
	os.Stdin = stdinR
	defer func() { os.Stdin = origStdin }()

	gtdStore := gtd.NewStore(pool, &ws)

	// collectTaskHealth: one stuck in_progress task (updated_at backdated
	// past the 4h threshold). Assignee is required by BeginTask before a
	// task can move to in_progress, and must be a recognized actor
	// (internal/gtd/actor.go: claude, codex, human).
	stuckTask, err := gtdStore.CreateTask(ctx, gtd.CreateTaskParams{Title: "stuck task", Assignee: "human"})
	if err != nil {
		t.Fatalf("CreateTask(stuck): %v", err)
	}
	if _, err := gtdStore.BeginTask(ctx, stuckTask.ID); err != nil {
		t.Fatalf("BeginTask: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE tasks SET updated_at = NOW() - INTERVAL '5 hours' WHERE id = $1`, stuckTask.ID); err != nil {
		t.Fatalf("backdate stuck task: %v", err)
	}

	// collectProposalCount: one pending proposal.
	if _, err := pool.Exec(ctx, `INSERT INTO pending_proposals (workspace_id, type, payload) VALUES ($1, 'task', '{}'::jsonb)`, ws); err != nil {
		t.Fatalf("seed pending_proposals: %v", err)
	}

	// collectDueReviewCount: one concept due for review (due_date in the past).
	var conceptID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO concepts (title, content, workspace_id) VALUES ('c', 'body', $1) RETURNING id`, ws).Scan(&conceptID); err != nil {
		t.Fatalf("seed concepts: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO review_schedule (concept_id, due_date, workspace_id) VALUES ($1, NOW() - INTERVAL '1 hour', $2)`, conceptID, ws); err != nil {
		t.Fatalf("seed review_schedule: %v", err)
	}

	// collectDeliveryVisibility: one goal due tomorrow, one top-priority task.
	tomorrow := time.Now().UTC().AddDate(0, 0, 1)
	if _, err := gtdStore.CreateGoal(ctx, gtd.CreateGoalParams{Title: "ship P1", DueDate: &tomorrow}); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if _, err := gtdStore.CreateTask(ctx, gtd.CreateTaskParams{Title: "top-prio", Priority: 1}); err != nil {
		t.Fatalf("CreateTask(top-prio): %v", err)
	}

	stdout := captureStdout(t, func() {
		if err := RunDoctor(nil); err != nil {
			t.Fatalf("RunDoctor: %v", err)
		}
	})

	var snap DoctorSnapshot
	if err := json.Unmarshal(stdout, &snap); err != nil {
		t.Fatalf("decode RunDoctor stdout JSON: %v\n--- stdout ---\n%s", err, stdout)
	}

	if snap.StuckCount != 1 {
		t.Errorf("StuckCount = %d, want 1 — collectTaskHealth wiring (doctor_cmd.go:135)", snap.StuckCount)
	}
	if snap.PendingProposals != 1 {
		t.Errorf("PendingProposals = %d, want 1 — collectProposalCount wiring (doctor_cmd.go:136)", snap.PendingProposals)
	}
	if snap.DueReviews != 1 {
		t.Errorf("DueReviews = %d, want 1 — collectDueReviewCount wiring (doctor_cmd.go:137)", snap.DueReviews)
	}
	if len(snap.GoalsDue) != 1 || snap.GoalsDue[0].Title != "ship P1" {
		t.Errorf("GoalsDue = %+v, want 1 entry titled %q — collectDeliveryVisibility wiring (doctor_cmd.go:138)", snap.GoalsDue, "ship P1")
	}
	if snap.TopPending == nil || snap.TopPending.Title != "top-prio" {
		t.Errorf("TopPending = %+v, want title %q — collectDeliveryVisibility wiring (doctor_cmd.go:138)", snap.TopPending, "top-prio")
	}
}
