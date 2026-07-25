package sqlite_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// These tests exercise the ImportX methods added for cmd/qa-seed: unlike
// CreateGoal/CreateProject/CreateTask/Log/Create (which always mint a fresh
// UUID + timestamp), ImportX must preserve every field on the input row
// verbatim — that fidelity is the entire point of the qa-seed tool, so each
// test asserts against the raw column values (not just the Go struct
// scanned back through the app's normal read path, which may not surface
// every column — e.g. tasksSelectCols omits checklist/vision_item_id).

func pgUUIDVal(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: [16]byte(id), Valid: true}
}

func pgTextVal(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: true}
}

func pgTimeVal(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func openGTDDB(t *testing.T) (*sqlite.DB, *sqlite.GTDStore) {
	t.Helper()
	d, err := sqlite.Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d, sqlite.NewGTDStore(d)
}

func TestGTDStore_ImportGoal(t *testing.T) {
	fixed := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	due := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		goal db.Goal
	}{
		{
			name: "archived goal with every optional field set",
			goal: db.Goal{
				ID: uuid.New(), Title: "Ship v2", Description: pgTextVal("desc"),
				Status: "archived", Area: pgTextVal("engineering"),
				DueDate: pgTimeVal(due), CreatedAt: pgTimeVal(fixed), UpdatedAt: pgTimeVal(fixed),
			},
		},
		{
			name: "active goal with no optional fields — nulls roundtrip",
			goal: db.Goal{
				ID: uuid.New(), Title: "Minimal goal", Status: "active",
				CreatedAt: pgTimeVal(fixed), UpdatedAt: pgTimeVal(fixed),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, s := openGTDDB(t)
			ctx := context.Background()
			if err := s.ImportGoal(ctx, tt.goal); err != nil {
				t.Fatalf("ImportGoal: %v", err)
			}

			var status string
			var desc, area, due sql.NullString
			row := d.QueryRowContext(ctx, `SELECT status, description, area, due_date FROM goals WHERE id = ?1`, tt.goal.ID.String())
			if err := row.Scan(&status, &desc, &area, &due); err != nil {
				t.Fatalf("verify scan: %v", err)
			}
			// Import must preserve the original status (e.g. "archived"),
			// not silently reset it to CreateGoal's implicit 'active' default.
			if status != tt.goal.Status {
				t.Errorf("status = %q, want %q", status, tt.goal.Status)
			}
			if desc.Valid != tt.goal.Description.Valid {
				t.Errorf("description valid = %v, want %v", desc.Valid, tt.goal.Description.Valid)
			}
			if area.Valid != tt.goal.Area.Valid {
				t.Errorf("area valid = %v, want %v", area.Valid, tt.goal.Area.Valid)
			}
			if due.Valid != tt.goal.DueDate.Valid {
				t.Errorf("due_date valid = %v, want %v", due.Valid, tt.goal.DueDate.Valid)
			}
		})
	}
}

func TestGTDStore_ImportGoal_DuplicateIDFails(t *testing.T) {
	_, s := openGTDDB(t)
	ctx := context.Background()
	g := db.Goal{ID: uuid.New(), Title: "dup", Status: "active", CreatedAt: pgTimeVal(time.Now()), UpdatedAt: pgTimeVal(time.Now())}

	if err := s.ImportGoal(ctx, g); err != nil {
		t.Fatalf("first ImportGoal: %v", err)
	}
	if err := s.ImportGoal(ctx, g); err == nil {
		t.Fatal("re-importing a duplicate id: expected error, got nil (Import must never silently upsert)")
	}
}

func TestGTDStore_ImportProject(t *testing.T) {
	fixed := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)
	goalID := uuid.New()

	tests := []struct {
		name    string
		project db.Project
	}{
		{
			name: "project linked to a goal, on_hold status",
			project: db.Project{
				ID: uuid.New(), GoalID: pgUUIDVal(goalID), Name: "proj-a", Title: "Project A",
				Status: "on_hold", Area: "engineering", Priority: 1, RepoName: pgTextVal("wayneblacktea"),
				CreatedAt: pgTimeVal(fixed), UpdatedAt: pgTimeVal(fixed),
			},
		},
		{
			name: "project with no goal, no repo — nulls roundtrip",
			project: db.Project{
				ID: uuid.New(), Name: "proj-b", Title: "Project B",
				Status: "active", Area: "projects", Priority: 3,
				CreatedAt: pgTimeVal(fixed), UpdatedAt: pgTimeVal(fixed),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, s := openGTDDB(t)
			ctx := context.Background()
			if err := s.ImportProject(ctx, tt.project); err != nil {
				t.Fatalf("ImportProject: %v", err)
			}

			var status string
			var gid, repo sql.NullString
			row := d.QueryRowContext(ctx, `SELECT status, goal_id, repo_name FROM projects WHERE id = ?1`, tt.project.ID.String())
			if err := row.Scan(&status, &gid, &repo); err != nil {
				t.Fatalf("verify scan: %v", err)
			}
			if status != tt.project.Status {
				t.Errorf("status = %q, want %q", status, tt.project.Status)
			}
			if gid.Valid != tt.project.GoalID.Valid {
				t.Errorf("goal_id valid = %v, want %v", gid.Valid, tt.project.GoalID.Valid)
			}
			if tt.project.GoalID.Valid && gid.String != goalID.String() {
				t.Errorf("goal_id = %q, want %q (relationship must survive import verbatim)", gid.String, goalID.String())
			}
			if repo.Valid != tt.project.RepoName.Valid {
				t.Errorf("repo_name valid = %v, want %v", repo.Valid, tt.project.RepoName.Valid)
			}
		})
	}
}

func TestGTDStore_ImportProject_DuplicateIDFails(t *testing.T) {
	_, s := openGTDDB(t)
	ctx := context.Background()
	p := db.Project{
		ID: uuid.New(), Name: "dup-proj", Title: "dup", Status: "active", Area: "projects", Priority: 3,
		CreatedAt: pgTimeVal(time.Now()), UpdatedAt: pgTimeVal(time.Now()),
	}
	if err := s.ImportProject(ctx, p); err != nil {
		t.Fatalf("first ImportProject: %v", err)
	}
	if err := s.ImportProject(ctx, p); err == nil {
		t.Fatal("re-importing a duplicate id: expected error, got nil")
	}
}

func TestGTDStore_ImportTask(t *testing.T) {
	fixed := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	projectID := uuid.New()
	visionID := uuid.New()
	importance := pgtype.Int2{Int16: 1, Valid: true}

	tests := []struct {
		name string
		task db.Task
	}{
		{
			name: "completed task with checklist, branch/PR, commit shas, vision link",
			task: db.Task{
				ID: uuid.New(), ProjectID: pgUUIDVal(projectID), Title: "Fix bug", Status: "completed",
				Priority: 1, Importance: importance, Assignee: pgTextVal("claude"),
				Artifact: pgTextVal("PR #42"), Kind: "fix-pr",
				BranchName: pgTextVal("fix/bug-42"), PRUrl: pgTextVal("https://github.com/x/y/pull/42"),
				CommitSHAs:   []string{"abc123", "def456"},
				Checklist:    []byte(`[{"id":"11111111-1111-1111-1111-111111111111","title":"step1","done":true}]`),
				VisionItemID: pgUUIDVal(visionID),
				CreatedAt:    pgTimeVal(fixed), UpdatedAt: pgTimeVal(fixed),
			},
		},
		{
			name: "minimal pending task — no project, no optional fields",
			task: db.Task{
				ID: uuid.New(), Title: "Bare task", Status: "pending", Priority: 3,
				CreatedAt: pgTimeVal(fixed), UpdatedAt: pgTimeVal(fixed),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, s := openGTDDB(t)
			ctx := context.Background()
			if err := s.ImportTask(ctx, tt.task); err != nil {
				t.Fatalf("ImportTask: %v", err)
			}

			var status, kind, commitSHAs, checklist string
			var pid, branch, prURL, vision sql.NullString
			row := d.QueryRowContext(ctx,
				`SELECT status, project_id, kind, branch_name, pr_url, commit_shas, checklist, vision_item_id FROM tasks WHERE id = ?1`,
				tt.task.ID.String())
			if err := row.Scan(&status, &pid, &kind, &branch, &prURL, &commitSHAs, &checklist, &vision); err != nil {
				t.Fatalf("verify scan: %v", err)
			}
			if status != tt.task.Status {
				t.Errorf("status = %q, want %q", status, tt.task.Status)
			}
			if pid.Valid != tt.task.ProjectID.Valid {
				t.Errorf("project_id valid = %v, want %v", pid.Valid, tt.task.ProjectID.Valid)
			}
			wantKind := tt.task.Kind
			if wantKind == "" {
				wantKind = "general"
			}
			if kind != wantKind {
				t.Errorf("kind = %q, want %q", kind, wantKind)
			}
			if branch.Valid != tt.task.BranchName.Valid {
				t.Errorf("branch_name valid = %v, want %v", branch.Valid, tt.task.BranchName.Valid)
			}
			if vision.Valid != tt.task.VisionItemID.Valid {
				t.Errorf("vision_item_id valid = %v, want %v", vision.Valid, tt.task.VisionItemID.Valid)
			}
			wantCommitSHAs := "[]"
			if len(tt.task.CommitSHAs) > 0 {
				wantCommitSHAs = `["abc123","def456"]`
			}
			if commitSHAs != wantCommitSHAs {
				t.Errorf("commit_shas = %q, want %q", commitSHAs, wantCommitSHAs)
			}
			wantChecklist := "[]"
			if len(tt.task.Checklist) > 0 {
				wantChecklist = string(tt.task.Checklist)
			}
			if checklist != wantChecklist {
				t.Errorf("checklist = %q, want %q", checklist, wantChecklist)
			}
		})
	}
}

func TestGTDStore_ImportTask_DuplicateIDFails(t *testing.T) {
	_, s := openGTDDB(t)
	ctx := context.Background()
	task := db.Task{
		ID: uuid.New(), Title: "dup", Status: "pending", Priority: 3,
		CreatedAt: pgTimeVal(time.Now()), UpdatedAt: pgTimeVal(time.Now()),
	}

	if err := s.ImportTask(ctx, task); err != nil {
		t.Fatalf("first ImportTask: %v", err)
	}
	if err := s.ImportTask(ctx, task); err == nil {
		t.Fatal("re-importing a duplicate id: expected error, got nil")
	}
}
