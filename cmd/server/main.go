package main

import (
	"context"
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

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/discord"
	"github.com/Wayne997035/wayneblacktea/internal/discordbot"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	apimw "github.com/Wayne997035/wayneblacktea/internal/middleware"
	"github.com/Wayne997035/wayneblacktea/internal/notion"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/scheduler"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	echolog "github.com/labstack/echo/v4/middleware"
)

//go:embed web/dist
var staticFiles embed.FS

func main() {
	envFile := flag.String("env", ".env", "env file to load")
	flag.Parse()
	// Non-fatal: Railway injects env vars directly; .env is for local dev only.
	if err := godotenv.Load(*envFile); err != nil && !os.IsNotExist(err) {
		log.Fatalf("loading %s: %v", *envFile, err)
	}
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	backend, err := storage.ResolveFromEnv()
	if err != nil {
		return fmt.Errorf("resolving storage backend: %w", err)
	}
	log.Printf("storage backend: %s", backend)
	apiKey := os.Getenv("API_KEY")
	if apiKey == "" {
		return fmt.Errorf("API_KEY not set")
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	// allowedOrigins is validated by CORSMiddleware — empty or "*" will panic at startup.
	allowedOrigins := os.Getenv("ALLOWED_ORIGINS")

	stores, err := buildStores(backend)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := stores.Close(); cerr != nil {
			log.Printf("closing stores: %v", cerr)
		}
	}()
	discordClient := discord.NewClient()

	ctxH := handler.NewContextHandler(stores.GTD(), stores.Session())
	gtdH := handler.NewGTDHandler(stores.GTD())
	wsH := handler.NewWorkspaceHandler(stores.Workspace())
	decH := handler.NewDecisionHandler(stores.Decision())
	sessH := handler.NewSessionHandler(stores.Session())
	knowledgeH := handler.NewKnowledgeHandler(stores.Knowledge(), stores.Proposal())
	searchH := handler.NewSearchHandler(stores.Knowledge(), stores.Decision(), stores.GTD())
	learningH := handler.NewLearningHandler(stores.Learning(),
		handler.WithKnowledgeStore(stores.Knowledge()),
		handler.WithDecisionStore(stores.Decision()),
	)
	authSessH := handler.NewAuthSessionHandler(apiKey)
	var sum *ai.Summarizer
	var conceptReviewer ai.ConceptReviewerIface
	var clf *ai.ActivityClassifier
	if claudeKey := os.Getenv("CLAUDE_API_KEY"); claudeKey != "" {
		sum = ai.New(claudeKey)
		conceptReviewer = ai.NewConceptReviewer(claudeKey)
		clf = ai.NewActivityClassifier(claudeKey)
	}
	autologH := handler.NewAutologHandlerWithClassifier(stores.GTD(), stores.Session(), stores.Decision(), sum, clf)

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
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Response().Header().Set("Content-Security-Policy",
				"default-src 'self'; "+
					"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; "+
					"font-src 'self' https://fonts.gstatic.com; "+
					"script-src 'self'; "+
					"connect-src 'self'; "+
					"img-src 'self' data:")
			return next(c)
		}
	})

	e.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})
	// Browser SPA calls this once on startup to receive the wbt_session cookie.
	// Requires X-API-Key header; the SPA reads the key from VITE_API_KEY at build time.
	e.POST("/api/session", authSessH.IssueSession)

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

	api.GET("/search", searchH.Search, echolog.RateLimiter(echolog.NewRateLimiterMemoryStore(20)))

	api.GET("/learning/reviews", learningH.GetDueReviews)
	api.POST("/learning/reviews/:id/submit", learningH.SubmitReview)
	api.POST("/learning/concepts", learningH.CreateConcept)
	api.GET("/learning/suggestions", learningH.GetSuggestions)
	api.POST("/learning/from-knowledge", learningH.CreateConceptFromKnowledge)

	api.POST("/activity", autologH.LogActivity)
	api.POST("/auto-handoff", autologH.AutoHandoff)

	distFS, err := fs.Sub(staticFiles, "web/dist")
	if err != nil {
		return fmt.Errorf("embedding static files: %w", err)
	}
	e.GET("/*", echo.WrapHandler(buildSPAHandler(distFS)))

	notionClient := notion.NewClient()
	briefingStores := newBriefingStores(stores)
	sched, err := scheduler.New(stores.Learning(), discordClient, notionClient, briefingStores, conceptReviewer)
	if err != nil {
		return fmt.Errorf("creating scheduler: %w", err)
	}
	sched.Start()
	defer sched.Stop()

	stopBot, err := startDiscordBotIfConfigured(port, apiKey)
	if err != nil {
		return err
	}
	defer stopBot()

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

func buildSPAHandler(distFS fs.FS) http.Handler {
	spaFS := http.FS(distFS)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, err := spaFS.Open(r.URL.Path)
		if err != nil {
			r.URL.Path = "/"
		} else {
			_ = f.Close()
		}
		http.FileServer(spaFS).ServeHTTP(w, r)
	})
}

func startDiscordBotIfConfigured(port, apiKey string) (func(), error) {
	botToken := os.Getenv("DISCORD_BOT_TOKEN")
	if botToken == "" {
		return func() {}, nil
	}
	bot, err := discordbot.New(
		botToken,
		os.Getenv("GROQ_API_KEY"),
		"http://localhost:"+port,
		apiKey,
		os.Getenv("DISCORD_GUILD_ID"),
	)
	if err != nil {
		return nil, fmt.Errorf("creating discord bot: %w", err)
	}
	if err := bot.Start(); err != nil {
		return nil, fmt.Errorf("starting discord bot: %w", err)
	}
	log.Println("discord bot started")
	return bot.Stop, nil
}

func buildStores(backend storage.Backend) (storage.ServerStores, error) {
	stores, err := storage.BuildServerStores(context.Background(), backend)
	if err != nil {
		return nil, fmt.Errorf("building stores for backend %s: %w", backend, err)
	}
	return stores, nil
}

// briefingStoresAdapter implements notion.BriefingStores by delegating to
// the backend-agnostic StoreIface types from storage.ServerStores.
type briefingStoresAdapter struct {
	gtd      gtd.StoreIface
	learning learning.StoreIface
	proposal proposal.StoreIface
	decision decision.StoreIface
}

func (a *briefingStoresAdapter) Tasks(ctx context.Context, projectID *uuid.UUID) ([]db.Task, error) {
	tasks, err := a.gtd.Tasks(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("gtd tasks: %w", err)
	}
	return tasks, nil
}

func (a *briefingStoresAdapter) DueReviews(ctx context.Context, limit int) ([]learning.DueReview, error) {
	reviews, err := a.learning.DueReviews(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("due reviews: %w", err)
	}
	return reviews, nil
}

func (a *briefingStoresAdapter) ListPending(ctx context.Context) ([]db.PendingProposal, error) {
	pending, err := a.proposal.ListPending(ctx)
	if err != nil {
		return nil, fmt.Errorf("list pending proposals: %w", err)
	}
	return pending, nil
}

func (a *briefingStoresAdapter) All(ctx context.Context, limit int32) ([]db.Decision, error) {
	decisions, err := a.decision.All(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("all decisions: %w", err)
	}
	return decisions, nil
}

func (a *briefingStoresAdapter) WeeklyProgress(ctx context.Context) (completed, total int64, err error) {
	completed, total, err = a.gtd.WeeklyProgress(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("weekly progress: %w", err)
	}
	return completed, total, nil
}

func newBriefingStores(stores storage.ServerStores) notion.BriefingStores {
	return &briefingStoresAdapter{
		gtd:      stores.GTD(),
		learning: stores.Learning(),
		proposal: stores.Proposal(),
		decision: stores.Decision(),
	}
}
