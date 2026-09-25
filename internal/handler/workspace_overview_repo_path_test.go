package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/Wayne997035/wayneblacktea/internal/workspace"
)

// TestRepoOverview_PairsPathShapedRepoWithProject is the end-to-end [F0925-29]
// check on real SQLite stores: a repo registered under a path-shaped name
// and a project linked with the same repo_name pair up, so the repo overview
// shows the project's completed task. Before the shared rule, the repo write
// path required owner/repo while project.repo_name rejected "/", so no name
// could be written on both sides and the overview always came back empty.
func TestRepoOverview_PairsPathShapedRepoWithProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sdb, err := wbtsqlite.Open(ctx, ":memory:", "")
	if err != nil {
		t.Fatalf("wbtsqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = sdb.Close() })

	ws := wbtsqlite.NewWorkspaceStore(sdb)
	gs := wbtsqlite.NewGTDStore(sdb)
	const name = "Flare-Go/auth"

	repo, err := ws.UpsertRepo(ctx, workspace.UpsertRepoParams{Name: name})
	if err != nil {
		t.Fatalf("UpsertRepo(%q): %v", name, err)
	}
	proj, err := gs.CreateProject(ctx, gtd.CreateProjectParams{Name: "auth", Title: "Auth", RepoName: name})
	if err != nil {
		t.Fatalf("CreateProject(repo_name=%q): %v", name, err)
	}
	task, err := gs.CreateTask(ctx, gtd.CreateTaskParams{ProjectID: &proj.ID, Title: "ship login"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := gs.CompleteTask(ctx, task.ID, nil); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}

	e := newEcho()
	h := handler.NewWorkspaceOverviewHandler(ws, gs, wbtsqlite.NewDecisionStore(sdb), wbtsqlite.NewSessionStore(sdb))
	e.GET("/api/workspace/repos/:id/overview", h.GetRepoOverview)
	rec := performRequest(e, http.MethodGet, "/api/workspace/repos/"+repo.ID.String()+"/overview", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("overview status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		CompletedTasks []struct {
			ID string `json:"id"`
		} `json:"completed_tasks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode overview: %v", err)
	}
	if len(body.CompletedTasks) != 1 || body.CompletedTasks[0].ID != task.ID.String() {
		t.Errorf("completed_tasks = %+v, want exactly task %s", body.CompletedTasks, task.ID)
	}
}
