package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	wbtruntime "github.com/Wayne997035/wayneblacktea/internal/runtime"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	demo := flag.Bool("demo", false, "seed demo data (projects, tasks, decisions, knowledge, concepts)")
	flag.Parse()

	if _, err := storage.ResolveFromEnv(); err != nil {
		return fmt.Errorf("resolving storage backend: %w", err)
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL not set: cmd/seed requires a Postgres backend (set STORAGE_BACKEND=postgres and DATABASE_URL)")
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("parsing database URL: %w", err)
	}
	tlsCfg, err := storage.BuildTLSConfig(os.Getenv("APP_ENV"), os.Getenv("PGSSLROOTCERT"))
	if err != nil {
		return fmt.Errorf("building TLS config: %w", err)
	}
	if tlsCfg != nil {
		cfg.ConnConfig.TLSConfig = tlsCfg
	}

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer pool.Close()

	ctx := context.Background()
	wsID, err := wbtruntime.WorkspaceIDFromEnv()
	if err != nil {
		return fmt.Errorf("reading WORKSPACE_ID env: %w", err)
	}
	gtdStore := gtd.NewStore(pool, wsID)
	wsStore := workspace.NewStore(pool, wsID)

	goalsCreated := seedGoals(ctx, gtdStore)
	reposSynced := seedRepos(ctx, wsStore)

	slog.Info("seed complete", "goals_created", goalsCreated, "repos_synced", reposSynced)

	if *demo {
		return seedDemo(ctx, pool, wsID)
	}
	return nil
}

// seedDemo seeds realistic demo data so a new user can understand the system
// in 5 minutes. It is idempotent: existing records are skipped gracefully.
func seedDemo(ctx context.Context, pool *pgxpool.Pool, wsID *uuid.UUID) error {
	gtdStore := gtd.NewStore(pool, wsID)
	decStore := decision.NewStore(pool, wsID)
	knStore := knowledge.NewStore(pool, nil, wsID) // nil embedder: dedup skipped, fine for seeding
	lrnStore := learning.NewStore(pool, wsID)

	projectIDs, err := seedDemoProjects(ctx, gtdStore)
	if err != nil {
		return fmt.Errorf("demo projects: %w", err)
	}
	if err := seedDemoTasks(ctx, gtdStore, projectIDs); err != nil {
		return fmt.Errorf("demo tasks: %w", err)
	}
	if err := seedDemoDecisions(ctx, decStore); err != nil {
		return fmt.Errorf("demo decisions: %w", err)
	}
	if err := seedDemoKnowledge(ctx, knStore); err != nil {
		return fmt.Errorf("demo knowledge: %w", err)
	}
	if err := seedDemoConcepts(ctx, lrnStore); err != nil {
		return fmt.Errorf("demo concepts: %w", err)
	}
	slog.Info("demo seed complete")
	return nil
}

// seedDemoProjects seeds 2 demo projects and returns their UUIDs (in order).
// Returns a slice of length 2 (nil entries where creation was skipped).
func seedDemoProjects(ctx context.Context, store *gtd.Store) ([]*uuid.UUID, error) {
	type projectSpec struct {
		name        string
		title       string
		description string
		area        string
		priority    int32
	}
	specs := []projectSpec{
		{
			name:        "personal-os-dev",
			title:       "Personal OS Development",
			description: "Build the wayneblacktea personal operating system — MCP tools, scheduler, and knowledge base.",
			area:        "engineering",
			priority:    1,
		},
		{
			name:        "learning-go",
			title:       "Learning Go",
			description: "Systematic study of Go: concurrency patterns, standard library, and ecosystem tooling.",
			area:        "learning",
			priority:    2,
		},
	}

	ids := make([]*uuid.UUID, len(specs))
	for i, sp := range specs {
		// Check if project already exists.
		existing, err := store.ProjectByName(ctx, sp.name)
		if err == nil {
			id := existing.ID
			slog.Info("demo project already exists, skipping", "name", sp.name)
			ids[i] = &id
			continue
		}
		if !errors.Is(err, gtd.ErrNotFound) {
			return nil, fmt.Errorf("checking project %q: %w", sp.name, err)
		}
		proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{
			Name:        sp.name,
			Title:       sp.title,
			Description: sp.description,
			Area:        sp.area,
			Priority:    sp.priority,
		})
		if err != nil {
			if errors.Is(err, gtd.ErrConflict) {
				slog.Info("demo project conflict, skipping", "name", sp.name)
				continue
			}
			return nil, fmt.Errorf("creating demo project %q: %w", sp.name, err)
		}
		slog.Info("demo project created", "name", sp.name, "id", proj.ID)
		id := proj.ID
		ids[i] = &id
	}
	return ids, nil
}

// seedDemoTasks seeds 5 demo tasks across the given projects (by index).
func seedDemoTasks(ctx context.Context, store *gtd.Store, projectIDs []*uuid.UUID) error {
	type taskSpec struct {
		projectIdx  int // index into projectIDs; -1 = no project
		title       string
		description string
		priority    int32
		importance  int16
		status      gtd.TaskStatus
		// assignee is required whenever status is in_progress (p6-7
		// domain-layer gate in Store.UpdateTaskStatus); left empty for
		// pending/completed specs below.
		assignee string
	}

	specs := []taskSpec{
		{
			projectIdx:  0,
			title:       "Set up MCP server locally",
			description: "Clone the repo, copy .env.example, run npm run build && go run ./cmd/server.",
			priority:    1,
			importance:  1,
			status:      gtd.TaskStatusCompleted,
		},
		{
			projectIdx:  0,
			title:       "Design the seed CLI --demo flag",
			description: "Add realistic demo data so a new user can understand the system in 5 minutes.",
			priority:    2,
			importance:  2,
			status:      gtd.TaskStatusInProgress,
			assignee:    "claude",
		},
		{
			projectIdx:  0,
			title:       "Write integration tests for knowledge store",
			description: "Use testcontainers to run Postgres in CI; cover AddItem, Search, dedup paths.",
			priority:    3,
			importance:  3,
			status:      gtd.TaskStatusPending,
		},
		{
			projectIdx:  1,
			title:       "Read Effective Go cover-to-cover",
			description: "Take notes on interfaces, goroutines, and the error-handling idioms.",
			priority:    2,
			importance:  2,
			status:      gtd.TaskStatusInProgress,
			assignee:    "human",
		},
		{
			projectIdx:  1,
			title:       "Complete Tour of Go exercises",
			description: "Finish all exercises in the concurrency chapter, focusing on channels and select.",
			priority:    3,
			importance:  3,
			status:      gtd.TaskStatusPending,
		},
	}

	for _, sp := range specs {
		var projectID *uuid.UUID
		if sp.projectIdx >= 0 && sp.projectIdx < len(projectIDs) {
			projectID = projectIDs[sp.projectIdx]
		}

		task, err := store.CreateTask(ctx, gtd.CreateTaskParams{
			ProjectID:   projectID,
			Title:       sp.title,
			Description: sp.description,
			Priority:    sp.priority,
			Importance:  &sp.importance,
			Assignee:    sp.assignee,
		})
		if err != nil {
			// Postgres unique violation (duplicate title + project)
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				slog.Info("demo task already exists, skipping", "title", sp.title)
				continue
			}
			slog.Warn("failed to create demo task", "title", sp.title, "err", err)
			continue
		}

		// Set status for non-pending tasks.
		if sp.status != gtd.TaskStatusPending {
			if sp.status == gtd.TaskStatusCompleted {
				if _, serr := store.CompleteTask(ctx, task.ID, nil); serr != nil {
					slog.Warn("failed to complete demo task", "title", sp.title, "err", serr)
				}
			} else {
				if _, serr := store.UpdateTaskStatus(ctx, task.ID, sp.status); serr != nil {
					slog.Warn("failed to update demo task status", "title", sp.title, "status", sp.status, "err", serr)
				}
			}
		}
		slog.Info("demo task created", "title", sp.title, "status", sp.status)
	}
	return nil
}

// seedDemoDecisions seeds 3 demo architectural decisions.
func seedDemoDecisions(ctx context.Context, store *decision.Store) error {
	type decisionSpec struct {
		title        string
		context      string
		decision     string
		rationale    string
		alternatives string
		repoName     string
	}
	specs := []decisionSpec{
		{
			title: "Use PostgreSQL + pgvector for semantic search",
			context: "wayneblacktea needs full-text and vector-similarity search over the knowledge base. " +
				"Evaluated PostgreSQL with pgvector, Elasticsearch, and pure in-memory cosine similarity.",
			decision: "PostgreSQL with pgvector ivfflat index and Gemini embeddings.",
			rationale: "Single datastore reduces operational complexity; pgvector's ivfflat is fast enough " +
				"at personal-OS scale (<10k items). Gemini Embedding API is free-tier friendly.",
			alternatives: "Elasticsearch (too heavy for single-tenant personal use), " +
				"SQLite-vec (no ANN yet — brute force only), Qdrant sidecar (extra moving part).",
			repoName: "wayneblacktea",
		},
		{
			title: "Dual-backend: SQLite (local) + Postgres (production)",
			context: "Local development should not require a running Postgres instance. " +
				"Production (Railway) runs Postgres for pgvector and full ACID.",
			decision: "storage.ResolveFromEnv() picks backend at startup via STORAGE_BACKEND env var. " +
				"Same Store interface used everywhere.",
			rationale: "Zero extra friction for contributors. SQLite :memory: is used in integration tests " +
				"for fast feedback. Backend-specific SQL lives in separate store implementations.",
			alternatives: "Always Postgres (requires local Docker); embedded DuckDB (licensing friction); " +
				"single in-memory map (no persistence).",
			repoName: "wayneblacktea",
		},
		{
			title:   "No FK constraints in database schema",
			context: "Referential integrity options: DB-level FK constraints vs application-level validation.",
			decision: "All referential integrity is enforced in the service/handler layer. " +
				"No FOREIGN KEY ... REFERENCES or ON DELETE CASCADE in any migration.",
			rationale: "FK constraints complicate zero-downtime migrations, create cross-table lock contention, " +
				"and make partial imports impossible. Application-layer validation gives the same guarantees.",
			alternatives: "FK constraints with ON DELETE CASCADE (rejected: migration complexity), " +
				"DB triggers (rejected: hidden side-effects).",
			repoName: "wayneblacktea",
		},
	}

	for _, sp := range specs {
		// Check idempotency: try to find by title via listing recent decisions.
		existing, err := store.All(ctx, 100)
		if err == nil {
			duplicate := false
			for _, d := range existing {
				if d.Title == sp.title {
					slog.Info("demo decision already exists, skipping", "title", sp.title)
					duplicate = true
					break
				}
			}
			if duplicate {
				continue
			}
		}

		_, err = store.Log(ctx, decision.LogParams{
			RepoName:     sp.repoName,
			Title:        sp.title,
			Context:      sp.context,
			Decision:     sp.decision,
			Rationale:    sp.rationale,
			Alternatives: sp.alternatives,
		})
		if err != nil {
			slog.Warn("failed to create demo decision", "title", sp.title, "err", err)
			continue
		}
		slog.Info("demo decision created", "title", sp.title)
	}
	return nil
}

// seedDemoKnowledge seeds 2 demo knowledge items (one article, one TIL).
func seedDemoKnowledge(ctx context.Context, store *knowledge.Store) error {
	type knowledgeSpec struct {
		itemType      string
		title         string
		content       string
		tags          []string
		learningValue int
	}
	specs := []knowledgeSpec{
		{
			itemType: string(knowledge.TypeArticle),
			title:    "How the Go scheduler works",
			content: "Go uses a cooperative, work-stealing M:N scheduler " +
				"(goroutines multiplexed over OS threads).\n\n" +
				"Key concepts:\n" +
				"- **G** (goroutine): lightweight user-space thread; stack starts at 8 KB.\n" +
				"- **M** (machine/OS thread): executes goroutines. One M per logical CPU core (GOMAXPROCS).\n" +
				"- **P** (processor): local run queue + context. Each P runs one goroutine at a time.\n\n" +
				"Goroutines are preempted at safe points (function call prologues, memory allocations). " +
				"Since Go 1.14, the runtime injects async preemption via OS signals.\n\n" +
				"Work-stealing: when a P's local queue is empty it steals half of another P's run queue.",
			tags:          []string{"go", "concurrency", "scheduler", "runtime"},
			learningValue: 4,
		},
		{
			itemType: string(knowledge.TypeTIL),
			title:    "TIL: context.WithCancelCause in Go 1.20",
			content: `Go 1.20 added context.WithCancelCause(parent) which returns a CancelCauseFunc.

Call cancelCause(err) to cancel with a specific error, then retrieve it with context.Cause(ctx).

This is useful for propagating the root cause through a goroutine tree without an extra error channel.

Example:
` + "```go" + `
ctx, cancel := context.WithCancelCause(context.Background())
go func() { cancel(ErrTokenExpired) }()
<-ctx.Done()
fmt.Println(context.Cause(ctx)) // prints: token expired
` + "```",
			tags:          []string{"go", "context", "til", "go-1.20"},
			learningValue: 3,
		},
	}

	for _, sp := range specs {
		_, err := store.AddItem(ctx, knowledge.AddItemParams{
			Type:          sp.itemType,
			Title:         sp.title,
			Content:       sp.content,
			Tags:          sp.tags,
			Source:        "demo-seed",
			LearningValue: sp.learningValue,
		})
		if err != nil {
			var dupErr knowledge.ErrDuplicate
			if errors.As(err, &dupErr) {
				slog.Info("demo knowledge item already exists, skipping", "title", sp.title)
				continue
			}
			slog.Warn("failed to create demo knowledge item", "title", sp.title, "err", err)
			continue
		}
		slog.Info("demo knowledge item created", "type", sp.itemType, "title", sp.title)
	}
	return nil
}

// seedDemoConcepts seeds 1 demo concept for spaced-repetition review.
func seedDemoConcepts(ctx context.Context, store *learning.Store) error {
	type conceptSpec struct {
		title   string
		content string
		tags    []string
	}
	specs := []conceptSpec{
		{
			title: "FSRS algorithm: stability and difficulty",
			content: "FSRS (Free Spaced Repetition Scheduler) models memory with two parameters:\n\n" +
				"**Stability (S)**: days until 90% recall probability. Increases with each review. " +
				"Higher stability → longer review interval.\n\n" +
				"**Difficulty (D)**: how hard the item is (1.0 = easiest, 10.0 = hardest). " +
				"Initialized from the first review rating and adjusted slowly over time.\n\n" +
				"**Review interval formula**: interval = S × ln(0.9) / ln(target_retention)\n\n" +
				"At target retention = 90%: interval ≈ S (days).\n\n" +
				"**Rating scale (1–4)**:\n" +
				"1 = Again (forgot, reset stability)\n" +
				"2 = Hard (remembered with difficulty, small increase)\n" +
				"3 = Good (remembered correctly, standard increase)\n" +
				"4 = Easy (too easy, large increase)",
			tags: []string{"spaced-repetition", "FSRS", "learning", "memory"},
		},
	}

	for _, sp := range specs {
		_, err := store.CreateConcept(ctx, sp.title, sp.content, sp.tags)
		if err != nil {
			// Postgres unique violation on title
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				slog.Info("demo concept already exists, skipping", "title", sp.title)
				continue
			}
			slog.Warn("failed to create demo concept", "title", sp.title, "err", err)
			continue
		}
		slog.Info("demo concept created", "title", sp.title)
	}
	return nil
}

func seedGoals(ctx context.Context, store *gtd.Store) int {
	type goalSpec struct {
		title   string
		area    string
		dueDate time.Time
	}
	due := func(year, month, day int) time.Time {
		return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	}
	goals := []goalSpec{
		{"Build wayneblacktea Personal OS", "engineering", due(2026, 7, 31)},
		{"Ship chat-gateway v2", "engineering", due(2026, 6, 30)},
	}

	created := 0
	for _, g := range goals {
		d := g.dueDate
		_, err := store.CreateGoal(ctx, gtd.CreateGoalParams{
			Title:   g.title,
			Area:    g.area,
			DueDate: &d,
		})
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				slog.Info("goal already exists, skipping", "title", g.title)
				continue
			}
			slog.Warn("failed to create goal", "title", g.title, "err", err)
			continue
		}
		slog.Info("goal created", "title", g.title)
		created++
	}
	return created
}

// repoSpec holds auto-discovered repo metadata.
type repoSpec struct {
	name     string
	path     string
	language string
	desc     string
}

func seedRepos(ctx context.Context, store *workspace.Store) int {
	root := os.Getenv("PROJECT_ROOT")
	if root == "" {
		slog.Warn("PROJECT_ROOT not set, skipping repo discovery")
		return 0
	}

	repos := discoverRepos(root)
	synced := 0
	for _, r := range repos {
		_, err := store.UpsertRepo(ctx, workspace.UpsertRepoParams{
			Name:        r.name,
			Path:        r.path,
			Language:    r.language,
			Description: r.desc,
		})
		if err != nil {
			slog.Warn("failed to upsert repo", "name", r.name, "err", err)
			continue
		}
		slog.Info("repo synced", "name", r.name, "lang", r.language)
		synced++
	}
	return synced
}

// discoverRepos scans root for code projects (root-level and one level deep for
// monorepo-style directories that contain nested modules).
func discoverRepos(root string) []repoSpec {
	entries, err := os.ReadDir(root)
	if err != nil {
		slog.Warn("cannot read PROJECT_ROOT", "path", root, "err", err)
		return nil
	}

	var repos []repoSpec
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(root, e.Name())
		lang := detectLanguage(dir)
		if lang != "" {
			repos = append(repos, repoSpec{
				name:     e.Name(),
				path:     dir,
				language: lang,
				desc:     autoDesc(e.Name(), lang),
			})
			continue
		}
		// No marker at root → scan one level deep (e.g. Flare-Go/auth, skcloud-*/subpkg)
		subs, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, sub := range subs {
			if !sub.IsDir() || strings.HasPrefix(sub.Name(), ".") {
				continue
			}
			subDir := filepath.Join(dir, sub.Name())
			subLang := detectLanguage(subDir)
			if subLang != "" {
				repos = append(repos, repoSpec{
					name:     e.Name() + "/" + sub.Name(),
					path:     subDir,
					language: subLang,
					desc:     autoDesc(sub.Name(), subLang),
				})
			}
		}
	}
	return repos
}

// detectLanguage returns the primary language of a directory based on marker files.
// Returns "" when no known marker is found.
func detectLanguage(dir string) string {
	switch {
	case fileExists(dir, "go.mod"):
		return "Go"
	case fileExists(dir, "pom.xml"), fileExists(dir, "build.gradle"), fileExists(dir, "build.gradle.kts"):
		return "Java"
	case fileExists(dir, "package.json"):
		if fileExists(dir, "tsconfig.json") {
			return "TypeScript"
		}
		return "JavaScript"
	case hasGlob(dir, "*.py"):
		return "Python"
	case fileExists(dir, "Cargo.toml"):
		return "Rust"
	default:
		return ""
	}
}

func autoDesc(name, lang string) string {
	labels := map[string]string{
		"Go":         "Go service",
		"TypeScript": "TypeScript app",
		"JavaScript": "JavaScript app",
		"Java":       "Java/Spring Boot service",
		"Python":     "Python project",
		"Rust":       "Rust project",
	}
	label := labels[lang]
	if label == "" {
		label = lang + " project"
	}
	return name + " — " + label
}

func fileExists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func hasGlob(dir, pattern string) bool {
	matches, err := filepath.Glob(filepath.Join(dir, pattern))
	return err == nil && len(matches) > 0
}
