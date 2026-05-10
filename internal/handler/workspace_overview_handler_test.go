package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ---- minimal fakes for the four narrow interfaces ----

type fakeOverviewWorkspaceStore struct {
	repo      *db.Repo
	repoErr   error
	gotRepoID uuid.UUID
}

func (f *fakeOverviewWorkspaceStore) RepoByID(_ context.Context, id uuid.UUID) (*db.Repo, error) {
	f.gotRepoID = id
	return f.repo, f.repoErr
}

type fakeOverviewGTDStore struct {
	project        *db.Project
	projectErr     error
	pending        []db.Task
	pendingErr     error
	completed      []db.Task
	completedErr   error
	activity       []db.ActivityLog
	activityErr    error
	gotProjectName string
	gotSince       time.Time
}

func (f *fakeOverviewGTDStore) ProjectByName(_ context.Context, name string) (*db.Project, error) {
	f.gotProjectName = name
	return f.project, f.projectErr
}

func (f *fakeOverviewGTDStore) Tasks(_ context.Context, _ *uuid.UUID) ([]db.Task, error) {
	return f.pending, f.pendingErr
}

func (f *fakeOverviewGTDStore) RecentCompletedTasks(_ context.Context, _ uuid.UUID, _ int32) ([]db.Task, error) {
	return f.completed, f.completedErr
}

func (f *fakeOverviewGTDStore) RecentActivityByProject(_ context.Context, _ uuid.UUID, since time.Time, _ int32) ([]db.ActivityLog, error) {
	f.gotSince = since
	return f.activity, f.activityErr
}

type fakeOverviewDecisionStore struct {
	list        []db.Decision
	err         error
	gotRepoName string
	gotLimit    int32
}

func (f *fakeOverviewDecisionStore) ByRepo(_ context.Context, name string, limit int32) ([]db.Decision, error) {
	f.gotRepoName = name
	f.gotLimit = limit
	return f.list, f.err
}

type fakeOverviewSessionStore struct {
	list        []db.SessionHandoff
	err         error
	gotRepoName string
	gotLimit    int
}

func (f *fakeOverviewSessionStore) HandoffsByRepo(_ context.Context, name string, limit int) ([]db.SessionHandoff, error) {
	f.gotRepoName = name
	f.gotLimit = limit
	return f.list, f.err
}

// ---- tests ----

// testRepoName is the canonical repo name used across happy-path test cases;
// it is also the project name the GTD store is asked to look up.
const testRepoName = "wayneblacktea"

func TestWorkspaceOverviewHandler_GetRepoOverview(t *testing.T) {
	repoID := uuid.New()
	projID := uuid.New()
	taskA := uuid.New()
	taskB := uuid.New()
	decID := uuid.New()
	actID := uuid.New()
	hoID := uuid.New()
	resolvedTime := pgtype.Timestamptz{Time: time.Now().Add(-1 * time.Hour), Valid: true}
	updatedTime := pgtype.Timestamptz{Time: time.Now().Add(-2 * time.Hour), Valid: true}
	createdTime := pgtype.Timestamptz{Time: time.Now().Add(-30 * time.Minute), Valid: true}

	fullRepo := &db.Repo{
		ID:              repoID,
		Name:            testRepoName,
		Status:          "active",
		Description:     pgtype.Text{String: "Personal OS", Valid: true},
		Language:        pgtype.Text{String: "Go", Valid: true},
		CurrentBranch:   pgtype.Text{String: "main", Valid: true},
		NextPlannedStep: pgtype.Text{String: "ship overview", Valid: true},
		Path:            pgtype.Text{String: "/code/wayneblacktea", Valid: true},
		KnownIssues:     []string{"flaky test X"},
		LastActivity:    updatedTime,
	}

	cases := []struct {
		name      string
		paramID   string
		ws        *fakeOverviewWorkspaceStore
		gtdStore  *fakeOverviewGTDStore
		dec       *fakeOverviewDecisionStore
		sess      *fakeOverviewSessionStore
		wantCode  int
		checkBody func(t *testing.T, body []byte, fakes overviewFakes)
	}{
		{
			name:    "happy path with paired project",
			paramID: repoID.String(),
			ws:      &fakeOverviewWorkspaceStore{repo: fullRepo},
			gtdStore: &fakeOverviewGTDStore{
				project: &db.Project{ID: projID, Name: testRepoName, Title: "Personal OS"},
				pending: []db.Task{{
					ID: taskA, Title: "Pending A", Status: "pending", Priority: 2,
					Importance: pgtype.Int2{Int16: 1, Valid: true},
					ProjectID:  pgtype.UUID{Bytes: [16]byte(projID), Valid: true},
				}},
				completed: []db.Task{{
					ID: taskB, Title: "Done B", Status: "completed",
					UpdatedAt: updatedTime,
					Artifact:  pgtype.Text{String: "https://github.com/x/y/pull/1", Valid: true},
					ProjectID: pgtype.UUID{Bytes: [16]byte(projID), Valid: true},
				}},
				activity: []db.ActivityLog{{
					ID: actID, Action: "log_decision",
					Notes:     pgtype.Text{String: "Use Echo", Valid: true},
					CreatedAt: createdTime,
				}},
			},
			dec: &fakeOverviewDecisionStore{list: []db.Decision{{
				ID: decID, Title: "Use Echo", Decision: "Echo", Rationale: "Fast",
				CreatedAt: createdTime,
			}}},
			sess: &fakeOverviewSessionStore{list: []db.SessionHandoff{{
				ID: hoID, Intent: "continue",
				CreatedAt:  createdTime,
				ResolvedAt: resolvedTime,
			}}},
			wantCode: http.StatusOK,
			checkBody: func(t *testing.T, body []byte, fakes overviewFakes) {
				assertHappyPathBody(t, body, repoID, fakes)
			},
		},
		{
			name:    "repo without paired project — task lists empty, decisions still returned",
			paramID: repoID.String(),
			ws:      &fakeOverviewWorkspaceStore{repo: fullRepo},
			gtdStore: &fakeOverviewGTDStore{
				projectErr: gtd.ErrNotFound,
			},
			dec: &fakeOverviewDecisionStore{list: []db.Decision{{
				ID: decID, Title: "Use Echo", Decision: "Echo", Rationale: "Fast",
			}}},
			sess:     &fakeOverviewSessionStore{list: []db.SessionHandoff{}},
			wantCode: http.StatusOK,
			checkBody: func(t *testing.T, body []byte, _ overviewFakes) {
				t.Helper()
				var resp map[string]json.RawMessage
				if err := json.Unmarshal(body, &resp); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
				// Tasks lists should be [] not null
				for _, key := range []string{"pending_tasks", "completed_tasks", "recent_activity", "recent_handoffs"} {
					if string(resp[key]) != "[]" {
						t.Errorf("expected %q to be empty array, got %s", key, string(resp[key]))
					}
				}
				// Decisions should still be present (not gated by project)
				var dec []map[string]any
				if err := json.Unmarshal(resp["recent_decisions"], &dec); err != nil {
					t.Fatalf("invalid recent_decisions JSON: %v", err)
				}
				if len(dec) != 1 {
					t.Errorf("recent_decisions len = %d, want 1", len(dec))
				}
			},
		},
		{
			name:     "invalid uuid → 400",
			paramID:  "not-a-uuid",
			ws:       &fakeOverviewWorkspaceStore{},
			gtdStore: &fakeOverviewGTDStore{},
			dec:      &fakeOverviewDecisionStore{},
			sess:     &fakeOverviewSessionStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "repo not found → 404",
			paramID:  repoID.String(),
			ws:       &fakeOverviewWorkspaceStore{repoErr: workspace.ErrNotFound},
			gtdStore: &fakeOverviewGTDStore{},
			dec:      &fakeOverviewDecisionStore{},
			sess:     &fakeOverviewSessionStore{},
			wantCode: http.StatusNotFound,
		},
		{
			name:     "repo store error → 500",
			paramID:  repoID.String(),
			ws:       &fakeOverviewWorkspaceStore{repoErr: errors.New("db down")},
			gtdStore: &fakeOverviewGTDStore{},
			dec:      &fakeOverviewDecisionStore{},
			sess:     &fakeOverviewSessionStore{},
			wantCode: http.StatusInternalServerError,
		},
		{
			name:    "decision store error → 500 (after repo+project succeed)",
			paramID: repoID.String(),
			ws:      &fakeOverviewWorkspaceStore{repo: fullRepo},
			gtdStore: &fakeOverviewGTDStore{
				project: &db.Project{ID: projID, Name: "wayneblacktea"},
			},
			dec:      &fakeOverviewDecisionStore{err: errors.New("db down")},
			sess:     &fakeOverviewSessionStore{},
			wantCode: http.StatusInternalServerError,
		},
		{
			name:    "session store error → 500 (after others succeed)",
			paramID: repoID.String(),
			ws:      &fakeOverviewWorkspaceStore{repo: fullRepo},
			gtdStore: &fakeOverviewGTDStore{
				project: &db.Project{ID: projID, Name: "wayneblacktea"},
			},
			dec:      &fakeOverviewDecisionStore{list: []db.Decision{}},
			sess:     &fakeOverviewSessionStore{err: errors.New("db down")},
			wantCode: http.StatusInternalServerError,
		},
		{
			name:    "pending tasks store error → 500",
			paramID: repoID.String(),
			ws:      &fakeOverviewWorkspaceStore{repo: fullRepo},
			gtdStore: &fakeOverviewGTDStore{
				project:    &db.Project{ID: projID, Name: "wayneblacktea"},
				pendingErr: errors.New("db down"),
			},
			dec:      &fakeOverviewDecisionStore{},
			sess:     &fakeOverviewSessionStore{},
			wantCode: http.StatusInternalServerError,
		},
		{
			name:    "completed tasks store error → 500",
			paramID: repoID.String(),
			ws:      &fakeOverviewWorkspaceStore{repo: fullRepo},
			gtdStore: &fakeOverviewGTDStore{
				project:      &db.Project{ID: projID, Name: "wayneblacktea"},
				completedErr: errors.New("db down"),
			},
			dec:      &fakeOverviewDecisionStore{},
			sess:     &fakeOverviewSessionStore{},
			wantCode: http.StatusInternalServerError,
		},
		{
			name:    "activity store error → 500",
			paramID: repoID.String(),
			ws:      &fakeOverviewWorkspaceStore{repo: fullRepo},
			gtdStore: &fakeOverviewGTDStore{
				project:     &db.Project{ID: projID, Name: "wayneblacktea"},
				activityErr: errors.New("db down"),
			},
			dec:      &fakeOverviewDecisionStore{},
			sess:     &fakeOverviewSessionStore{},
			wantCode: http.StatusInternalServerError,
		},
		{
			name:    "ProjectByName non-NotFound error → 500",
			paramID: repoID.String(),
			ws:      &fakeOverviewWorkspaceStore{repo: fullRepo},
			gtdStore: &fakeOverviewGTDStore{
				projectErr: errors.New("db down"),
			},
			dec:      &fakeOverviewDecisionStore{},
			sess:     &fakeOverviewSessionStore{},
			wantCode: http.StatusInternalServerError,
		},
		{
			name:    "pending list capped at 50 (response trimmed)",
			paramID: repoID.String(),
			ws:      &fakeOverviewWorkspaceStore{repo: fullRepo},
			gtdStore: &fakeOverviewGTDStore{
				project: &db.Project{ID: projID, Name: "wayneblacktea"},
				pending: makePendingTasks(75, projID),
			},
			dec:      &fakeOverviewDecisionStore{},
			sess:     &fakeOverviewSessionStore{},
			wantCode: http.StatusOK,
			checkBody: func(t *testing.T, body []byte, _ overviewFakes) {
				t.Helper()
				var resp map[string]json.RawMessage
				if err := json.Unmarshal(body, &resp); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
				var pending []map[string]any
				if err := json.Unmarshal(resp["pending_tasks"], &pending); err != nil {
					t.Fatalf("invalid pending_tasks JSON: %v", err)
				}
				if len(pending) != 50 {
					t.Errorf("pending_tasks len = %d, want 50 (cap)", len(pending))
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewWorkspaceOverviewHandler(tc.ws, tc.gtdStore, tc.dec, tc.sess)
			e.GET("/api/workspace/repos/:id/overview", h.GetRepoOverview)
			rec := performRequest(e, http.MethodGet, "/api/workspace/repos/"+tc.paramID+"/overview", "")
			if rec.Code != tc.wantCode {
				t.Errorf("got status %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.checkBody != nil && rec.Code == http.StatusOK {
				tc.checkBody(t, rec.Body.Bytes(), overviewFakes{ws: tc.ws, gtd: tc.gtdStore, dec: tc.dec, sess: tc.sess})
			}
		})
	}
}

// overviewFakes bundles the four fakes for closure access inside checkBody.
type overviewFakes struct {
	ws   *fakeOverviewWorkspaceStore
	gtd  *fakeOverviewGTDStore
	dec  *fakeOverviewDecisionStore
	sess *fakeOverviewSessionStore
}

func makePendingTasks(n int, projID uuid.UUID) []db.Task {
	tasks := make([]db.Task, 0, n)
	for i := 0; i < n; i++ {
		tasks = append(tasks, db.Task{
			ID:        uuid.New(),
			Title:     "task",
			Status:    "pending",
			Priority:  3,
			ProjectID: pgtype.UUID{Bytes: [16]byte(projID), Valid: true},
		})
	}
	return tasks
}

// assertHappyPathBody pulls the heavy assertion logic out of the table-driven
// test cases to keep TestWorkspaceOverviewHandler_GetRepoOverview's cyclomatic
// complexity manageable.
func assertHappyPathBody(t *testing.T, body []byte, repoID uuid.UUID, fakes overviewFakes) {
	t.Helper()
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	for _, key := range []string{"repo", "completed_tasks", "pending_tasks", "recent_decisions", "recent_activity", "recent_handoffs"} {
		if _, ok := resp[key]; !ok {
			t.Errorf("missing key %q in response", key)
		}
	}
	assertRepoID(t, resp["repo"], repoID)
	assertListLen(t, resp["pending_tasks"], "pending_tasks", 1)
	assertHandoffStatus(t, resp["recent_handoffs"], "resolved")
	assertActivityKind(t, resp["recent_activity"], "decision")
	assertCallsUseRepoName(t, fakes, testRepoName)
	assertActivityWindow(t, fakes.gtd.gotSince)
}

func assertRepoID(t *testing.T, raw json.RawMessage, want uuid.UUID) {
	t.Helper()
	var repo map[string]any
	if err := json.Unmarshal(raw, &repo); err != nil {
		t.Fatalf("invalid repo JSON: %v", err)
	}
	if repo["id"] != want.String() {
		t.Errorf("repo.id = %v, want %s", repo["id"], want.String())
	}
}

func assertListLen(t *testing.T, raw json.RawMessage, key string, wantLen int) {
	t.Helper()
	var arr []map[string]any
	if err := json.Unmarshal(raw, &arr); err != nil {
		t.Fatalf("invalid %s JSON: %v", key, err)
	}
	if len(arr) != wantLen {
		t.Errorf("%s len = %d, want %d", key, len(arr), wantLen)
	}
}

func assertHandoffStatus(t *testing.T, raw json.RawMessage, wantStatus string) {
	t.Helper()
	var handoffs []map[string]any
	if err := json.Unmarshal(raw, &handoffs); err != nil {
		t.Fatalf("invalid recent_handoffs JSON: %v", err)
	}
	if len(handoffs) != 1 || handoffs[0]["status"] != wantStatus {
		t.Errorf("handoffs[0].status = %v, want %s", handoffs[0]["status"], wantStatus)
	}
}

func assertActivityKind(t *testing.T, raw json.RawMessage, wantKind string) {
	t.Helper()
	var activity []map[string]any
	if err := json.Unmarshal(raw, &activity); err != nil {
		t.Fatalf("invalid recent_activity JSON: %v", err)
	}
	if len(activity) != 1 || activity[0]["kind"] != wantKind {
		t.Errorf("activity[0].kind = %v, want %s", activity[0]["kind"], wantKind)
	}
}

func assertCallsUseRepoName(t *testing.T, fakes overviewFakes, want string) {
	t.Helper()
	if fakes.dec.gotRepoName != want {
		t.Errorf("dec.gotRepoName = %q, want %s", fakes.dec.gotRepoName, want)
	}
	if fakes.sess.gotRepoName != want {
		t.Errorf("sess.gotRepoName = %q, want %s", fakes.sess.gotRepoName, want)
	}
	if fakes.gtd.gotProjectName != want {
		t.Errorf("gtd.gotProjectName = %q, want %s", fakes.gtd.gotProjectName, want)
	}
}

func assertActivityWindow(t *testing.T, since time.Time) {
	t.Helper()
	windowAgo := time.Since(since)
	if windowAgo < 13*24*time.Hour || windowAgo > 15*24*time.Hour {
		t.Errorf("activity window = %v, want ~14 days", windowAgo)
	}
}
