package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	echolog "github.com/labstack/echo/v4/middleware"
	"github.com/waynechen/wayneblacktea/internal/decision"
	"github.com/waynechen/wayneblacktea/internal/gtd"
	"github.com/waynechen/wayneblacktea/internal/handler"
	apimw "github.com/waynechen/wayneblacktea/internal/middleware"
	"github.com/waynechen/wayneblacktea/internal/session"
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
	apiKey := os.Getenv("API_KEY")
	if apiKey == "" {
		return fmt.Errorf("API_KEY not set")
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	allowedOrigins := os.Getenv("ALLOWED_ORIGINS")
	if allowedOrigins == "" {
		allowedOrigins = "*"
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

	gtdStore := gtd.NewStore(pool)
	wsStore := workspace.NewStore(pool)
	decStore := decision.NewStore(pool)
	sessStore := session.NewStore(pool)

	ctxH := handler.NewContextHandler(gtdStore, sessStore)
	gtdH := handler.NewGTDHandler(gtdStore)
	wsH := handler.NewWorkspaceHandler(wsStore)
	decH := handler.NewDecisionHandler(decStore)
	sessH := handler.NewSessionHandler(sessStore)

	e := echo.New()
	e.HideBanner = true
	e.Use(echolog.RequestLogger())
	e.Use(echolog.Recover())
	e.Use(echolog.BodyLimit("1M"))
	e.Use(apimw.CORSMiddleware(allowedOrigins))

	e.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	api := e.Group("/api", apimw.APIKeyMiddleware(apiKey))

	api.GET("/context/today", ctxH.GetTodayContext)

	api.GET("/goals", gtdH.ListGoals)
	api.POST("/goals", gtdH.CreateGoal)

	api.GET("/projects", gtdH.ListProjects)
	api.POST("/projects", gtdH.CreateProject)
	api.GET("/projects/:id", gtdH.GetProject)
	api.PATCH("/projects/:id/status", gtdH.UpdateProjectStatus)
	api.GET("/projects/:id/tasks", gtdH.ListProjectTasks)

	api.POST("/tasks", gtdH.CreateTask)
	api.PATCH("/tasks/:id/status", gtdH.UpdateTaskStatus)
	api.PATCH("/tasks/:id/complete", gtdH.CompleteTask)

	api.GET("/decisions", decH.ListDecisions)
	api.POST("/decisions", decH.LogDecision)

	api.GET("/workspace/repos", wsH.ListRepos)
	api.POST("/workspace/repos", wsH.UpsertRepo)

	api.GET("/session/handoff", sessH.GetHandoff)
	api.POST("/session/handoff", sessH.SetHandoff)

	if err := e.Start(":" + port); err != nil {
		return fmt.Errorf("server: %w", err)
	}
	return nil
}
