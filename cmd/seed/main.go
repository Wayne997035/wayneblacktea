package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/waynechen/wayneblacktea/internal/gtd"
	"github.com/waynechen/wayneblacktea/internal/workspace"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL not set")
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("parsing database URL: %w", err)
	}
	// Aiven uses a custom CA not in the system trust store.
	cfg.ConnConfig.TLSConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // Aiven custom CA

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer pool.Close()

	ctx := context.Background()
	gtdStore := gtd.NewStore(pool)
	wsStore := workspace.NewStore(pool)

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

func seedRepos(ctx context.Context, store *workspace.Store) int {
	type repoSpec struct {
		name        string
		path        string
		language    string
		description string
	}
	repos := []repoSpec{
		{"chat-gateway", "/Users/waynechen/_project/chat-gateway", "Go", "Gin+gRPC gateway with MongoDB"},
		{"chatbot-go", "/Users/waynechen/_project/chatbot-go", "Go", "Echo LINE Bot with Redis"},
		{"chat-web", "/Users/waynechen/_project/chat-web", "TypeScript", "React 19 chat frontend"},
		{"wayneblacktea", "/Users/waynechen/_project/wayneblacktea", "Go", "Personal AI OS MCP server"},
		{"chatbot", "/Users/waynechen/_project/chatbot", "Java", "Spring Boot LINE Bot"},
	}

	synced := 0
	for _, r := range repos {
		_, err := store.UpsertRepo(ctx, workspace.UpsertRepoParams{
			Name:        r.name,
			Path:        r.path,
			Language:    r.language,
			Description: r.description,
		})
		if err != nil {
			slog.Warn("failed to upsert repo", "name", r.name, "err", err)
			continue
		}
		slog.Info("repo synced", "name", r.name)
		synced++
	}
	return synced
}
