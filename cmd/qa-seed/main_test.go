package main

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ----- resolveDestPath -----

func TestResolveDestPath(t *testing.T) {
	t.Run("empty dest defaults to a disposable temp path", func(t *testing.T) {
		got, err := resolveDestPath("", false)
		if err != nil {
			t.Fatalf("resolveDestPath: %v", err)
		}
		if !strings.HasPrefix(got, os.TempDir()) || !strings.HasSuffix(got, ".db") {
			t.Errorf("got %q, want a path under %q ending in .db", got, os.TempDir())
		}
	})

	t.Run("fresh explicit path is accepted unchanged (Cleaned)", func(t *testing.T) {
		dir := t.TempDir()
		want := filepath.Join(dir, "fresh.db")
		got, err := resolveDestPath(want, false)
		if err != nil {
			t.Fatalf("resolveDestPath: %v", err)
		}
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("existing directory is rejected", func(t *testing.T) {
		dir := t.TempDir()
		if _, err := resolveDestPath(dir, false); err == nil {
			t.Fatal("expected error for a directory --dest, got nil")
		}
	})

	t.Run("existing file without --overwrite is rejected", func(t *testing.T) {
		dir := t.TempDir()
		f := filepath.Join(dir, "existing.db")
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatalf("seed file: %v", err)
		}
		if _, err := resolveDestPath(f, false); err == nil {
			t.Fatal("expected error for an existing file without --overwrite, got nil")
		}
	})

	t.Run("existing file with --overwrite is removed and path returned", func(t *testing.T) {
		dir := t.TempDir()
		f := filepath.Join(dir, "existing.db")
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatalf("seed file: %v", err)
		}
		got, err := resolveDestPath(f, true)
		if err != nil {
			t.Fatalf("resolveDestPath: %v", err)
		}
		if got != f {
			t.Errorf("got %q, want %q", got, f)
		}
		if _, statErr := os.Stat(f); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("expected --overwrite to remove the pre-existing file, but it still exists (stat err=%v)", statErr)
		}
	})

	t.Run("null byte in dest is rejected", func(t *testing.T) {
		if _, err := resolveDestPath("valid\x00path.db", false); err == nil {
			t.Fatal("expected error for a null byte in --dest, got nil")
		}
	})
}

// ----- parseFlags -----

func TestParseFlags(t *testing.T) {
	t.Run("defaults are used when no flags given", func(t *testing.T) {
		cfg, err := parseFlags(nil)
		if err != nil {
			t.Fatalf("parseFlags: %v", err)
		}
		if cfg.taskLimit != defaultTaskLimit || cfg.proposalLimit != defaultProposalLimit || cfg.decisionLimit != defaultDecisionLimit {
			t.Errorf("unexpected default limits: %+v", cfg)
		}
		if cfg.envFile != ".env.local" {
			t.Errorf("envFile = %q, want %q", cfg.envFile, ".env.local")
		}
		if cfg.envTestOut != defaultEnvTestOut {
			t.Errorf("envTestOut = %q, want %q", cfg.envTestOut, defaultEnvTestOut)
		}
		if cfg.qaPort != defaultQAPort {
			t.Errorf("qaPort = %d, want %d", cfg.qaPort, defaultQAPort)
		}
	})

	t.Run("zero task-limit is rejected", func(t *testing.T) {
		if _, err := parseFlags([]string{"--task-limit=0"}); err == nil {
			t.Fatal("expected error for --task-limit=0, got nil")
		}
	})

	t.Run("negative proposal-limit is rejected", func(t *testing.T) {
		if _, err := parseFlags([]string{"--proposal-limit=-5"}); err == nil {
			t.Fatal("expected error for negative --proposal-limit, got nil")
		}
	})

	t.Run("decision-limit above maxLimitFlag is rejected", func(t *testing.T) {
		if _, err := parseFlags([]string{"--decision-limit=" + strconv.Itoa(maxLimitFlag+1)}); err == nil {
			t.Fatal("expected error for --decision-limit above the max, got nil")
		}
	})

	t.Run("dest pointing at an existing directory is rejected", func(t *testing.T) {
		dir := t.TempDir()
		if _, err := parseFlags([]string{"--dest=" + dir}); err == nil {
			t.Fatal("expected resolveDestPath's error to propagate through parseFlags, got nil")
		}
	})
}

// TestParseFlags_EnvTestFlags covers --env-test-out / --qa-port validation,
// split from TestParseFlags to keep gocyclo within the project's limit
// (min-complexity 15, build/.golangci.yml).
func TestParseFlags_EnvTestFlags(t *testing.T) {
	t.Run("qa-port out of range is rejected", func(t *testing.T) {
		if _, err := parseFlags([]string{"--qa-port=70000"}); err == nil {
			t.Fatal("expected error for --qa-port=70000 (above 65535), got nil")
		}
		if _, err := parseFlags([]string{"--qa-port=0"}); err == nil {
			t.Fatal("expected error for --qa-port=0, got nil")
		}
	})

	t.Run("empty env-test-out is rejected", func(t *testing.T) {
		if _, err := parseFlags([]string{"--env-test-out="}); err == nil {
			t.Fatal("expected error for empty --env-test-out, got nil")
		}
	})

	t.Run("env-test-out with a null byte is rejected", func(t *testing.T) {
		if _, err := parseFlags([]string{"--env-test-out=valid\x00.test"}); err == nil {
			t.Fatal("expected error for a null byte in --env-test-out, got nil")
		}
	})

	t.Run("custom env-test-out and qa-port are accepted and cleaned", func(t *testing.T) {
		cfg, err := parseFlags([]string{"--env-test-out=./sub/../custom.test", "--qa-port=9000"})
		if err != nil {
			t.Fatalf("parseFlags: %v", err)
		}
		if cfg.envTestOut != "custom.test" {
			t.Errorf("envTestOut = %q, want %q (filepath.Clean'd)", cfg.envTestOut, "custom.test")
		}
		if cfg.qaPort != 9000 {
			t.Errorf("qaPort = %d, want 9000", cfg.qaPort)
		}
	})
}

// ----- writeEnvTest -----

func TestWriteEnvTest_HappyPath(t *testing.T) {
	// The whole point of qaDummyAPIKey is that it clears cmd/server's
	// validateAPIKey floor (>=32 chars) — assert it directly so a future
	// edit that shortens the constant fails loudly here, not silently at
	// `go run ./cmd/server -env .env.test` startup.
	if len(qaDummyAPIKey) < 32 {
		t.Fatalf("qaDummyAPIKey is %d chars, want >= 32 (cmd/server's validateAPIKey floor)", len(qaDummyAPIKey))
	}

	dir := t.TempDir()
	path := filepath.Join(dir, ".env.test")
	sqlitePath := filepath.Join(dir, "seeded.db")

	if err := writeEnvTest(path, sqlitePath, "", 8080); err != nil {
		t.Fatalf("writeEnvTest: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat written file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("permissions = %o, want 0600 (credential-bearing file)", perm)
	}

	content, err := os.ReadFile(path) //nolint:gosec // G304: path is test-constructed via t.TempDir()+filepath.Join, not user input
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	text := string(content)
	mustContain := []string{
		"STORAGE_BACKEND=sqlite",
		"SQLITE_PATH=" + sqlitePath,
		"API_KEY=" + qaDummyAPIKey,
		"PORT=8080",
	}
	for _, want := range mustContain {
		if !strings.Contains(text, want) {
			t.Errorf("written file missing %q; got:\n%s", want, text)
		}
	}
	if strings.Contains(text, "DATABASE_URL") {
		t.Error("written file must never contain DATABASE_URL (would resolve to postgres, defeating the point of a disposable QA SQLite file)")
	}
	if strings.Contains(text, "WORKSPACE_ID") {
		t.Error("empty workspaceID input must not emit a WORKSPACE_ID line")
	}
}

func TestWriteEnvTest_WorkspaceIDPropagated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env.test")
	wsID := uuid.New().String()

	if err := writeEnvTest(path, filepath.Join(dir, "seeded.db"), wsID, 8080); err != nil {
		t.Fatalf("writeEnvTest: %v", err)
	}
	content, err := os.ReadFile(path) //nolint:gosec // G304: path is test-constructed via t.TempDir()+filepath.Join, not user input
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if !strings.Contains(string(content), "WORKSPACE_ID="+wsID) {
		t.Errorf("written file missing WORKSPACE_ID=%s; got:\n%s", wsID, content)
	}
}

func TestWriteEnvTest_Overwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env.test")

	if err := writeEnvTest(path, filepath.Join(dir, "first.db"), "", 8080); err != nil {
		t.Fatalf("first writeEnvTest: %v", err)
	}
	if err := writeEnvTest(path, filepath.Join(dir, "second.db"), "", 9000); err != nil {
		t.Fatalf("second writeEnvTest: %v", err)
	}
	content, err := os.ReadFile(path) //nolint:gosec // G304: path is test-constructed via t.TempDir()+filepath.Join, not user input
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	text := string(content)
	if strings.Contains(text, "first.db") {
		t.Error("stale SQLITE_PATH from the first run was not overwritten")
	}
	if !strings.Contains(text, "SQLITE_PATH="+filepath.Join(dir, "second.db")) {
		t.Errorf("expected the second run's SQLITE_PATH; got:\n%s", text)
	}
	if strings.Count(text, "STORAGE_BACKEND=sqlite") != 1 {
		t.Errorf("expected exactly one STORAGE_BACKEND line (overwrite, not append); got:\n%s", text)
	}
}

func TestWriteEnvTest_UnwritableDirErrors(t *testing.T) {
	err := writeEnvTest(filepath.Join(t.TempDir(), "does-not-exist", ".env.test"), "seeded.db", "", 8080)
	if err == nil {
		t.Fatal("expected an error writing into a non-existent directory, got nil")
	}
}

// ----- toInt32Limit -----

func TestToInt32Limit(t *testing.T) {
	tests := []struct {
		name    string
		in      int
		want    int32
		wantErr bool
	}{
		{name: "typical value", in: 500, want: 500},
		{name: "boundary: math.MaxInt32 is accepted", in: math.MaxInt32, want: math.MaxInt32},
		{name: "zero is rejected", in: 0, wantErr: true},
		{name: "negative is rejected", in: -1, wantErr: true},
		{name: "above int32 range is rejected", in: math.MaxInt32 + 1, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toInt32Limit(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("toInt32Limit(%d) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Errorf("toInt32Limit(%d) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

// ----- seedGoals / seedProjects / seedTasks / seedDecisions / seedProposals -----
//
// Each uses a fake source reader (no Postgres/testcontainers needed — the
// interesting logic under test is qa-seed's own orchestration, not the
// already-tested ImportX SQL) and a real in-memory SQLite destination
// (internal/storage/sqlite's own test suite covers ImportX SQL fidelity).

type fakeGoalReader struct {
	goals []db.Goal
	err   error
}

func (f fakeGoalReader) ActiveGoals(context.Context) ([]db.Goal, error) { return f.goals, f.err }

type fakeProjectReader struct {
	projects []db.Project
	err      error
}

func (f fakeProjectReader) ListActiveProjects(context.Context) ([]db.Project, error) {
	return f.projects, f.err
}

type fakeTaskReader struct {
	tasks []db.Task
	err   error
}

func (f fakeTaskReader) TasksFiltered(context.Context, gtd.TaskFilter) ([]db.Task, error) {
	return f.tasks, f.err
}

type fakeProjectByIDReader struct {
	byID        map[uuid.UUID]db.Project
	notFoundIDs map[uuid.UUID]bool
	err         error
}

func (f fakeProjectByIDReader) GetProjectByID(_ context.Context, id uuid.UUID) (*db.Project, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.notFoundIDs[id] {
		return nil, gtd.ErrNotFound
	}
	p, ok := f.byID[id]
	if !ok {
		return nil, gtd.ErrNotFound
	}
	return &p, nil
}

type fakeDecisionReader struct {
	decisions []db.Decision
	err       error
}

func (f fakeDecisionReader) All(context.Context, int32) ([]db.Decision, error) {
	return f.decisions, f.err
}

type fakeProposalReader struct {
	byType map[string][]db.PendingProposal
	err    error
}

func (f fakeProposalReader) ListAll(_ context.Context, proposalType string, _ int32) ([]db.PendingProposal, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.byType[proposalType], nil
}

func openDestGTD(t *testing.T) *wbtsqlite.GTDStore {
	t.Helper()
	d, err := wbtsqlite.Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("wbtsqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return wbtsqlite.NewGTDStore(d)
}

func openDestDecision(t *testing.T) *wbtsqlite.DecisionStore {
	t.Helper()
	d, err := wbtsqlite.Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("wbtsqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return wbtsqlite.NewDecisionStore(d)
}

func openDestProposal(t *testing.T) *wbtsqlite.ProposalStore {
	t.Helper()
	d, err := wbtsqlite.Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("wbtsqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return wbtsqlite.NewProposalStore(d)
}

func testGoal(title, status string) db.Goal {
	now := time.Now()
	return db.Goal{ID: uuid.New(), Title: title, Status: status, CreatedAt: pgTimeVal(now), UpdatedAt: pgTimeVal(now)}
}

func TestSeedGoals(t *testing.T) {
	t.Run("happy path imports every row", func(t *testing.T) {
		dest := openDestGTD(t)
		src := fakeGoalReader{goals: []db.Goal{testGoal("g1", "active"), testGoal("g2", "active")}}

		n, err := seedGoals(context.Background(), src, dest)
		if err != nil {
			t.Fatalf("seedGoals: %v", err)
		}
		if n != 2 {
			t.Errorf("n = %d, want 2", n)
		}
	})

	t.Run("empty source is not an error", func(t *testing.T) {
		dest := openDestGTD(t)
		n, err := seedGoals(context.Background(), fakeGoalReader{}, dest)
		if err != nil || n != 0 {
			t.Errorf("seedGoals() = (%d, %v), want (0, nil)", n, err)
		}
	})

	t.Run("source list error propagates", func(t *testing.T) {
		dest := openDestGTD(t)
		wantErr := errors.New("postgres unreachable")
		_, err := seedGoals(context.Background(), fakeGoalReader{err: wantErr}, dest)
		if !errors.Is(err, wantErr) {
			t.Errorf("seedGoals() error = %v, want wrapping %v", err, wantErr)
		}
	})
}

func TestSeedProjects(t *testing.T) {
	now := time.Now()
	proj := db.Project{
		ID: uuid.New(), Name: "p1", Title: "P1", Status: "active", Area: "projects", Priority: 3,
		CreatedAt: pgTimeVal(now), UpdatedAt: pgTimeVal(now),
	}

	t.Run("happy path imports every row and returns its id in the seeded set", func(t *testing.T) {
		dest := openDestGTD(t)
		seeded, err := seedProjects(context.Background(), fakeProjectReader{projects: []db.Project{proj}}, dest)
		if err != nil {
			t.Fatalf("seedProjects: %v", err)
		}
		if _, ok := seeded[proj.ID]; !ok || len(seeded) != 1 {
			t.Errorf("seeded = %v, want exactly {%s}", seeded, proj.ID)
		}
	})

	t.Run("source list error propagates", func(t *testing.T) {
		dest := openDestGTD(t)
		wantErr := errors.New("boom")
		_, err := seedProjects(context.Background(), fakeProjectReader{err: wantErr}, dest)
		if !errors.Is(err, wantErr) {
			t.Errorf("seedProjects() error = %v, want wrapping %v", err, wantErr)
		}
	})
}

func TestSeedTasks(t *testing.T) {
	now := time.Now()
	projectID := uuid.New()
	task := db.Task{
		ID: uuid.New(), Title: "t1", Status: "completed", Priority: 3, ProjectID: pgUUIDVal(projectID),
		CreatedAt: pgTimeVal(now), UpdatedAt: pgTimeVal(now),
	}

	t.Run("happy path imports every row and collects referenced project ids", func(t *testing.T) {
		dest := openDestGTD(t)
		n, referenced, err := seedTasks(context.Background(), fakeTaskReader{tasks: []db.Task{task}}, dest, defaultTaskLimit)
		if err != nil || n != 1 {
			t.Fatalf("seedTasks() = (%d, _, %v), want (1, _, nil)", n, err)
		}
		if _, ok := referenced[projectID]; !ok || len(referenced) != 1 {
			t.Errorf("referenced = %v, want exactly {%s}", referenced, projectID)
		}
	})

	t.Run("task with no project is not added to the referenced set", func(t *testing.T) {
		dest := openDestGTD(t)
		bare := db.Task{ID: uuid.New(), Title: "bare", Status: "pending", Priority: 3, CreatedAt: pgTimeVal(now), UpdatedAt: pgTimeVal(now)}
		_, referenced, err := seedTasks(context.Background(), fakeTaskReader{tasks: []db.Task{bare}}, dest, defaultTaskLimit)
		if err != nil {
			t.Fatalf("seedTasks: %v", err)
		}
		if len(referenced) != 0 {
			t.Errorf("referenced = %v, want empty (task has no project_id)", referenced)
		}
	})

	t.Run("source list error propagates", func(t *testing.T) {
		dest := openDestGTD(t)
		wantErr := errors.New("boom")
		_, _, err := seedTasks(context.Background(), fakeTaskReader{err: wantErr}, dest, defaultTaskLimit)
		if !errors.Is(err, wantErr) {
			t.Errorf("seedTasks() error = %v, want wrapping %v", err, wantErr)
		}
	})
}

func TestSeedDecisions(t *testing.T) {
	now := time.Now()
	projectID := uuid.New()
	dec := db.Decision{
		ID: uuid.New(), Title: "d1", Context: "c", Decision: "d", Rationale: "r", ProjectID: pgUUIDVal(projectID),
		CreatedAt: pgTimeVal(now), Source: "manual",
	}

	t.Run("happy path imports every row and collects referenced project ids", func(t *testing.T) {
		dest := openDestDecision(t)
		n, referenced, err := seedDecisions(context.Background(), fakeDecisionReader{decisions: []db.Decision{dec}}, dest, defaultDecisionLimit)
		if err != nil || n != 1 {
			t.Fatalf("seedDecisions() = (%d, _, %v), want (1, _, nil)", n, err)
		}
		if _, ok := referenced[projectID]; !ok || len(referenced) != 1 {
			t.Errorf("referenced = %v, want exactly {%s}", referenced, projectID)
		}
	})

	t.Run("invalid limit is rejected before any store call", func(t *testing.T) {
		dest := openDestDecision(t)
		if _, _, err := seedDecisions(context.Background(), fakeDecisionReader{decisions: []db.Decision{dec}}, dest, 0); err == nil {
			t.Fatal("expected error for limit=0, got nil")
		}
	})

	t.Run("source list error propagates", func(t *testing.T) {
		dest := openDestDecision(t)
		wantErr := errors.New("boom")
		_, _, err := seedDecisions(context.Background(), fakeDecisionReader{err: wantErr}, dest, defaultDecisionLimit)
		if !errors.Is(err, wantErr) {
			t.Errorf("seedDecisions() error = %v, want wrapping %v", err, wantErr)
		}
	})
}

func TestSeedBackfillProjects(t *testing.T) {
	now := time.Now()
	activeID := uuid.New()
	archivedID := uuid.New()
	goneID := uuid.New()
	archived := db.Project{
		ID: archivedID, Name: "archived-proj", Title: "Archived", Status: "archived", Area: "projects", Priority: 3,
		CreatedAt: pgTimeVal(now), UpdatedAt: pgTimeVal(now),
	}

	t.Run("fetches and imports only the ids missing from seeded", func(t *testing.T) {
		dest := openDestGTD(t)
		src := fakeProjectByIDReader{byID: map[uuid.UUID]db.Project{archivedID: archived}}
		seeded := map[uuid.UUID]struct{}{activeID: {}}
		wanted := map[uuid.UUID]struct{}{activeID: {}, archivedID: {}}

		n, err := seedBackfillProjects(context.Background(), src, dest, seeded, wanted)
		if err != nil {
			t.Fatalf("seedBackfillProjects: %v", err)
		}
		if n != 1 {
			t.Errorf("n = %d, want 1 (only archivedID was missing)", n)
		}
		if _, ok := seeded[archivedID]; !ok {
			t.Error("seeded map was not updated with the backfilled id")
		}
	})

	t.Run("a project deleted from postgres between listing and backfill is skipped, not fatal", func(t *testing.T) {
		dest := openDestGTD(t)
		src := fakeProjectByIDReader{byID: map[uuid.UUID]db.Project{}, notFoundIDs: map[uuid.UUID]bool{goneID: true}}
		n, err := seedBackfillProjects(context.Background(), src, dest, map[uuid.UUID]struct{}{}, map[uuid.UUID]struct{}{goneID: {}})
		if err != nil {
			t.Fatalf("seedBackfillProjects: %v (a not-found project must be skipped, not fatal)", err)
		}
		if n != 0 {
			t.Errorf("n = %d, want 0", n)
		}
	})

	t.Run("a genuine postgres error is fatal", func(t *testing.T) {
		dest := openDestGTD(t)
		wantErr := errors.New("connection reset")
		src := fakeProjectByIDReader{err: wantErr}
		_, err := seedBackfillProjects(context.Background(), src, dest, map[uuid.UUID]struct{}{}, map[uuid.UUID]struct{}{goneID: {}})
		if !errors.Is(err, wantErr) {
			t.Errorf("seedBackfillProjects() error = %v, want wrapping %v", err, wantErr)
		}
	})
}

func TestSeedProposals(t *testing.T) {
	now := time.Now()
	p := db.PendingProposal{ID: uuid.New(), Type: "goal", Payload: []byte(`{}`), Status: "pending", CreatedAt: pgTimeVal(now)}

	t.Run("happy path sums across every known proposal type", func(t *testing.T) {
		dest := openDestProposal(t)
		src := fakeProposalReader{byType: map[string][]db.PendingProposal{"goal": {p}}}
		n, err := seedProposals(context.Background(), src, dest, defaultProposalLimit)
		if err != nil || n != 1 {
			t.Errorf("seedProposals() = (%d, %v), want (1, nil)", n, err)
		}
	})

	t.Run("empty source across all types is not an error", func(t *testing.T) {
		dest := openDestProposal(t)
		n, err := seedProposals(context.Background(), fakeProposalReader{}, dest, defaultProposalLimit)
		if err != nil || n != 0 {
			t.Errorf("seedProposals() = (%d, %v), want (0, nil)", n, err)
		}
	})

	t.Run("source list error propagates", func(t *testing.T) {
		dest := openDestProposal(t)
		wantErr := errors.New("boom")
		_, err := seedProposals(context.Background(), fakeProposalReader{err: wantErr}, dest, defaultProposalLimit)
		if !errors.Is(err, wantErr) {
			t.Errorf("seedProposals() error = %v, want wrapping %v", err, wantErr)
		}
	})
}

// pgTimeVal/pgUUIDVal are local conveniences matching internal/storage/
// sqlite's own test helpers of the same shape — duplicated here rather than
// exported from a non-test file, since they are test-fixture-only.
func pgTimeVal(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func pgUUIDVal(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: [16]byte(id), Valid: true}
}
