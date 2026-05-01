package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	wbtruntime "github.com/Wayne997035/wayneblacktea/internal/runtime"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	if _, err := storage.ResolveFromEnv(); err != nil {
		return fmt.Errorf("resolving storage backend: %w", err)
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL not set")
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
