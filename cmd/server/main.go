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

	"github.com/Wayne997035/wayneblacktea/internal/discord"
	"github.com/Wayne997035/wayneblacktea/internal/discordbot"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	apimw "github.com/Wayne997035/wayneblacktea/internal/middleware"
	"github.com/Wayne997035/wayneblacktea/internal/scheduler"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
	"github.com/labstack/echo/v4"
	echolog "github.com/labstack/echo/v4/middleware"
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
	allowedOrigins := os.Getenv("ALLOWED_ORIGINS")
	if allowedOrigins == "" {
		allowedOrigins = "*"
	}

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
	learningH := handler.NewLearningHandler(stores.Learning())

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
	e.GET("/*", echo.WrapHandler(buildSPAHandler(distFS)))

	// Start scheduler.
	sched, err := scheduler.New(stores.Learning(), discordClient)
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

// startDiscordBotIfConfigured starts the Discord bot when DISCORD_BOT_TOKEN is
// set, returning a stop function the caller defers. When the token is unset,
// returns a no-op stop function so the caller can defer unconditionally.
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
