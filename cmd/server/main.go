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
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decay"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/discord"
	"github.com/Wayne997035/wayneblacktea/internal/discordbot"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/llm"
	mcpsrv "github.com/Wayne997035/wayneblacktea/internal/mcp"
	"github.com/Wayne997035/wayneblacktea/internal/mergedprs"
	apimw "github.com/Wayne997035/wayneblacktea/internal/middleware"
	"github.com/Wayne997035/wayneblacktea/internal/notion"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/scheduler"
	"github.com/Wayne997035/wayneblacktea/internal/snapshot"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
	"github.com/Wayne997035/wayneblacktea/internal/timeline"
	"github.com/google/uuid"
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

	// Build snapshot deps early so both ctxH and scheduler can share the same store.
	snapStore, snapGen := buildSnapshotDeps(stores)

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
	if cs := buildCandidateStore(stores); cs != nil {
		dashH.SetCandidateStore(cs)
	}
	dashH.SetHandoffStore(stores.Session())
	if as := buildActivityStore(stores); as != nil {
		dashH.SetActivityStore(as)
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
	// LLM provider chain (Phase 1-4 of docs/openrouter-fallback.md):
	// classifier + concept reviewer go through the provider abstraction so
	// they pick up OpenRouter / Groq fallback automatically. Reflector and
	// Summarizer remain Claude-only this phase (deferred to Phase 5).
	llmChain := llm.BuildChainFromEnv()
	if llmChain.Len() == 0 {
		log.Println("llm: memory-only mode (no provider configured)")
	} else {
		log.Printf("llm: provider chain = %v", llmChain.Names())
	}
	// costRecorder: PG-only (ai_cost_ledger table lives in Postgres).
	// When SQLite is the backend the NopRecorder is used so AI calls are
	// never blocked by a missing table.
	var costRecorder aicost.Recorder = aicost.NopRecorder{}
	if pool := stores.PgxPool(); pool != nil {
		costRecorder = aicost.NewPgRecorder(pool)
	}

	var sum *ai.Summarizer
	var conceptReviewer ai.ConceptReviewerIface
	var clf *ai.ActivityClassifier
	var reflector ai.ReflectorIface
	if llmChain.Len() > 0 {
		clf = ai.NewActivityClassifierFromLLM(llmChain).
			WithCostRecorder(costRecorder, stores.WorkspaceID())
		conceptReviewer = ai.NewConceptReviewerFromLLM(llmChain).
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
	// auto-capture proposal wiring (sprint feature/gtd-enforce-server-side TASK 2)
	// Routes IsTask=true classifier verdicts through the TypeTask proposal queue
	// instead of direct task creation, so the user reviews and validator runs at
	// confirm time (SA decision 42e0b783). nil-safe: if stores.Proposal() is nil
	// (no SQLite / no PG), AutologHandler falls back to legacy direct create.
	autologH := handler.NewAutologHandlerWithClassifierAndProposal(
		stores.GTD(), stores.Session(), stores.Decision(), sum, clf, stores.Proposal(),
	)
	postToolUseH := handler.NewPostToolUseHandler(stores.GTD())
	defer postToolUseH.Stop()

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
	e.POST("/api/session", authSessH.IssueSession, sessionRL)

	api := e.Group("/api", apimw.APIKeyMiddleware(apiKey))
	mutationRL := echolog.RateLimiter(echolog.NewRateLimiterMemoryStore(30))

	api.GET("/context/today", ctxH.GetTodayContext)

	api.GET("/goals", gtdH.ListGoals)
	api.POST("/goals", gtdH.CreateGoal, mutationRL)
	api.PATCH("/goals/:id", gtdH.UpdateGoal, mutationRL)

	api.GET("/projects", gtdH.ListProjects)
	api.POST("/projects", gtdH.CreateProject, mutationRL)
	api.GET("/projects/:id", gtdH.GetProject)
	api.PATCH("/projects/:id", gtdH.UpdateProject, mutationRL)
	api.GET("/projects/:id/tasks", gtdH.ListProjectTasks)

	api.GET("/tasks", gtdH.ListTasks)
	api.POST("/tasks", gtdH.CreateTask, mutationRL)
	api.PATCH("/tasks/:id", gtdH.UpdateTask, mutationRL)
	api.PATCH("/tasks/:id/complete", gtdH.CompleteTask, mutationRL)

	// reconcile handler wiring (sprint feature/gtd-enforce-server-side GTD-fix 9/12)
	// Closes the "PR merged but task still pending" gap: Claude posts a list of
	// recently-merged PRs and the server auto-applies the matching tasks.
	// candidate store may be nil under unusual configs; the handler tolerates nil.
	reconcileH := handler.NewReconcileHandler(stores.GTD(), buildCandidateStore(stores)).
		WithMergedPRsStore(buildMergedPRsStore(stores))
	// Rate limit shared with other mutation endpoints (30 req/min) — reconcile
	// is bursty (post-merge sweep) but capped by 200-entry payload limit anyway.
	api.POST("/tasks/reconcile-merged-prs", reconcileH.Reconcile, mutationRL)

	api.GET("/decisions", decH.ListDecisions)
	api.POST("/decisions", decH.LogDecision, mutationRL)

	api.GET("/workspace/repos", wsH.ListRepos)
	api.POST("/workspace/repos", wsH.UpsertRepo, mutationRL)
	api.GET("/workspace/repos/:id/overview", wsOverviewH.GetRepoOverview)
	// GET /workspace/settings is intentionally un-rate-limited: reads are cheap
	// and rate-limiting only mutations (mutationRL) is the convention for all
	// other GET endpoints in this file.
	api.GET("/workspace/settings", wsH.GetSettings)
	api.PATCH("/workspace/settings", wsH.PatchSettings, mutationRL)

	// handoffRL caps POST /auto-handoff at 5 req/min — each request may
	// spawn a Gemini embedding call; keep well below Gemini free-tier quota.
	handoffRL := echolog.RateLimiter(echolog.NewRateLimiterMemoryStore(5))

	api.GET("/vision", visionH.ListVision)
	api.POST("/vision", visionH.AddVision, mutationRL)
	api.PATCH("/vision/:id", visionH.UpdateVision, mutationRL)

	// /knowledge/search has a side-effect (bumps recall_count + last_recalled_at
	// on every hit) so a high-rate caller can permanently subvert the Ebbinghaus
	// decay prune by inflating recall_count past the threshold. Cap at 20 RPS
	// per IP — same tier as /search (security audit M-1).
	knowledgeRL := echolog.RateLimiter(echolog.NewRateLimiterMemoryStore(20))
	api.GET("/knowledge", knowledgeH.ListKnowledge)
	api.POST("/knowledge", knowledgeH.AddKnowledge, knowledgeRL)
	api.PATCH("/knowledge/:id", knowledgeH.UpdateLearningValue, knowledgeRL)
	api.GET("/knowledge/search", knowledgeH.SearchKnowledge, knowledgeRL)

	proposalRL := echolog.RateLimiter(echolog.NewRateLimiterMemoryStore(10))
	// GET /api/proposals supports ?status=pending|accepted|rejected|all (UX-5).
	api.GET("/proposals", proposalH.ListProposals, proposalRL)
	// Legacy endpoint kept for backward compat; clients can migrate to /proposals?status=pending.
	api.GET("/proposals/pending", proposalH.ListPendingProposals, proposalRL)
	// confirm-batch registered before /:id/confirm so Echo's router matches the
	// literal segment before the :id wildcard.
	api.POST("/proposals/confirm-batch", proposalH.ConfirmBatch, proposalRL)
	api.POST("/proposals/:id/confirm", proposalH.ConfirmProposal, proposalRL)

	api.GET("/search", searchH.Search, echolog.RateLimiter(echolog.NewRateLimiterMemoryStore(20)))

	dashboardRL := echolog.RateLimiter(echolog.NewRateLimiterMemoryStore(30))
	api.GET("/dashboard/stats", dashH.GetStats, dashboardRL)
	api.GET("/dashboard/recent-decisions", dashH.GetRecentDecisions, dashboardRL)
	api.GET("/dashboard/next-task", dashH.GetNextTask, dashboardRL)
	api.GET("/dashboard/upcoming-tasks", dashH.GetUpcomingTasks, dashboardRL)
	api.GET("/dashboard/upcoming", dashH.GetUpcoming, dashboardRL)
	api.GET("/dashboard/automation-health", dashH.GetAutomationHealth, dashboardRL)
	api.GET("/dashboard/automation-feed", dashH.GetAutomationFeed, dashboardRL)
	// ai-cost ledger (1.5-C): per-model token + cost aggregation, last 30d.
	api.GET("/dashboard/ai-cost", dashH.GetAICost, dashboardRL)

	timelineRL := echolog.RateLimiter(echolog.NewRateLimiterMemoryStore(10))
	api.GET("/timeline", timelineH.GetTimeline, timelineRL)

	api.GET("/learning/reviews", learningH.GetDueReviews)
	api.POST("/learning/reviews/:id/submit", learningH.SubmitReview, mutationRL)
	api.POST("/learning/concepts", learningH.CreateConcept, mutationRL)
	api.GET("/learning/suggestions", learningH.GetSuggestions)
	api.POST("/learning/from-knowledge", learningH.CreateConceptFromKnowledge, mutationRL)
	// UX-6: learning history and stats.
	api.GET("/learning/history", learningH.GetHistory)
	api.GET("/learning/stats", learningH.GetStats)

	activityRL := echolog.RateLimiter(echolog.NewRateLimiterMemoryStore(30))
	// postToolUseRL is deliberately more permissive (120 req/min) because
	// wbt-hook fires on every Claude Code tool call, including fast loops.
	postToolUseRL := echolog.RateLimiter(echolog.NewRateLimiterMemoryStore(120))
	api.POST("/activity", autologH.LogActivity, activityRL)
	api.POST("/activity/posttooluse", postToolUseH.PostToolUse, postToolUseRL)
	api.POST("/auto-handoff", autologH.AutoHandoff, handoffRL)

	// HTTP MCP transport: mount the MCP server at /mcp so Claude Code can connect via:
	//   claude mcp add --transport http wayneblacktea http://localhost:8420/mcp
	// The same stores used by HTTP handlers are reused — no separate DB connections needed.
	mcpServer, err := mcpsrv.New(stores)
	if err != nil {
		return fmt.Errorf("initializing MCP server: %w", err)
	}
	if llmChain.Len() > 0 {
		mcpServer.WithClassifier(ai.NewActivityClassifierFromLLM(llmChain))
		// Auto-decision proposer drafter shares the same chain — Haiku
		// picks up first via the env-driven priority ordering. nil chain
		// → drafter is inert and middleware short-circuits.
		mcpServer.WithDecisionDrafter(ai.NewDecisionDrafter(llmChain))
	}
	mcpServer.WithSnapshot(snapStore, snapGen)
	if cs := buildCandidateStore(stores); cs != nil {
		mcpServer.WithCompletionCandidates(cs)
	}
	if mps := buildMergedPRsStore(stores); mps != nil {
		mcpServer.WithMergedPRsStore(mps)
	}
	knowledgeH.WithAtomizer(mcpServer.LaunchAtomize)
	httpMCPHandler := mcphttp.NewStreamableHTTPServer(mcpServer.MCPServer())
	e.Any("/mcp", echo.WrapHandler(httpMCPHandler), apimw.APIKeyMiddleware(apiKey))

	distFS, err := fs.Sub(staticFiles, "web/dist")
	if err != nil {
		return fmt.Errorf("embedding static files: %w", err)
	}
	e.GET("/*", echo.WrapHandler(buildSPAHandler(distFS)))

	notionClient := notion.NewClient()
	briefingStores := newBriefingStores(stores)
	pruner := buildPruner(stores)

	sched, err := scheduler.New(
		stores.Learning(), discordClient, notionClient, briefingStores, conceptReviewer,
		stores.GTD(), stores.Decision(), stores.Proposal(), reflector,
		snapStore, snapGen, stores.WorkspaceID(), pruner, stores.Playbook(),
		stores.PgxPool(),   // nil under SQLite → daily-discipline-prune skipped gracefully
		stores.Knowledge(), // nil-safe: knowledge consolidation skipped when reflector absent
	)
	if err != nil {
		return fmt.Errorf("creating scheduler: %w", err)
	}
	if cs := buildCandidateStore(stores); cs != nil {
		if err := sched.WithCandidatePruner(cs); err != nil {
			return fmt.Errorf("wiring candidate pruner: %w", err)
		}
	}
	if mps := buildMergedPRsStore(stores); mps != nil {
		if err := sched.WithMergedPRsPruner(mps); err != nil {
			return fmt.Errorf("wiring merged_prs_observed pruner: %w", err)
		}
	}
	// Wire outcome pruner — Postgres only (outcomes table requires pgx pool;
	// SQLite local dev has no growth concern requiring scheduled TTL).
	if stores.PgxPool() != nil {
		if err := sched.WithOutcomePruner(stores.Outcome()); err != nil {
			return fmt.Errorf("wiring outcome pruner: %w", err)
		}
	}
	// Wire reflection pruner (both backends; reflections accumulate on weekly
	// Saturday cron + per-cycle generate_reflection; 180-day TTL per §1.3).
	if stores.Reflection() != nil {
		if err := sched.WithReflectionPruner(stores.Reflection()); err != nil {
			return fmt.Errorf("wiring reflection pruner: %w", err)
		}
	}
	// Wire behavior rule pruner (both backends; 365-day TTL for rejected/deprecated
	// rows per backend-security-design.md §1.3; active/proposed rows never auto-pruned).
	if stores.BehaviorRule() != nil {
		if err := sched.WithBehaviorRulePruner(stores.BehaviorRule()); err != nil {
			return fmt.Errorf("wiring behavior rule pruner: %w", err)
		}
	}
	// Wire work_session_evidence pruner (both backends; 90-day TTL per
	// backend-security-design.md §1.3, wbt-2.0 P2.4).
	if stores.WorkSession() != nil {
		if err := sched.WithWorkSessionEvidencePruner(stores.WorkSession()); err != nil {
			return fmt.Errorf("wiring work_session_evidence pruner: %w", err)
		}
	}
	// Wire activity_log pruner (both backends; 365-day TTL per
	// backend-security-design.md §1.3).
	if stores.GTD() != nil {
		if err := sched.WithActivityLogPruner(stores.GTD()); err != nil {
			return fmt.Errorf("wiring activity_log pruner: %w", err)
		}
	}
	// Wire project_status_snapshots pruner (Postgres only — snapStore is nil
	// under SQLite or when CLAUDE_API_KEY is unset; see buildSnapshotDeps).
	// 180-day TTL per backend-security-design.md §1.3.
	if snapStore != nil {
		if err := sched.WithProjectStatusSnapshotPruner(snapStore); err != nil {
			return fmt.Errorf("wiring project_status_snapshots pruner: %w", err)
		}
	}
	// Wire session_handoffs pruner (both backends; 365-day TTL for resolved
	// handoffs per backend-security-design.md §1.3; open handoffs never pruned).
	if stores.Session() != nil {
		if err := sched.WithSessionHandoffPruner(stores.Session()); err != nil {
			return fmt.Errorf("wiring session_handoffs pruner: %w", err)
		}
	}
	// Wire behavior governance weekly job (Wednesday 04:15 Asia/Taipei). Applies
	// outcome→rule confidence updates and auto-deprecates stale low-confidence rules.
	// Startup-fatal if it errors (registering a gocron job should not fail in practice).
	if err := sched.WithBehaviorGovernance(stores.Outcome(), stores.BehaviorRule(), stores.WorkspaceID()); err != nil {
		return fmt.Errorf("wiring behavior governance: %w", err)
	}
	// Wire Memory-9 atom consolidation (daily 04:30 Asia/Taipei). Skipped when
	// CLAUDE_API_KEY is absent (atomizer nil) or atom store unavailable. Both
	// conditions are nil-safe inside WithAtomConsolidator.
	if atomizer := ai.NewAtomizer(); atomizer != nil {
		atomizer.WithCostRecorder(costRecorder, stores.WorkspaceID())
		if err := sched.WithAtomConsolidator(scheduler.NewAtomConsolidDeps(stores.Atom(), atomizer, stores.WorkspaceID())); err != nil {
			return fmt.Errorf("wiring atom consolidator: %w", err)
		}
	}
	// Wire atom-bridge weekly job (Saturday 04:15 Asia/Taipei). Promotes
	// consolidated atoms into pending knowledge proposals. Skipped when atom
	// or proposal store is unavailable; nil-safe inside WithAtomBridge.
	if stores.Atom() != nil && stores.Proposal() != nil {
		if err := sched.WithAtomBridge(scheduler.NewAtomBridgeDeps(stores.Atom(), stores.Proposal(), stores.WorkspaceID())); err != nil {
			return fmt.Errorf("wiring atom bridge: %w", err)
		}
	}
	// Wire Memory-7 cognitive jobs. All 7 jobs are nil-safe: if the pool or
	// stores are absent (SQLite dev path, missing CLAUDE_API_KEY, etc.) each
	// job logs an info-level skip and returns without error.
	if err := sched.WithCognitiveDeps(scheduler.NewCognitiveDeps(
		stores.Reflection(),
		stores.GTD(),
		stores.Proposal(),
		stores.WorkspaceID(),
	)); err != nil {
		return fmt.Errorf("wiring cognitive deps: %w", err)
	}
	// Wire discipline_events_m8 pruner — both backends, 90-day TTL per §1.3.
	if des := stores.DisciplineEventStore(); des != nil {
		if err := sched.WithDisciplineEventPruner(des); err != nil {
			return fmt.Errorf("wiring discipline event m8 pruner: %w", err)
		}
	}
	// Wire ai_cost_ledger pruner — PG-only, 30-day TTL per §1.3.
	if pool := stores.PgxPool(); pool != nil {
		if err := sched.WithAICostLedgerPruner(pool); err != nil {
			return fmt.Errorf("wiring ai_cost_ledger pruner: %w", err)
		}
	}
	sched.Start()
	defer sched.Stop()

	stopBot, err := startDiscordBotIfConfigured(port, apiKey, llmChain)
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

// buildSnapshotDeps returns a snapshot store and generator when the Postgres
// backend is active and CLAUDE_API_KEY is set; otherwise both are nil (feature
// gracefully disabled).
func buildSnapshotDeps(stores storage.ServerStores) (snapshot.StoreIface, snapshot.GeneratorIface) {
	pool := stores.PgxPool()
	if pool == nil {
		return nil, nil
	}
	claudeKey := os.Getenv("CLAUDE_API_KEY")
	if claudeKey == "" {
		return nil, nil
	}
	return snapshot.NewStore(pool, stores.WorkspaceID()), snapshot.NewGenerator(claudeKey)
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

// buildCandidateStore returns a completion candidate store for the active backend,
// or nil when the bundle exposes neither a SQLite DB nor a Postgres pool.
// The store is cheap to construct (no extra connections opened) and is safe to
// call multiple times — each call returns an independent value backed by the
// same shared connection pool.
func buildCandidateStore(stores storage.ServerStores) completioncandidate.Store {
	if sqliteDB := stores.SqliteDB(); sqliteDB != nil {
		return completioncandidate.NewSQLiteStore(sqliteDB.SqlConn(), sqliteDB.WorkspaceID())
	}
	if pool := stores.PgxPool(); pool != nil {
		return completioncandidate.NewPgStore(pool, stores.WorkspaceID())
	}
	return nil
}

// buildMergedPRsStore returns a merged_prs_observed store for the active
// backend. Used by the reconcile handler + MCP tool to persist every PR
// (audit trail) and to power the Phase 2 fuzzy matcher
// (sprint feature/0519-gtd-reconcile-phase2, GTD-fix 10/12).
func buildMergedPRsStore(stores storage.ServerStores) mergedprs.Store {
	if sqliteDB := stores.SqliteDB(); sqliteDB != nil {
		return mergedprs.NewSQLiteStore(sqliteDB.SqlConn(), sqliteDB.WorkspaceID())
	}
	if pool := stores.PgxPool(); pool != nil {
		return mergedprs.NewPgStore(pool, stores.WorkspaceID())
	}
	return nil
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

// buildPruner constructs a decay.Pruner for the given backend.
// Both knowledge.Store (Postgres) and sqlite.KnowledgeStore (SQLite) implement
// decay.PrunerStore via their SoftPruneDecayed method. Same for learning.Store
// and sqlite.LearningStore (concepts pruning).
// When neither interface is available (unexpected backend), pruner is nil and
// the daily prune job is skipped gracefully.
func buildPruner(stores storage.ServerStores) *decay.Pruner {
	// Type-assert to decay.PrunerStore for each table.
	// knowledge.Store and sqlite.KnowledgeStore both implement the interface.
	var kp decay.PrunerStore
	if ps, ok := stores.Knowledge().(decay.PrunerStore); ok {
		kp = ps
	}
	// learning.Store and sqlite.LearningStore both implement the interface.
	var cp decay.PrunerStore
	if ps, ok := stores.Learning().(decay.PrunerStore); ok {
		cp = ps
	}
	// atoms is always available from either backend.
	ap := stores.Atom()
	if kp == nil && cp == nil && ap == nil {
		return nil
	}
	return decay.NewPruner(kp, cp, ap)
}
