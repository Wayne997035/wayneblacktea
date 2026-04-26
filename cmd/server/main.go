package main

import (
	"context"
	"crypto/tls"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	_ "time/tzdata" // embed IANA timezone DB so Asia/Taipei works on any base image

	"github.com/joho/godotenv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	echolog "github.com/labstack/echo/v4/middleware"
	pgvectorpgx "github.com/pgvector/pgvector-go/pgx"
	"github.com/waynechen/wayneblacktea/internal/decision"
	"github.com/waynechen/wayneblacktea/internal/discord"
	"github.com/waynechen/wayneblacktea/internal/discordbot"
	"github.com/waynechen/wayneblacktea/internal/gtd"
	"github.com/waynechen/wayneblacktea/internal/handler"
	"github.com/waynechen/wayneblacktea/internal/knowledge"
	"github.com/waynechen/wayneblacktea/internal/learning"
	apimw "github.com/waynechen/wayneblacktea/internal/middleware"
	"github.com/waynechen/wayneblacktea/internal/scheduler"
	"github.com/waynechen/wayneblacktea/internal/search"
	"github.com/waynechen/wayneblacktea/internal/session"
	"github.com/waynechen/wayneblacktea/internal/workspace"
)

//go:embed web/dist
var staticFiles embed.FS

func main() {
	envFile := flag.String("env", ".env", "env file to load")
	flag.Parse()
	if err := godotenv.Load(*envFile); err != nil {
		log.Fatalf("loading %s: %v", *envFile, err)
	}
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
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		return pgvectorpgx.RegisterTypes(ctx, conn)
	}

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer pool.Close()

	gtdStore := gtd.NewStore(pool)
	wsStore := workspace.NewStore(pool)
	decStore := decision.NewStore(pool)
	sessStore := session.NewStore(pool)
	embedClient := search.NewEmbeddingClient()
	knowledgeStore := knowledge.NewStore(pool, embedClient)
	learningStore := learning.NewStore(pool)
	discordClient := discord.NewClient()

	ctxH := handler.NewContextHandler(gtdStore, sessStore)
	gtdH := handler.NewGTDHandler(gtdStore)
	wsH := handler.NewWorkspaceHandler(wsStore)
	decH := handler.NewDecisionHandler(decStore)
	sessH := handler.NewSessionHandler(sessStore)
	knowledgeH := handler.NewKnowledgeHandler(knowledgeStore)
	learningH := handler.NewLearningHandler(learningStore)

	e := echo.New()
	e.HideBanner = true
	e.Use(echolog.RequestLoggerWithConfig(echolog.RequestLoggerConfig{
		LogMethod: true, LogURI: true, LogStatus: true,
		LogLatency: true, LogHost: true, LogError: true,
		LogValuesFunc: func(c echo.Context, v echolog.RequestLoggerValues) error {
			if v.URI == "/health" {
				return nil
			}
			fmt.Fprintf(os.Stdout, "INFO REQUEST method=%s uri=%s status=%d latency=%s host=%s\n",
				v.Method, v.URI, v.Status, v.Latency, v.Host)
			return nil
		},
	}))
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

	api.GET("/knowledge", knowledgeH.ListKnowledge)
	api.POST("/knowledge", knowledgeH.AddKnowledge)
	api.GET("/knowledge/search", knowledgeH.SearchKnowledge)

	api.GET("/learning/reviews", learningH.GetDueReviews)
	api.POST("/learning/reviews/:id/submit", learningH.SubmitReview)
	api.POST("/learning/concepts", learningH.CreateConcept)

	// Serve the React SPA for all non-API routes (must be registered last).
	// Unknown paths fall back to index.html so client-side routing works.
	distFS, err := fs.Sub(staticFiles, "web/dist")
	if err != nil {
		return fmt.Errorf("embedding static files: %w", err)
	}
	spaFS := http.FS(distFS)
	spaHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, err := spaFS.Open(r.URL.Path)
		if err != nil {
			r.URL.Path = "/"
		} else {
			f.Close()
		}
		http.FileServer(spaFS).ServeHTTP(w, r)
	})
	e.GET("/*", echo.WrapHandler(spaHandler))

	// Start scheduler.
	sched, err := scheduler.New(learningStore, discordClient)
	if err != nil {
		return fmt.Errorf("creating scheduler: %w", err)
	}
	sched.Start()
	defer sched.Stop()

	// Start Discord bot if token is configured.
	if botToken := os.Getenv("DISCORD_BOT_TOKEN"); botToken != "" {
		bot, err := discordbot.New(botToken, os.Getenv("GROQ_API_KEY"), "http://localhost:"+port, apiKey, os.Getenv("DISCORD_GUILD_ID"))
		if err != nil {
			return fmt.Errorf("creating discord bot: %w", err)
		}
		if err := bot.Start(); err != nil {
			return fmt.Errorf("starting discord bot: %w", err)
		}
		defer bot.Stop()
		log.Println("discord bot started")
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		if err := e.Shutdown(context.Background()); err != nil {
			log.Printf("server shutdown error: %v", err)
		}
	}()

	if err := e.Start(":" + port); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server: %w", err)
	}
	return nil
}
