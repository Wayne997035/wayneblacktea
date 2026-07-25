// Command qa-seed copies real (read-only) production Postgres data into a
// disposable local SQLite file so integration-qa can exercise the frontend
// against real-shaped data without touching prod and without per-test
// cleanup — the seeded file is simply discarded afterward. This replaces the
// prior fallback of "QA against prod + delete test rows".
//
// v1 scope: goals (active), projects (active), tasks (all statuses),
// decisions, and pending_proposals (all known types) — the tables the
// frontend integration-qa currently touches. Knowledge/pgvector embedding
// data is out of scope (semantic search is not exercised by frontend QA).
//
// Source (read-only): reuses storage.BuildServerStores(ctx,
// storage.BackendPostgres), which reads DATABASE_URL / PGSSLROOTCERT /
// APP_ENV / WORKSPACE_ID from the environment exactly like cmd/server does.
// Every read in this tool goes through a domain StoreIface List/Get-style
// method (ActiveGoals / ListActiveProjects / TasksFiltered / ListAll / All);
// qa-seed never calls a Create*/Update*/Delete* method on the source stores.
// Each seedX helper below takes a narrow reader interface (goalReader,
// projectReader, ...) exposing only the one method it needs, so the
// read-only contract is visible at every call site, not just documented in
// prose.
//
// Destination: a fresh SQLite file opened via storage.NewServerStores(...,
// BackendSQLite), which runs the full migrations/sqlite/ chain at Open()
// (same as the app does at startup) — schema parity is automatic, see
// internal/storage/sqlite/db.go Open(). IDs, created_at, and updated_at are
// preserved via the ImportX methods added to the SQLite stores
// (ImportGoal/ImportProject/ImportTask/ImportDecision/ImportProposal)
// instead of Create* (which always mints a new UUID + timestamp) — this is
// required to keep cross-table references (project.goal_id, task.project_id,
// decision.task_id, ...) intact.
//
// # QA server flow
//
// On success qa-seed also (re)writes a local env file (default ".env.test",
// see --env-test-out) that points cmd/server at the freshly-seeded SQLite
// file, so the full flow is:
//
//	task qa-seed                        # seed SQLite + write .env.test
//	go run ./cmd/server -env .env.test  # serve the app against real data
//
// .env.test deliberately has no DATABASE_URL (so storage.ResolveFromEnv
// falls back to SQLite, never prod — see internal/storage/storage.go
// BackendFromEnv), sets SQLITE_PATH to the exact --dest just seeded, a
// clearly-fake API_KEY (>=32 chars, required by cmd/server's
// validateAPIKey — see cmd/server/main.go), and PORT=8080 — a different
// port from cmd/server's own hardcoded default (8420, see
// cmd/server/main.go run()) so a QA server and a normally-running dev/prod
// server can coexist without a bind conflict. It also propagates
// WORKSPACE_ID from the source env (same value used to open the Postgres
// source above), so the QA server sees the same workspace scope prod data
// was captured under, rather than relying on "WORKSPACE_ID unset matches
// every row" as an implicit side effect.
//
// .env.test is gitignored (matches the repo-wide ".env*" pattern in
// .gitignore) and is regenerated — overwritten — on every qa-seed run, so
// it always matches whatever SQLite file was seeded most recently. It is
// written with 0600 permissions per backend-security-design.md §4.1
// (credential-bearing files).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

// config holds validated CLI input. All fields are populated by parseFlags,
// which rejects invalid combinations before run() touches any store — see
// backend-security-design.md §5.2 (exhaustive client-side CLI validation).
type config struct {
	envFile       string
	dest          string
	overwrite     bool
	taskLimit     int
	proposalLimit int
	decisionLimit int
	envTestOut    string
	qaPort        int
}

const (
	defaultTaskLimit     = 5000
	defaultProposalLimit = 1000
	defaultDecisionLimit = 5000
	// maxLimitFlag bounds every --*-limit flag. It exists so the later
	// int -> int32 conversions (decision/proposal storage APIs take int32
	// limits) are provably safe within the same function that performs them
	// — see toInt32Limit.
	maxLimitFlag = 1_000_000

	// defaultEnvTestOut is where qa-seed writes the QA server's env file by
	// default — repo root, matching how .env / .env.local are conventionally
	// loaded relative to CWD (see cmd/server/main.go's -env flag).
	defaultEnvTestOut = ".env.test"
	// defaultQAPort deliberately differs from cmd/server's own hardcoded
	// default (8420, see cmd/server/main.go run()) so a QA server started
	// with .env.test does not collide with a normally-running dev/prod
	// server on the same machine.
	defaultQAPort = 8080
	// qaDummyAPIKey is a clearly-fake credential — never a real secret —
	// satisfying cmd/server's validateAPIKey (>=32 chars) so .env.test is
	// immediately runnable. 42 characters (verified: len(qaDummyAPIKey) is
	// asserted in TestWriteEnvTest_HappyPath so a future edit cannot
	// silently drop below the 32-char minimum).
	qaDummyAPIKey = "qa-test-dummy-key-not-a-real-secret-000000" //nolint:gosec // G101: documented fake credential, not a real secret
)

// knownProposalTypes enumerates every proposal.Type the schema accepts
// (pending_proposals.type CHECK constraint). proposal.StoreIface.ListAll
// requires an exact type match (no wildcard), so a full copy loops over
// this list rather than issuing one unfiltered query.
var knownProposalTypes = []proposal.Type{
	proposal.TypeGoal,
	proposal.TypeProject,
	proposal.TypeTask,
	proposal.TypeConcept,
	proposal.TypeKnowledge,
	proposal.TypePlaybook,
	proposal.TypeDecision,
}

func run(args []string) error {
	cfg, err := parseFlags(args)
	if err != nil {
		return err
	}

	// Non-fatal: mirrors cmd/server's godotenv.Load convention. Local dev
	// supplies .env.local; CI/operator env vars set directly take priority
	// over whatever the file has (godotenv.Load does not override existing
	// env vars).
	if loadErr := godotenv.Load(cfg.envFile); loadErr != nil && !os.IsNotExist(loadErr) {
		return fmt.Errorf("loading %s: %w", cfg.envFile, loadErr)
	}

	ctx := context.Background()

	slog.Info("qa-seed: opening source (prod Postgres, read-only)")
	src, err := storage.BuildServerStores(ctx, storage.BackendPostgres)
	if err != nil {
		return fmt.Errorf("opening postgres source: %w", err)
	}
	defer func() { _ = src.Close() }()

	slog.Info("qa-seed: opening destination (fresh sqlite, migrations auto-applied)", "path", cfg.dest)
	dest, err := storage.NewServerStores(ctx, storage.FactoryConfig{
		Backend:    storage.BackendSQLite,
		SQLitePath: cfg.dest,
		AppEnv:     os.Getenv("APP_ENV"),
	})
	if err != nil {
		return fmt.Errorf("opening sqlite destination: %w", err)
	}
	defer func() { _ = dest.Close() }()

	destGTD := dest.SqliteGTD()
	destProposal := dest.SqliteProposal()
	destDecision := dest.SqliteDecision()
	if destGTD == nil || destProposal == nil || destDecision == nil {
		// Cannot happen given Backend: BackendSQLite above, but guards
		// against a future factory refactor silently returning a
		// Postgres-shaped bundle for a SQLite backend request.
		return errors.New("qa-seed: destination stores did not resolve to sqlite concrete types")
	}

	gtdSrc := src.GTD()
	c, err := seedAll(ctx, gtdSrc, gtdSrc, gtdSrc, gtdSrc, src.Decision(), src.Proposal(), destGTD, destProposal, destDecision, cfg)
	if err != nil {
		return err
	}

	// WORKSPACE_ID read here (post godotenv.Load, same as the source/dest
	// opens above) so .env.test scopes the QA server identically to how the
	// data was captured — see the package doc comment's "QA server flow".
	if err := writeEnvTest(cfg.envTestOut, cfg.dest, os.Getenv("WORKSPACE_ID"), cfg.qaPort); err != nil {
		return fmt.Errorf("writing %s: %w", cfg.envTestOut, err)
	}
	slog.Info("qa-seed: wrote QA server env file", "path", cfg.envTestOut, "port", cfg.qaPort,
		"next_step", fmt.Sprintf("go run ./cmd/server -env %s", cfg.envTestOut))

	slog.Info("qa-seed complete",
		"dest", cfg.dest,
		"goals", c.goals, "projects", c.projects, "projects_backfilled", c.projectsBackfilled,
		"tasks", c.tasks, "decisions", c.decisions, "proposals", c.proposals)
	return nil
}

// writeEnvTest (re)writes the QA server's env file at path, always
// overwriting — the file is disposable and meant to be regenerated on every
// qa-seed run so it never drifts from the SQLite file most recently seeded
// (unlike --dest, there is no --overwrite gate here). sqlitePath is the
// exact path just seeded; workspaceID is propagated verbatim (may be empty
// — legacy/unscoped mode, same semantics as everywhere else in this repo).
// No DATABASE_URL is ever written, so storage.ResolveFromEnv (see
// internal/storage/storage.go BackendFromEnv) falls back to SQLite —
// pointing this file at prod is structurally impossible, not just a
// documented convention.
func writeEnvTest(path, sqlitePath, workspaceID string, port int) error {
	var b strings.Builder
	b.WriteString("# Auto-generated by cmd/qa-seed — DO NOT hand-edit.\n")
	b.WriteString("# Regenerated (overwritten) on every `task qa-seed` run to match the\n")
	b.WriteString("# freshly-seeded SQLite file below. Safe to delete; qa-seed recreates it.\n")
	b.WriteString("#\n")
	b.WriteString("# Run the QA server against the seeded data:\n")
	fmt.Fprintf(&b, "#   go run ./cmd/server -env %s\n", path)
	fmt.Fprintf(&b, "# Then it serves on http://localhost:%d for integration-qa.\n", port)
	b.WriteString("STORAGE_BACKEND=sqlite\n")
	fmt.Fprintf(&b, "SQLITE_PATH=%s\n", sqlitePath)
	b.WriteString("APP_ENV=test\n")
	fmt.Fprintf(&b, "API_KEY=%s\n", qaDummyAPIKey)
	fmt.Fprintf(&b, "PORT=%d\n", port)
	if workspaceID != "" {
		fmt.Fprintf(&b, "WORKSPACE_ID=%s\n", workspaceID)
	}

	// 0600: credential-bearing file (API_KEY), even though the key is a
	// declared dummy — backend-security-design.md §4.1.
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// parseFlags parses and validates CLI input. See config's doc comment.
func parseFlags(args []string) (config, error) {
	fs := flag.NewFlagSet("qa-seed", flag.ContinueOnError)
	envFile := fs.String("env", ".env.local", "env file to load (DATABASE_URL / PGSSLROOTCERT / WORKSPACE_ID / APP_ENV)")
	dest := fs.String("dest", "", "destination SQLite file path (default: disposable path under os.TempDir())")
	overwrite := fs.Bool("overwrite", false, "allow overwriting an existing file at --dest (default: refuse)")
	limitUsage := fmt.Sprintf("must be in range 1..%d", maxLimitFlag)
	taskLimit := fs.Int("task-limit", defaultTaskLimit, "max tasks to copy, all statuses ("+limitUsage+")")
	proposalLimit := fs.Int("proposal-limit", defaultProposalLimit, "max proposals PER TYPE to copy ("+limitUsage+")")
	decisionLimit := fs.Int("decision-limit", defaultDecisionLimit, "max decisions to copy ("+limitUsage+")")
	envTestOut := fs.String("env-test-out", defaultEnvTestOut,
		"path to (re)write the QA server's env file after a successful seed "+
			"(no DATABASE_URL, points SQLITE_PATH at --dest, dummy API_KEY, PORT=8080 by default — see --qa-port)")
	qaPort := fs.Int("qa-port", defaultQAPort,
		"PORT written into --env-test-out for the QA server; deliberately not 8420 "+
			"(cmd/server's own default) to avoid a bind conflict with a normally-running server (must be 1..65535)")
	if err := fs.Parse(args); err != nil {
		return config{}, fmt.Errorf("parsing flags: %w", err)
	}

	for _, f := range []struct {
		name string
		val  int
	}{
		{"task-limit", *taskLimit},
		{"proposal-limit", *proposalLimit},
		{"decision-limit", *decisionLimit},
	} {
		if f.val <= 0 || f.val > maxLimitFlag {
			return config{}, fmt.Errorf("--%s must be in range 1..%d, got %d", f.name, maxLimitFlag, f.val)
		}
	}
	if *qaPort < 1 || *qaPort > 65535 {
		return config{}, fmt.Errorf("--qa-port must be in range 1..65535, got %d", *qaPort)
	}
	if strings.TrimSpace(*envTestOut) == "" {
		return config{}, errors.New("--env-test-out must not be empty")
	}
	if strings.ContainsAny(*envTestOut, "\x00\r\n") {
		return config{}, errors.New("--env-test-out contains a null byte or control character")
	}

	resolvedDest, err := resolveDestPath(*dest, *overwrite)
	if err != nil {
		return config{}, err
	}

	return config{
		envFile:       *envFile,
		dest:          resolvedDest,
		overwrite:     *overwrite,
		taskLimit:     *taskLimit,
		proposalLimit: *proposalLimit,
		decisionLimit: *decisionLimit,
		envTestOut:    filepath.Clean(*envTestOut),
		qaPort:        *qaPort,
	}, nil
}

// resolveDestPath validates and returns the destination SQLite path. Empty
// input defaults to a disposable path under os.TempDir() stamped with the
// current time so repeat runs never collide. An explicit --dest that already
// exists is rejected unless --overwrite is set; a --dest naming an existing
// directory is always rejected (SQLite would refuse to open it as a database
// file, but this gives a clearer error up front). Null bytes / control
// characters are rejected defensively even though this is operator-supplied
// input, not adversarial LLM tool input (backend-security-design.md §2.2).
func resolveDestPath(dest string, overwrite bool) (string, error) {
	if dest == "" {
		return filepath.Join(os.TempDir(), fmt.Sprintf("wbt-qa-seed-%s.db", time.Now().UTC().Format("20060102-150405"))), nil
	}
	if strings.ContainsAny(dest, "\x00\r\n") {
		return "", errors.New("--dest contains a null byte or control character")
	}
	clean := filepath.Clean(dest)
	info, err := os.Stat(clean)
	switch {
	case err == nil:
		if info.IsDir() {
			return "", fmt.Errorf("--dest %q is an existing directory, not a file path", clean)
		}
		if !overwrite {
			return "", fmt.Errorf("--dest %q already exists; pass --overwrite to replace it", clean)
		}
		if rmErr := os.Remove(clean); rmErr != nil {
			return "", fmt.Errorf("removing existing --dest %q: %w", clean, rmErr)
		}
	case errors.Is(err, os.ErrNotExist):
		// fine — fresh path, nothing to remove.
	default:
		return "", fmt.Errorf("stat --dest %q: %w", clean, err)
	}
	return clean, nil
}

// toInt32Limit converts a flag-validated (1..maxLimitFlag) int to int32 for
// storage APIs that take int32 limits. The explicit math.MaxInt32 bound
// makes the narrowing conversion provably safe in the same function that
// performs it (gosec G115 integer-overflow-conversion).
func toInt32Limit(n int) (int32, error) {
	if n <= 0 || n > math.MaxInt32 {
		return 0, fmt.Errorf("limit %d out of int32 range", n)
	}
	return int32(n), nil
}

// counts tallies how many rows of each entity were imported, for the final
// summary log line. projectsBackfilled is a subset already counted in
// projects — see seedBackfillProjects.
type counts struct {
	goals, projects, projectsBackfilled, tasks, decisions, proposals int
}

// goalReader/projectReader/projectByIDReader/taskReader/decisionReader/
// proposalReader narrow the source (Postgres) stores down to only the
// single List/Get-style method each seedX helper calls. gtd.StoreIface /
// decision.StoreIface / proposal.StoreIface values satisfy these
// structurally — no adapter or type assertion needed, Go interface
// assignability handles it — but the narrow parameter types make it visible
// at every call site that qa-seed cannot accidentally invoke a
// Create*/Update*/Delete* method on the read-only source.
type goalReader interface {
	ActiveGoals(ctx context.Context) ([]db.Goal, error)
}
type projectReader interface {
	ListActiveProjects(ctx context.Context) ([]db.Project, error)
}

// projectByIDReader backs seedBackfillProjects: GetProjectByID returns a
// project "regardless of status" (see internal/gtd/store.go /
// internal/storage/sqlite/gtd.go doc comments) — unlike ListActiveProjects,
// which only surfaces status='active' rows. Tasks and decisions can
// reference a completed/archived/on_hold project (no FK enforces
// liveness), so a project referenced by an imported task/decision but
// missing from the active-only listing is fetched individually here to
// keep "task.project_id resolves in the destination" true for every
// imported task, not just the active-project subset.
type projectByIDReader interface {
	GetProjectByID(ctx context.Context, id uuid.UUID) (*db.Project, error)
}
type taskReader interface {
	TasksFiltered(ctx context.Context, f gtd.TaskFilter) ([]db.Task, error)
}
type decisionReader interface {
	All(ctx context.Context, limit int32) ([]db.Decision, error)
}
type proposalReader interface {
	ListAll(ctx context.Context, proposalType string, limit int32) ([]db.PendingProposal, error)
}

// seedAll copies goals -> projects -> tasks -> decisions -> proposals, then
// backfills any project referenced by an imported task/decision that the
// active-only project listing missed (see projectByIDReader). Order matters
// for human-readability of the summary log and because the backfill step
// needs the task/decision project_id references already collected — there
// are no FK constraints (CLAUDE.md red-line #9), so SQLite itself does not
// require parents to exist before children reference them.
func seedAll(
	ctx context.Context,
	srcGoals goalReader, srcProjects projectReader, srcProjectByID projectByIDReader, srcTasks taskReader,
	srcDecisions decisionReader, srcProposals proposalReader,
	destGTD *wbtsqlite.GTDStore, destProposal *wbtsqlite.ProposalStore, destDecision *wbtsqlite.DecisionStore,
	cfg config,
) (counts, error) {
	var c counts
	var err error

	if c.goals, err = seedGoals(ctx, srcGoals, destGTD); err != nil {
		return c, err
	}

	seededProjectIDs, err := seedProjects(ctx, srcProjects, destGTD)
	if err != nil {
		return c, err
	}
	c.projects = len(seededProjectIDs)

	var referencedProjectIDs map[uuid.UUID]struct{}
	if c.tasks, referencedProjectIDs, err = seedTasks(ctx, srcTasks, destGTD, cfg.taskLimit); err != nil {
		return c, err
	}

	var decisionProjectIDs map[uuid.UUID]struct{}
	if c.decisions, decisionProjectIDs, err = seedDecisions(ctx, srcDecisions, destDecision, cfg.decisionLimit); err != nil {
		return c, err
	}
	for id := range decisionProjectIDs {
		referencedProjectIDs[id] = struct{}{}
	}

	if c.projectsBackfilled, err = seedBackfillProjects(ctx, srcProjectByID, destGTD, seededProjectIDs, referencedProjectIDs); err != nil {
		return c, err
	}
	c.projects += c.projectsBackfilled

	if c.proposals, err = seedProposals(ctx, srcProposals, destProposal, cfg.proposalLimit); err != nil {
		return c, err
	}
	return c, nil
}

func seedGoals(ctx context.Context, src goalReader, dest *wbtsqlite.GTDStore) (int, error) {
	goals, err := src.ActiveGoals(ctx)
	if err != nil {
		return 0, fmt.Errorf("listing goals from postgres: %w", err)
	}
	n := 0
	for _, g := range goals {
		if err := dest.ImportGoal(ctx, g); err != nil {
			return n, fmt.Errorf("importing goal %s: %w", g.ID, err)
		}
		n++
	}
	slog.Info("qa-seed: goals imported", "count", n)
	return n, nil
}

// seedProjects imports every active project and returns the set of imported
// IDs, so seedBackfillProjects knows which task/decision-referenced project
// IDs still need fetching.
func seedProjects(ctx context.Context, src projectReader, dest *wbtsqlite.GTDStore) (map[uuid.UUID]struct{}, error) {
	projects, err := src.ListActiveProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing projects from postgres: %w", err)
	}
	seeded := make(map[uuid.UUID]struct{}, len(projects))
	for _, p := range projects {
		if err := dest.ImportProject(ctx, p); err != nil {
			return seeded, fmt.Errorf("importing project %s: %w", p.ID, err)
		}
		seeded[p.ID] = struct{}{}
	}
	slog.Info("qa-seed: projects imported", "count", len(seeded))
	return seeded, nil
}

// seedTasks imports every task (all statuses) and returns the set of
// distinct non-null project_id values referenced, for seedBackfillProjects.
func seedTasks(ctx context.Context, src taskReader, dest *wbtsqlite.GTDStore, limit int) (int, map[uuid.UUID]struct{}, error) {
	// TaskFilter.Status "all" returns every status (pending/in_progress/
	// completed/cancelled), unlike the active-only Tasks() method — QA needs
	// the full lifecycle so the frontend's "completed" views have real data.
	tasks, err := src.TasksFiltered(ctx, gtd.TaskFilter{Status: "all", Limit: limit})
	if err != nil {
		return 0, nil, fmt.Errorf("listing tasks from postgres: %w", err)
	}
	referenced := make(map[uuid.UUID]struct{})
	n := 0
	for _, t := range tasks {
		if err := dest.ImportTask(ctx, t); err != nil {
			return n, referenced, fmt.Errorf("importing task %s: %w", t.ID, err)
		}
		if t.ProjectID.Valid {
			referenced[uuid.UUID(t.ProjectID.Bytes)] = struct{}{}
		}
		n++
	}
	slog.Info("qa-seed: tasks imported", "count", n, "limit", limit)
	return n, referenced, nil
}

// seedDecisions imports every decision and returns the set of distinct
// non-null project_id values referenced, for seedBackfillProjects.
// decision.task_id is not backfilled the same way: every task (all
// statuses, up to --task-limit) is already imported by seedTasks, so a
// decision's task_id resolves as long as the referenced task is within that
// limit.
func seedDecisions(ctx context.Context, src decisionReader, dest *wbtsqlite.DecisionStore, limit int) (int, map[uuid.UUID]struct{}, error) {
	limit32, err := toInt32Limit(limit)
	if err != nil {
		return 0, nil, err
	}
	decisions, err := src.All(ctx, limit32)
	if err != nil {
		return 0, nil, fmt.Errorf("listing decisions from postgres: %w", err)
	}
	referenced := make(map[uuid.UUID]struct{})
	n := 0
	for _, d := range decisions {
		if err := dest.ImportDecision(ctx, d); err != nil {
			return n, referenced, fmt.Errorf("importing decision %s: %w", d.ID, err)
		}
		if d.ProjectID.Valid {
			referenced[uuid.UUID(d.ProjectID.Bytes)] = struct{}{}
		}
		n++
	}
	slog.Info("qa-seed: decisions imported", "count", n, "limit", limit)
	return n, referenced, nil
}

// seedBackfillProjects fetches and imports every project ID in wanted that
// is not already present in seeded, via GetProjectByID (works regardless of
// status — see projectByIDReader doc comment). A project that has vanished
// from Postgres between the earlier List calls and this one (ErrNotFound)
// is logged and skipped rather than failing the whole run — the reference
// simply stays dangling in the destination, matching what a live delete
// mid-seed would produce anyway.
func seedBackfillProjects(
	ctx context.Context, src projectByIDReader, dest *wbtsqlite.GTDStore,
	seeded, wanted map[uuid.UUID]struct{},
) (int, error) {
	n := 0
	for id := range wanted {
		if _, ok := seeded[id]; ok {
			continue
		}
		p, err := src.GetProjectByID(ctx, id)
		if err != nil {
			if errors.Is(err, gtd.ErrNotFound) {
				slog.Warn("qa-seed: project referenced by a task/decision no longer exists in postgres, skipping backfill", "project_id", id)
				continue
			}
			return n, fmt.Errorf("fetching project %s for backfill: %w", id, err)
		}
		if err := dest.ImportProject(ctx, *p); err != nil {
			return n, fmt.Errorf("importing backfilled project %s: %w", id, err)
		}
		seeded[id] = struct{}{}
		n++
	}
	slog.Info("qa-seed: non-active projects backfilled (referenced by a task or decision)", "count", n)
	return n, nil
}

func seedProposals(ctx context.Context, src proposalReader, dest *wbtsqlite.ProposalStore, limitPerType int) (int, error) {
	limit32, err := toInt32Limit(limitPerType)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, t := range knownProposalTypes {
		props, err := src.ListAll(ctx, string(t), limit32)
		if err != nil {
			return n, fmt.Errorf("listing %q proposals from postgres: %w", t, err)
		}
		for _, p := range props {
			if err := dest.ImportProposal(ctx, p); err != nil {
				return n, fmt.Errorf("importing proposal %s: %w", p.ID, err)
			}
			n++
		}
	}
	slog.Info("qa-seed: proposals imported", "count", n, "limit_per_type", limitPerType)
	return n, nil
}
