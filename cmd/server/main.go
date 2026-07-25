package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // embed IANA timezone DB so Asia/Taipei works on any base image

	"github.com/joho/godotenv"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/aicost"
	"github.com/Wayne997035/wayneblacktea/internal/completioncandidate"
	"github.com/Wayne997035/wayneblacktea/internal/decay"
	"github.com/Wayne997035/wayneblacktea/internal/discord"
	"github.com/Wayne997035/wayneblacktea/internal/discordbot"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/llm"
	mcpsrv "github.com/Wayne997035/wayneblacktea/internal/mcp"
	"github.com/Wayne997035/wayneblacktea/internal/mergedprs"
	apimw "github.com/Wayne997035/wayneblacktea/internal/middleware"
	"github.com/Wayne997035/wayneblacktea/internal/notion"
	"github.com/Wayne997035/wayneblacktea/internal/scheduler"
	"github.com/Wayne997035/wayneblacktea/internal/snapshot"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
	"github.com/Wayne997035/wayneblacktea/internal/timeline"
	"github.com/labstack/echo/v4"
	echolog "github.com/labstack/echo/v4/middleware"
	mcphttp "github.com/mark3labs/mcp-go/server"
	"golang.org/x/time/rate"
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

// run is the composition root: it resolves config/env, builds the store
// bundle, wires each subsystem (AI collaborators, HTTP handlers, the
// scheduler) exactly once, then starts serving. Each subsystem's wiring
// detail lives in its own wire*/build* function below — run() itself should
// read as the seam between subsystems, not the wiring of any one of them.
func run() error {
	backend, err := storage.ResolveFromEnv()
	if err != nil {
		return fmt.Errorf("resolving storage backend: %w", err)
	}
	log.Printf("storage backend: %s", backend)
	apiKey := os.Getenv("API_KEY")
	if err := validateAPIKey(apiKey); err != nil {
		return err
	}
	if env := os.Getenv("RAILWAY_ENVIRONMENT"); env != "" && os.Getenv("APP_ENV") == "" {
		// Secure-by-default: Railway's RAILWAY_ENVIRONMENT is authoritative on
		// Railway-hosted deploys. Inheriting APP_ENV avoids cookie Secure flag
		// being off in production when operator forgets to set APP_ENV
		// explicitly. Set APP_ENV manually to override (e.g. staging-on-Railway).
		if err := os.Setenv("APP_ENV", env); err != nil {
			log.Printf("WARNING: failed to inherit APP_ENV from RAILWAY_ENVIRONMENT=%q: %v", env, err)
		} else {
			log.Printf("INFO: APP_ENV inherited from RAILWAY_ENVIRONMENT=%q (set APP_ENV explicitly to override)", env)
		}
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8420"
	}
	allowedOrigins, err := resolveAllowedOrigins(port)
	if err != nil {
		return err
	}

	// Auto-migrate is the server's responsibility, not the MCP stdio client's.
	// MCP stdio (`wbt mcp`) intentionally skips migration: short-lived stdio
	// processes that die mid-`pg_advisory_lock` leak the migration lock and
	// stall every subsequent boot. Migration runs here so the long-lived
	// server owns lock acquisition + release. Set WBT_AUTO_MIGRATE=false to
	// disable (e.g. CI). SQLite skips migrations (schema.sql handles it).
	//
	// 2-minute ceiling on lock acquisition: if Aiven is unreachable or another
	// process holds the migration lock, fail-closed at startup instead of
	// hanging indefinitely. golang-migrate's pgx/v5 driver honours context
	// cancellation on the advisory_lock query.
	if backend == storage.BackendPostgres {
		migrateCtx, migrateCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		err := storage.RunMigrations(migrateCtx, os.Getenv("DATABASE_URL"))
		migrateCancel()
		if err != nil {
			return fmt.Errorf("auto-migrate: %w", err)
		}
	}

	// --- Stores ---------------------------------------------------------
	stores, err := buildStores(backend)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := stores.Close(); cerr != nil {
			log.Printf("closing stores: %v", cerr)
		}
	}()

	// Build snapshot deps early so both the context handler and scheduler
	// can share the same store. mcpsrv.Resolve* is the single source of
	// truth for backend-selection logic — also used by run() below to wire
	// the same instances into the MCP server via WireOptionalCapabilities.
	snapStore, snapGen := mcpsrv.ResolveSnapshotStore(stores)

	// Derived stores (completion candidates / merged PRs / activity) are
	// cheap to construct — each call returns an independent value backed by
	// the same shared connection pool — but building once here and passing
	// the value into every wiring function that needs it keeps that
	// convention visible at a single call site instead of re-derived at each
	// use.
	candidateStore := mcpsrv.ResolveCandidateStore(stores)
	mergedPRsStore := mcpsrv.ResolveMergedPRsStore(stores)
	activityStore := buildActivityStore(stores)

	// --- AI collaborators -------------------------------------------------
	aiw := wireAI(stores)
	if aiw.chain.Len() == 0 {
		log.Println("llm: memory-only mode (no provider configured)")
	} else {
		log.Printf("llm: provider chain = %v", aiw.chain.Names())
	}

	// --- HTTP handlers ------------------------------------------------
	handlers := wireHandlers(stores, aiw, apiKey, snapStore, candidateStore, mergedPRsStore, activityStore)
	defer handlers.postToolUse.Stop()

	e := echo.New()
	e.HideBanner = true
	ipExtractor, err := resolveIPExtractor(os.Getenv("TRUSTED_PROXY_CIDR"))
	if err != nil {
		return err
	}
	e.IPExtractor = ipExtractor
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
					"img-src 'self' data:; "+
					"frame-ancestors 'none'")
			c.Response().Header().Set("X-Frame-Options", "DENY")
			c.Response().Header().Set("X-Content-Type-Options", "nosniff")
			c.Response().Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
			if os.Getenv("APP_ENV") == "production" {
				c.Response().Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			return next(c)
		}
	})

	e.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})
	// Browser login page POSTs the user-entered key once to receive the wbt_session cookie.
	// Rate-limited to 10 req/min per IP to mitigate brute-force on the key.
	sessionRL := echolog.RateLimiter(echolog.NewRateLimiterMemoryStoreWithConfig(echolog.RateLimiterMemoryStoreConfig{
		Rate:      rate.Limit(10.0 / 60.0), // 10 req/min per IP
		Burst:     3,
		ExpiresIn: 5 * time.Minute,
	}))
	e.POST("/api/session", handlers.authSession.IssueSession, sessionRL)

	api := e.Group("/api", apimw.APIKeyMiddleware(apiKey))
	mutationRL := echolog.RateLimiter(echolog.NewRateLimiterMemoryStore(30))

	api.GET("/context/today", handlers.ctx.GetTodayContext)

	api.GET("/goals", handlers.gtd.ListGoals)
	api.POST("/goals", handlers.gtd.CreateGoal, mutationRL)
	api.PATCH("/goals/:id", handlers.gtd.UpdateGoal, mutationRL)

	api.GET("/projects", handlers.gtd.ListProjects)
	api.POST("/projects", handlers.gtd.CreateProject, mutationRL)
	api.GET("/projects/:id", handlers.gtd.GetProject)
	api.PATCH("/projects/:id", handlers.gtd.UpdateProject, mutationRL)
	api.GET("/projects/:id/tasks", handlers.gtd.ListProjectTasks)

	api.GET("/tasks", handlers.gtd.ListTasks)
	api.POST("/tasks", handlers.gtd.CreateTask, mutationRL)
	api.PATCH("/tasks/:id", handlers.gtd.UpdateTask, mutationRL)
	api.PATCH("/tasks/:id/complete", handlers.gtd.CompleteTask, mutationRL)

	// reconcile handler wiring (sprint feature/gtd-enforce-server-side GTD-fix 9/12)
	// Closes the "PR merged but task still pending" gap: Claude posts a list of
	// recently-merged PRs and the server auto-applies the matching tasks.
	// Rate limit shared with other mutation endpoints (30 req/min) — reconcile
	// is bursty (post-merge sweep) but capped by 200-entry payload limit anyway.
	api.POST("/tasks/reconcile-merged-prs", handlers.reconcile.Reconcile, mutationRL)

	api.GET("/decisions", handlers.decision.ListDecisions)
	api.POST("/decisions", handlers.decision.LogDecision, mutationRL)

	api.GET("/workspace/repos", handlers.workspace.ListRepos)
	api.POST("/workspace/repos", handlers.workspace.UpsertRepo, mutationRL)
	api.GET("/workspace/repos/:id/overview", handlers.workspaceOv.GetRepoOverview)
	// GET /workspace/settings is intentionally un-rate-limited: reads are cheap
	// and rate-limiting only mutations (mutationRL) is the convention for all
	// other GET endpoints in this file.
	api.GET("/workspace/settings", handlers.workspace.GetSettings)
	api.PATCH("/workspace/settings", handlers.workspace.PatchSettings, mutationRL)

	// handoffRL caps POST /auto-handoff at 5 req/min — each request may
	// spawn a Gemini embedding call; keep well below Gemini free-tier quota.
	handoffRL := echolog.RateLimiter(echolog.NewRateLimiterMemoryStore(5))

	api.GET("/vision", handlers.vision.ListVision)
	api.POST("/vision", handlers.vision.AddVision, mutationRL)
	api.PATCH("/vision/:id", handlers.vision.UpdateVision, mutationRL)

	// /knowledge/search has a side-effect (bumps recall_count + last_recalled_at
	// on every hit) so a high-rate caller can permanently subvert the Ebbinghaus
	// decay prune by inflating recall_count past the threshold. Cap at 20 RPS
	// per IP — same tier as /search (security audit M-1).
	knowledgeRL := echolog.RateLimiter(echolog.NewRateLimiterMemoryStore(20))
	api.GET("/knowledge", handlers.knowledge.ListKnowledge)
	api.POST("/knowledge", handlers.knowledge.AddKnowledge, knowledgeRL)
	api.PATCH("/knowledge/:id", handlers.knowledge.UpdateLearningValue, knowledgeRL)
	api.GET("/knowledge/search", handlers.knowledge.SearchKnowledge, knowledgeRL)

	proposalRL := echolog.RateLimiter(echolog.NewRateLimiterMemoryStore(10))
	// GET /api/proposals supports ?status=pending|accepted|rejected|all (UX-5).
	api.GET("/proposals", handlers.proposal.ListProposals, proposalRL)
	// Legacy endpoint kept for backward compat; clients can migrate to /proposals?status=pending.
	api.GET("/proposals/pending", handlers.proposal.ListPendingProposals, proposalRL)
	// confirm-batch registered before /:id/confirm so Echo's router matches the
	// literal segment before the :id wildcard.
	api.POST("/proposals/confirm-batch", handlers.proposal.ConfirmBatch, proposalRL)
	api.POST("/proposals/:id/confirm", handlers.proposal.ConfirmProposal, proposalRL)

	api.GET("/search", handlers.search.Search, echolog.RateLimiter(echolog.NewRateLimiterMemoryStore(20)))

	dashboardRL := echolog.RateLimiter(echolog.NewRateLimiterMemoryStore(30))
	api.GET("/dashboard/stats", handlers.dashboard.GetStats, dashboardRL)
	api.GET("/dashboard/recent-decisions", handlers.dashboard.GetRecentDecisions, dashboardRL)
	api.GET("/dashboard/next-task", handlers.dashboard.GetNextTask, dashboardRL)
	api.GET("/dashboard/upcoming-tasks", handlers.dashboard.GetUpcomingTasks, dashboardRL)
	api.GET("/dashboard/upcoming", handlers.dashboard.GetUpcoming, dashboardRL)
	api.GET("/dashboard/automation-health", handlers.dashboard.GetAutomationHealth, dashboardRL)
	api.GET("/dashboard/automation-feed", handlers.dashboard.GetAutomationFeed, dashboardRL)
	// ai-cost ledger (1.5-C): per-model token + cost aggregation, last 30d.
	api.GET("/dashboard/ai-cost", handlers.dashboard.GetAICost, dashboardRL)

	timelineRL := echolog.RateLimiter(echolog.NewRateLimiterMemoryStore(10))
	api.GET("/timeline", handlers.timeline.GetTimeline, timelineRL)

	api.GET("/learning/reviews", handlers.learning.GetDueReviews)
	api.POST("/learning/reviews/:id/submit", handlers.learning.SubmitReview, mutationRL)
	api.POST("/learning/concepts", handlers.learning.CreateConcept, mutationRL)
	api.GET("/learning/suggestions", handlers.learning.GetSuggestions)
	api.POST("/learning/from-knowledge", handlers.learning.CreateConceptFromKnowledge, mutationRL)
	// UX-6: learning history and stats.
	api.GET("/learning/history", handlers.learning.GetHistory)
	api.GET("/learning/stats", handlers.learning.GetStats)

	activityRL := echolog.RateLimiter(echolog.NewRateLimiterMemoryStore(30))
	// postToolUseRL is deliberately more permissive (120 req/min) because
	// wbt-hook fires on every Claude Code tool call, including fast loops.
	postToolUseRL := echolog.RateLimiter(echolog.NewRateLimiterMemoryStore(120))
	api.POST("/activity", handlers.autolog.LogActivity, activityRL)
	api.POST("/activity/posttooluse", handlers.postToolUse.PostToolUse, postToolUseRL)
	api.POST("/auto-handoff", handlers.autolog.AutoHandoff, handoffRL)

	// HTTP MCP transport: mount the MCP server at /mcp so Claude Code can connect via:
	//   claude mcp add --transport http wayneblacktea http://localhost:8420/mcp
	// The same stores used by HTTP handlers are reused — no separate DB connections needed.
	mcpServer, err := mcpsrv.New(stores)
	if err != nil {
		return fmt.Errorf("initializing MCP server: %w", err)
	}
	// Single shared assembly function for both MCP transports — see
	// internal/mcp/capabilities.go. stdio (internal/mcprunner.Run) calls the
	// same function so the two transports cannot silently drift apart again.
	capReport := mcpServer.WireOptionalCapabilities(mcpsrv.CapabilityInputs{
		Chain:          aiw.chain,
		SnapshotStore:  snapStore,
		SnapshotGen:    snapGen,
		CandidateStore: candidateStore,
		MergedPRsStore: mergedPRsStore,
	})
	log.Printf("mcp: http transport capabilities = %+v", capReport)
	handlers.knowledge.WithAtomizer(mcpServer.LaunchAtomize)
	httpMCPHandler := mcphttp.NewStreamableHTTPServer(mcpServer.MCPServer())
	e.Any("/mcp", echo.WrapHandler(httpMCPHandler), apimw.APIKeyMiddleware(apiKey))

	distFS, err := fs.Sub(staticFiles, "web/dist")
	if err != nil {
		return fmt.Errorf("embedding static files: %w", err)
	}
	e.GET("/*", echo.WrapHandler(buildSPAHandler(distFS)))

	// --- Scheduler ------------------------------------------------------
	discordClient := discord.NewClient()
	notionClient := notion.NewClient()
	briefingStores := notion.NewBriefingStores(stores)
	pruner := buildPruner(stores)

	sched, err := wireScheduler(stores, aiw, discordClient, notionClient, briefingStores, snapStore, snapGen, pruner, candidateStore, mergedPRsStore)
	if err != nil {
		return err
	}
	sched.Start()
	defer sched.Stop()

	stopBot, err := startDiscordBotIfConfigured(port, apiKey, aiw.chain)
	if err != nil {
		return err
	}
	defer stopBot()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer shutCancel()
		if err := e.Shutdown(shutCtx); err != nil {
			log.Printf("server shutdown error: %v", err)
		}
	}()

	if err := e.Start(":" + port); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server: %w", err)
	}
	return nil
}

func validateAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("API_KEY not set")
	}
	if len(apiKey) < 32 {
		return fmt.Errorf("API_KEY must be at least 32 characters")
	}
	return nil
}

func resolveIPExtractor(rawTrustedCIDRs string) (echo.IPExtractor, error) {
	rawTrustedCIDRs = strings.TrimSpace(rawTrustedCIDRs)
	if rawTrustedCIDRs == "" {
		return echo.ExtractIPDirect(), nil
	}

	parts := strings.FieldsFunc(rawTrustedCIDRs, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})
	options := make([]echo.TrustOption, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		_, ipNet, err := net.ParseCIDR(part)
		if err != nil {
			return nil, fmt.Errorf("TRUSTED_PROXY_CIDR invalid CIDR %q: %w", part, err)
		}
		options = append(options, echo.TrustIPRange(ipNet))
	}
	if len(options) == 0 {
		return echo.ExtractIPDirect(), nil
	}
	return echo.ExtractIPFromXFFHeader(options...), nil
}

// resolveAllowedOrigins determines the CORS allowed origins at startup.
//
// Rules:
//   - ALLOWED_ORIGINS="*" is always rejected (wildcard is unsafe with AllowCredentials=true).
//   - ALLOWED_ORIGINS non-empty → returned as-is.
//   - ALLOWED_ORIGINS empty + APP_ENV="production" → error (production must be explicit).
//   - ALLOWED_ORIGINS empty + any other APP_ENV → default to localhost on the given port.
func resolveAllowedOrigins(port string) (string, error) {
	raw := strings.TrimSpace(os.Getenv("ALLOWED_ORIGINS"))
	appEnv := strings.TrimSpace(os.Getenv("APP_ENV"))

	if raw == "*" {
		return "", fmt.Errorf("ALLOWED_ORIGINS must not be '*' when AllowCredentials=true; set explicit origins")
	}
	if raw != "" {
		return raw, nil
	}
	if appEnv == "production" {
		return "", fmt.Errorf("ALLOWED_ORIGINS must be set in production (APP_ENV=production); got empty value")
	}
	// local / dev default — safe because AllowCredentials only grants access to
	// origins that the browser has already confirmed are same-scheme/host.
	// Warn unconditionally so that operators catch missing ALLOWED_ORIGINS in
	// production environments that do not set APP_ENV=production.
	log.Printf("WARN: ALLOWED_ORIGINS not set — defaulting to localhost:%s; set ALLOWED_ORIGINS explicitly in production", port)
	return fmt.Sprintf("http://localhost:%s,http://127.0.0.1:%s", port, port), nil
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

func startDiscordBotIfConfigured(port, apiKey string, llmClient llm.JSONClient) (func(), error) {
	if strings.EqualFold(os.Getenv("DISCORD_ENV"), "local") {
		log.Println("discord bot: DISCORD_ENV=local — skipping bot startup (local dev mode)")
		return func() {}, nil
	}
	botToken := os.Getenv("DISCORD_BOT_TOKEN")
	if botToken == "" {
		return func() {}, nil
	}
	bot, err := discordbot.New(
		botToken,
		"http://localhost:"+port,
		apiKey,
		os.Getenv("DISCORD_GUILD_ID"),
		os.Getenv("DISCORD_ALLOWED_USER_IDS"),
		llmClient,
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

// aiWiring bundles the LLM provider chain and the optional AI-backed
// collaborators wireAI constructs. summarizer / conceptReviewer / classifier /
// reflector are nil when their precondition isn't met (empty provider chain
// for classifier/conceptReviewer, missing CLAUDE_API_KEY for
// summarizer/reflector) — callers must nil-check before use, exactly as
// before this refactor.
type aiWiring struct {
	chain           *llm.Chain
	costRecorder    aicost.Recorder
	summarizer      *ai.Summarizer
	conceptReviewer ai.ConceptReviewerIface
	classifier      *ai.ActivityClassifier
	reflector       ai.ReflectorIface
}

// wireAI builds the LLM provider chain (Phase 1-4 of docs/openrouter-fallback.md):
// classifier + concept reviewer go through the provider abstraction so they
// pick up OpenRouter / Groq fallback automatically. Reflector and Summarizer
// remain Claude-only this phase (deferred to Phase 5).
func wireAI(stores storage.ServerStores) aiWiring {
	chain := llm.BuildChainFromEnv()

	// costRecorder: PG-only (ai_cost_ledger table lives in Postgres). When
	// SQLite is the backend the NopRecorder is used so AI calls are never
	// blocked by a missing table.
	var costRecorder aicost.Recorder = aicost.NopRecorder{}
	if pool := stores.PgxPool(); pool != nil {
		costRecorder = aicost.NewPgRecorder(pool)
	}

	var sum *ai.Summarizer
	var conceptReviewer ai.ConceptReviewerIface
	var clf *ai.ActivityClassifier
	var reflector ai.ReflectorIface
	if chain.Len() > 0 {
		clf = ai.NewActivityClassifierFromLLM(chain).
			WithCostRecorder(costRecorder, stores.WorkspaceID())
		conceptReviewer = ai.NewConceptReviewerFromLLM(chain).
			WithCostRecorder(costRecorder, stores.WorkspaceID())
	}
	if claudeKey := os.Getenv("CLAUDE_API_KEY"); claudeKey != "" {
		// Summarizer and reflector still bind to Claude directly until
		// Phase 5 of the spec. They are independent of the provider chain.
		// Per-workspace model preference: the summarizer consults the workspace
		// DB setting first, falling back to CLAUDE_SUMMARY_MODEL env / default.
		sum = ai.NewWithPreference(claudeKey, stores.Workspace()).
			WithCostRecorder(costRecorder, stores.WorkspaceID())
		reflector = ai.NewReflector(claudeKey).
			WithCostRecorder(costRecorder, stores.WorkspaceID())
	}
	return aiWiring{
		chain:           chain,
		costRecorder:    costRecorder,
		summarizer:      sum,
		conceptReviewer: conceptReviewer,
		classifier:      clf,
		reflector:       reflector,
	}
}

// serverHandlers bundles the HTTP handler instances wireHandlers constructs.
// run() uses the fields to register echo routes and to wire the MCP atomizer
// callback — a plain data holder for wiring output, not a DI container.
type serverHandlers struct {
	ctx         *handler.ContextHandler
	gtd         *handler.GTDHandler
	workspace   *handler.WorkspaceHandler
	workspaceOv *handler.WorkspaceOverviewHandler
	decision    *handler.DecisionHandler
	knowledge   *handler.KnowledgeHandler
	proposal    *handler.ProposalHandler
	search      *handler.SearchHandler
	learning    *handler.LearningHandler
	dashboard   *handler.DashboardHandler
	vision      *handler.VisionHandler
	authSession *handler.AuthSessionHandler
	timeline    *handler.TimelineHandler
	autolog     *handler.AutologHandler
	postToolUse *handler.PostToolUseHandler
	reconcile   *handler.ReconcileHandler
}

// wireHandlers constructs the HTTP handlers run() registers as echo routes.
// candidateStore / mergedPRsStore / activityStore are built once in run()
// and passed in rather than rebuilt here (see the buildCandidateStore /
// buildMergedPRsStore doc comments on why repeated construction is safe but
// unnecessary).
func wireHandlers(
	stores storage.ServerStores,
	aiw aiWiring,
	apiKey string,
	snapStore snapshot.StoreIface,
	candidateStore completioncandidate.Store,
	mergedPRsStore mergedprs.Store,
	activityStore handler.DashboardActivityStoreIface,
) *serverHandlers {
	ctxH := handler.NewContextHandler(stores.GTD(), stores.Session())
	if snapStore != nil {
		ctxH.WithSnapshotStore(snapStore)
	}
	gtdH := handler.NewGTDHandler(stores.GTD())
	wsH := handler.NewWorkspaceHandler(stores.Workspace())
	wsOverviewH := handler.NewWorkspaceOverviewHandler(stores.Workspace(), stores.GTD(), stores.Decision(), stores.Session())
	decH := handler.NewDecisionHandler(stores.Decision())
	knowledgeH := handler.NewKnowledgeHandler(stores.Knowledge(), stores.Proposal())
	proposalH := handler.NewProposalHandler(stores.Proposal(), stores.Learning()).
		WithDecision(stores.Decision()).
		WithKnowledge(stores.Knowledge()).
		WithTask(stores.GTD())
	searchH := handler.NewSearchHandler(stores.Knowledge(), stores.Decision(), stores.GTD())
	learningH := handler.NewLearningHandler(
		stores.Learning(),
		handler.WithKnowledgeStore(stores.Knowledge()),
		handler.WithDecisionStore(stores.Decision()),
	)
	dashH := handler.NewDashboardHandler(stores.GTD(), stores.Decision(), stores.Proposal())
	if candidateStore != nil {
		dashH.SetCandidateStore(candidateStore)
	}
	dashH.SetHandoffStore(stores.Session())
	if activityStore != nil {
		dashH.SetActivityStore(activityStore)
	}
	if pool := stores.PgxPool(); pool != nil {
		dashH.SetAICostStore(aicost.NewPgStore(pool, stores.WorkspaceID()))
	}
	visionH := handler.NewVisionHandler(stores.Vision())
	authSessH := handler.NewAuthSessionHandler(apiKey)
	timelineAgg := &timeline.Aggregator{
		Tasks:     stores.GTD(),
		Decisions: stores.Decision(),
		Activity:  stores.GTD(),
		Knowledge: stores.Knowledge(),
		Concepts:  stores.Learning(),
		Reviews:   stores.Learning(),
		Handoffs:  stores.Session(),
		Visions:   stores.Vision(),
	}
	timelineH := handler.NewTimelineHandler(timelineAgg)
	// auto-capture proposal wiring (sprint feature/gtd-enforce-server-side TASK 2)
	// Routes IsTask=true classifier verdicts through the TypeTask proposal queue
	// instead of direct task creation, so the user reviews and validator runs at
	// confirm time (SA decision 42e0b783). nil-safe: if stores.Proposal() is nil
	// (no SQLite / no PG), AutologHandler falls back to legacy direct create.
	autologH := handler.NewAutologHandlerWithClassifierAndProposal(
		stores.GTD(), stores.Session(), stores.Decision(), aiw.summarizer, aiw.classifier, stores.Proposal(),
	)
	postToolUseH := handler.NewPostToolUseHandler(stores.GTD())
	// reconcile handler wiring (sprint feature/gtd-enforce-server-side GTD-fix 9/12):
	// candidate/merged-PRs stores may be nil under unusual configs; the handler
	// tolerates nil.
	reconcileH := handler.NewReconcileHandler(stores.GTD(), candidateStore).
		WithMergedPRsStore(mergedPRsStore)

	return &serverHandlers{
		ctx:         ctxH,
		gtd:         gtdH,
		workspace:   wsH,
		workspaceOv: wsOverviewH,
		decision:    decH,
		knowledge:   knowledgeH,
		proposal:    proposalH,
		search:      searchH,
		learning:    learningH,
		dashboard:   dashH,
		vision:      visionH,
		authSession: authSessH,
		timeline:    timelineH,
		autolog:     autologH,
		postToolUse: postToolUseH,
		reconcile:   reconcileH,
	}
}

// wireScheduler constructs the scheduler and registers every job (pruners,
// behavior governance, atom consolidation/bridge, cognitive jobs). Daily
// TTL-prune jobs share one registry loop (scheduler.WithPruner); adding a new
// observability table's retention job is a single PrunerSpec value, not a new
// interface/method/exec block. Retention and hour/minute values below are
// unchanged from the pre-refactor per-table With*Pruner constants. The
// returned scheduler is not started — the caller runs Start()/defer Stop().
func wireScheduler(
	stores storage.ServerStores,
	aiw aiWiring,
	discordClient *discord.Client,
	notionClient *notion.Client,
	briefingStores notion.BriefingStores,
	snapStore snapshot.StoreIface,
	snapGen snapshot.GeneratorIface,
	pruner *decay.Pruner,
	candidateStore completioncandidate.Store,
	mergedPRsStore mergedprs.Store,
) (*scheduler.Scheduler, error) {
	sched, err := scheduler.New(
		stores.Learning(), discordClient, notionClient, briefingStores, aiw.conceptReviewer,
		stores.GTD(), stores.Decision(), stores.Proposal(), aiw.reflector,
		snapStore, snapGen, stores.WorkspaceID(), pruner, stores.Playbook(),
		stores.PgxPool(),   // nil under SQLite → daily-discipline-prune skipped gracefully
		stores.Knowledge(), // nil-safe: knowledge consolidation skipped when reflector absent
	)
	if err != nil {
		return nil, fmt.Errorf("creating scheduler: %w", err)
	}
	if candidateStore != nil {
		// 30-day TTL, 23:00 Asia/Taipei (backend-security-design.md §1.3).
		if err := sched.WithPruner(scheduler.PrunerSpec{
			Name:      "candidate",
			Store:     scheduler.NewCandidatePrunerAdapter(candidateStore),
			Retention: 30 * 24 * time.Hour,
			Hour:      23,
			Minute:    0,
		}); err != nil {
			return nil, fmt.Errorf("wiring candidate pruner: %w", err)
		}
	}
	if mergedPRsStore != nil {
		// 30-day TTL, 03:00 Asia/Taipei — keeps it inside the existing 03:00
		// pending_proposals cluster instead of growing the 23:00 cluster,
		// avoiding multiple concurrent DELETEs against large tables.
		if err := sched.WithPruner(scheduler.PrunerSpec{
			Name:      "merged_prs_observed",
			Store:     scheduler.NewMergedPRsPrunerAdapter(mergedPRsStore),
			Retention: 30 * 24 * time.Hour,
			Hour:      3,
			Minute:    0,
		}); err != nil {
			return nil, fmt.Errorf("wiring merged_prs_observed pruner: %w", err)
		}
	}
	// Wire outcome pruner — Postgres only (outcomes table requires pgx pool;
	// SQLite local dev has no growth concern requiring scheduled TTL).
	// 90-day TTL, 03:30 — avoids overlap with the 03:00 pending_proposals +
	// merged_prs cluster.
	if stores.PgxPool() != nil {
		if err := sched.WithPruner(scheduler.PrunerSpec{
			Name:      "outcome",
			Store:     stores.Outcome(),
			Retention: 90 * 24 * time.Hour,
			Hour:      3,
			Minute:    30,
		}); err != nil {
			return nil, fmt.Errorf("wiring outcome pruner: %w", err)
		}
	}
	// Wire reflection pruner (both backends; reflections accumulate on weekly
	// Saturday cron + per-cycle generate_reflection; 180-day TTL per §1.3).
	// 03:45 avoids overlap with the 03:00/03:30 pending_proposals/outcome cluster.
	if stores.Reflection() != nil {
		if err := sched.WithPruner(scheduler.PrunerSpec{
			Name:      "reflection",
			Store:     stores.Reflection(),
			Retention: 180 * 24 * time.Hour,
			Hour:      3,
			Minute:    45,
		}); err != nil {
			return nil, fmt.Errorf("wiring reflection pruner: %w", err)
		}
	}
	// Wire behavior rule pruner (both backends; 365-day TTL for rejected/deprecated
	// rows per backend-security-design.md §1.3; active/proposed rows never auto-pruned).
	// 04:00 is distinct from the 03:00/03:30/03:45 cluster to avoid gocron
	// singleton-mode reschedule interference under slow DB conditions.
	if stores.BehaviorRule() != nil {
		if err := sched.WithPruner(scheduler.PrunerSpec{
			Name:      "behavior_rule",
			Store:     stores.BehaviorRule(),
			Retention: 365 * 24 * time.Hour,
			Hour:      4,
			Minute:    0,
		}); err != nil {
			return nil, fmt.Errorf("wiring behavior rule pruner: %w", err)
		}
	}
	// Wire work_session_evidence pruner (both backends; 90-day TTL per
	// backend-security-design.md §1.3, wbt-2.0 P2.4). 04:20 avoids overlap
	// with the 03:00-04:15 prune cluster (outcome 03:30, reflection 03:45,
	// discipline-event-m8 03:50, behavior-rule 04:00, ai-cost-ledger 04:15)
	// and the 04:15/04:30 weekly/daily atom jobs.
	if stores.WorkSession() != nil {
		if err := sched.WithPruner(scheduler.PrunerSpec{
			Name:      "work_session_evidence",
			Store:     stores.WorkSession(),
			Retention: 90 * 24 * time.Hour,
			Hour:      4,
			Minute:    20,
		}); err != nil {
			return nil, fmt.Errorf("wiring work_session_evidence pruner: %w", err)
		}
	}
	// Wire activity_log pruner (both backends; 365-day TTL per
	// backend-security-design.md §1.3). 04:35 avoids the 03:00-04:30 prune
	// cluster (pending-proposals/merged-prs 03:00, outcome 03:30, reflection
	// 03:45, discipline-event-m8 03:50, behavior-rule 04:00, ai-cost-ledger
	// 04:15, work-session-evidence 04:20, atom-consolidation 04:30).
	if stores.GTD() != nil {
		if err := sched.WithPruner(scheduler.PrunerSpec{
			Name:      "activity_log",
			Store:     stores.GTD(),
			Retention: 365 * 24 * time.Hour,
			Hour:      4,
			Minute:    35,
		}); err != nil {
			return nil, fmt.Errorf("wiring activity_log pruner: %w", err)
		}
	}
	// Wire project_status_snapshots pruner (Postgres only — snapStore is nil
	// under SQLite or when CLAUDE_API_KEY is unset; see buildSnapshotDeps).
	// 180-day TTL per backend-security-design.md §1.3.
	if snapStore != nil {
		if err := sched.WithPruner(scheduler.PrunerSpec{
			Name:      "project_status_snapshot",
			Store:     snapStore,
			Retention: 180 * 24 * time.Hour,
			Hour:      4,
			Minute:    40,
		}); err != nil {
			return nil, fmt.Errorf("wiring project_status_snapshots pruner: %w", err)
		}
	}
	// Wire session_handoffs pruner (both backends; 365-day TTL for resolved
	// handoffs per backend-security-design.md §1.3; open handoffs never pruned).
	if stores.Session() != nil {
		if err := sched.WithPruner(scheduler.PrunerSpec{
			Name:      "session_handoff",
			Store:     stores.Session(),
			Retention: 365 * 24 * time.Hour,
			Hour:      4,
			Minute:    45,
		}); err != nil {
			return nil, fmt.Errorf("wiring session_handoffs pruner: %w", err)
		}
	}
	// Wire behavior governance weekly job (Wednesday 04:15 Asia/Taipei). Applies
	// outcome→rule confidence updates and auto-deprecates stale low-confidence rules.
	// Startup-fatal if it errors (registering a gocron job should not fail in practice).
	if err := sched.WithBehaviorGovernance(stores.Outcome(), stores.BehaviorRule(), stores.WorkspaceID()); err != nil {
		return nil, fmt.Errorf("wiring behavior governance: %w", err)
	}
	// Wire Memory-9 atom consolidation (daily 04:30 Asia/Taipei). Skipped when
	// CLAUDE_API_KEY is absent (atomizer nil) or atom store unavailable. Both
	// conditions are nil-safe inside WithAtomConsolidator.
	if atomizer := ai.NewAtomizer(); atomizer != nil {
		atomizer.WithCostRecorder(aiw.costRecorder, stores.WorkspaceID())
		if err := sched.WithAtomConsolidator(scheduler.NewAtomConsolidDeps(stores.Atom(), atomizer, stores.WorkspaceID())); err != nil {
			return nil, fmt.Errorf("wiring atom consolidator: %w", err)
		}
	}
	// Wire atom-bridge weekly job (Saturday 04:15 Asia/Taipei). Promotes
	// consolidated atoms into pending knowledge proposals. Skipped when atom
	// or proposal store is unavailable; nil-safe inside WithAtomBridge.
	if stores.Atom() != nil && stores.Proposal() != nil {
		if err := sched.WithAtomBridge(scheduler.NewAtomBridgeDeps(stores.Atom(), stores.Proposal(), stores.WorkspaceID())); err != nil {
			return nil, fmt.Errorf("wiring atom bridge: %w", err)
		}
	}
	// Wire Memory-7 cognitive jobs. All 6 jobs are nil-safe: if the pool or
	// stores are absent (SQLite dev path, missing CLAUDE_API_KEY, etc.) each
	// job logs an info-level skip and returns without error.
	if err := sched.WithCognitiveDeps(scheduler.NewCognitiveDeps(
		stores.Reflection(),
		stores.GTD(),
		stores.Proposal(),
		stores.WorkspaceID(),
	)); err != nil {
		return nil, fmt.Errorf("wiring cognitive deps: %w", err)
	}
	// Wire discipline_events_m8 pruner — both backends, 90-day TTL per §1.3.
	// 03:50 avoids overlap with the 03:00 pending_proposals cluster and the
	// 03:30 outcome pruner slot.
	if des := stores.DisciplineEventStore(); des != nil {
		if err := sched.WithPruner(scheduler.PrunerSpec{
			Name:      "discipline_event_m8",
			Store:     des,
			Retention: 90 * 24 * time.Hour,
			Hour:      3,
			Minute:    50,
		}); err != nil {
			return nil, fmt.Errorf("wiring discipline event m8 pruner: %w", err)
		}
	}
	// Wire ai_cost_ledger pruner — PG-only, 30-day TTL per §1.3. 04:15 avoids
	// overlap with the 03:00-04:00 prune cluster.
	if pool := stores.PgxPool(); pool != nil {
		if err := sched.WithPruner(scheduler.PrunerSpec{
			Name:      "ai_cost_ledger",
			Store:     scheduler.NewAICostLedgerPrunerAdapter(pool),
			Retention: 30 * 24 * time.Hour,
			Hour:      4,
			Minute:    15,
		}); err != nil {
			return nil, fmt.Errorf("wiring ai_cost_ledger pruner: %w", err)
		}
	}
	return sched, nil
}

// buildActivityStore returns a handler.dashboardActivityStore for the active
// backend by type-asserting the GTD store. Both *gtd.Store and
// *sqlite.GTDStore implement the three new activity methods; if neither
// assertion succeeds the function returns nil (dashboard falls back to
// omitting last_updated_at / automation-feed).
func buildActivityStore(stores storage.ServerStores) handler.DashboardActivityStoreIface {
	if s := stores.PgGTD(); s != nil {
		return s
	}
	if s := stores.SqliteGTD(); s != nil {
		return s
	}
	return nil
}

// buildPruner constructs a decay.Pruner for the given backend. The
// knowledge/learning decay.PrunerStore assertions live on the ServerStores
// bundle itself (KnowledgePruner / LearningPruner in
// internal/storage/server_stores.go) — buildPruner just reads the two
// accessors, it never type-asserts a backend-specific store.
// When neither is available (unexpected backend), pruner is nil and the
// daily prune job is skipped gracefully.
func buildPruner(stores storage.ServerStores) *decay.Pruner {
	kp := stores.KnowledgePruner()
	cp := stores.LearningPruner()
	// atoms is always available from either backend.
	ap := stores.Atom()
	if kp == nil && cp == nil && ap == nil {
		return nil
	}
	return decay.NewPruner(kp, cp, ap)
}
