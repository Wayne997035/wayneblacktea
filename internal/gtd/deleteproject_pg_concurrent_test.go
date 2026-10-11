package gtd_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

// moveInBeforeDelete wraps the real Postgres adapter. Right before the DELETE
// runs, after the delete transaction has begun, it commits a move of another
// task into the project on a different pool connection. [PR #200 SE-01]
type moveInBeforeDelete struct {
	gtd.DeleteProjectAdapter
	move func(context.Context) error
}

func (w *moveInBeforeDelete) DeleteTaskRows(ctx context.Context) ([]uuid.UUID, error) {
	if err := w.move(ctx); err != nil {
		return nil, err
	}
	return w.DeleteProjectAdapter.DeleteTaskRows(ctx)
}

func TestDeleteProjectOrchestration_PGConcurrentMoveInRefused(t *testing.T) {
	pool := openTestPgPool(t)
	ws := uuid.New()
	store := newPgGTDStore(pool, &ws)
	ctx := context.Background()

	target, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: "se01-target", Title: "t"})
	if err != nil {
		t.Fatalf("CreateProject target: %v", err)
	}
	other, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: "se01-other", Title: "o"})
	if err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	kept, err := store.CreateTask(ctx, gtd.CreateTaskParams{ProjectID: &target.ID, Title: "previewed"})
	if err != nil {
		t.Fatalf("CreateTask kept: %v", err)
	}
	mover, err := store.CreateTask(ctx, gtd.CreateTaskParams{ProjectID: &other.ID, Title: "moved in mid-delete"})
	if err != nil {
		t.Fatalf("CreateTask mover: %v", err)
	}

	ids, err := store.TaskIDsByProjectAllStatuses(ctx, target.ID)
	if err != nil {
		t.Fatalf("TaskIDsByProjectAllStatuses: %v", err)
	}
	preview := gtd.TaskSetDigest(ids)

	moved := false
	w := &moveInBeforeDelete{
		DeleteProjectAdapter: gtd.NewPGDeleteProjectAdapterForTest(store, target.ID),
		move: func(ctx context.Context) error {
			moved = true
			_, err := store.UpdateTask(ctx, mover.ID, gtd.UpdateTaskParams{Project: &gtd.ProjectRef{ID: &target.ID}})
			return err
		},
	}
	n, err := gtd.DeleteProjectOrchestration(ctx, target.ID, "se01-probe", time.Now().UTC(), preview, w)
	if !moved {
		t.Fatal("probe broken: the concurrent move never ran")
	}
	if !errors.Is(err, gtd.ErrProjectTasksChanged) {
		t.Fatalf("DeleteProjectOrchestration err = %v (n=%d), want ErrProjectTasksChanged", err, n)
	}
	if _, err := store.GetProjectByID(ctx, target.ID); err != nil {
		t.Errorf("project gone after a refused delete: %v", err)
	}
	for _, id := range []uuid.UUID{kept.ID, mover.ID} {
		if _, err := store.GetTaskByID(ctx, id); err != nil {
			t.Errorf("task %s gone after a refused delete: %v", id, err)
		}
	}
}
