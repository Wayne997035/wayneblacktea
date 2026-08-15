package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/aicost"
	"github.com/Wayne997035/wayneblacktea/internal/arch"
	"github.com/Wayne997035/wayneblacktea/internal/atom"
	"github.com/Wayne997035/wayneblacktea/internal/behaviorrule"
	"github.com/Wayne997035/wayneblacktea/internal/buildinfo"
	"github.com/Wayne997035/wayneblacktea/internal/contextpack"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/discipline"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/mergedprs"
	"github.com/Wayne997035/wayneblacktea/internal/notion"
	"github.com/Wayne997035/wayneblacktea/internal/outcome"
	"github.com/Wayne997035/wayneblacktea/internal/playbook"
	"github.com/Wayne997035/wayneblacktea/internal/procedural"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/reflection"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/Wayne997035/wayneblacktea/internal/skill"
	"github.com/Wayne997035/wayneblacktea/internal/snapshot"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/Wayne997035/wayneblacktea/internal/vision"
	"github.com/Wayne997035/wayneblacktea/internal/watchdog"
	"github.com/Wayne997035/wayneblacktea/internal/worksession"
	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Server wires all domain stores to MCP tools.
//
// Domain stores are held as backend-agnostic StoreIface values so the same
// Server runs against either Postgres or SQLite. The pgxpool.Pool plus the
// concrete pg* fields are populated only on the Postgres bundle and used
// exclusively by acceptProposal (the only flow that needs a pgx-typed
// transaction across multiple stores). On SQLite they are nil and the flow
// falls back to a sequential best-effort path.
type Server struct {
	pool         *pgxpool.Pool
	gtd          gtd.StoreIface
	workspace    workspace.StoreIface
	decision     decision.StoreIface
	session      session.StoreIface
	knowledge    knowledge.StoreIface
	learning     learning.StoreIface
	proposal     proposal.StoreIface
	arch         arch.StoreIface
	workSession  worksession.StoreIface
	vision       vision.StoreIface
	playbook     playbook.StoreIface
	procedural   procedural.StoreIface
	atom         atom.StoreIface
	outcome      outcome.StoreIface
	skill        skill.StoreIface
	reflection   reflection.StoreIface
	behaviorRule behaviorrule.StoreIface
	// contextAssembler backs the assemble_context tool (tools_contextpack.go).
	// Wired from the same store bundle as the fields above — see New().
	contextAssembler *contextpack.Assembler
	atomizer         *ai.Atomizer
	// atomizeSem limits concurrent background atomize goroutines to prevent
	// API budget exhaustion from rapid add_* bursts. (security M4)
	atomizeSem chan struct{}
	// autologSem caps concurrent background autoLog goroutines to 50 to
	// prevent goroutine accumulation under burst MCP traffic.
	autologSem chan struct{}
	// atomizeFn is the function called inside the semaphore-guarded goroutine.
	// nil means use atomizeAndPersist (the production default). Tests replace
	// this with a lightweight fake to verify semaphore enforcement without
	// hitting real LLM/DB dependencies.
	atomizeFn func(
		ctx context.Context, atomizer *ai.Atomizer, store atom.StoreIface,
		wsID *uuid.UUID, parentTable string, parentID uuid.UUID, text string,
	)

	// pg* are concrete pg-backed Stores (or nil under SQLite) used by
	// acceptProposal to call WithTx(tx). Add new tx-typed code paths
	// reluctantly — see ServerStores doc comment for the migration plan.
	pgGTD      *gtd.Store
	pgProposal *proposal.Store
	pgLearning *learning.Store
	pgDecision *decision.Store

	// sqlite* are concrete SQLite-backed Stores (or nil under Postgres) used
	// by acceptProposalSQLite to run the materialise + resolve sequence inside
	// a single *sql.Tx for atomicity.
	sqliteGTD      *wbtsqlite.GTDStore
	sqliteProposal *wbtsqlite.ProposalStore
	sqliteLearning *wbtsqlite.LearningStore
	sqliteDecision *wbtsqlite.DecisionStore

	notion     *notion.Client
	watchdog   *watchdog.Watchdog
	classifier *ai.ActivityClassifier

	// disciplineEventStore persists watchdog meta-cognition findings
	// (Memory-8). nil when the store is not wired (e.g. in unit tests that
	// do not exercise watchdog tools).
	disciplineEventStore watchdog.DisciplineEventStoreIface

	// snapshotStore / snapshotGen are optional; nil = feature disabled.
	// Populated via WithSnapshot when CLAUDE_API_KEY is set.
	snapshotStore snapshot.StoreIface
	snapshotGen   snapshot.GeneratorIface

	// discipline records every MCP tool call into discipline_events for the
	// system_health drift-detection signal. Always non-nil under both
	// backends — wired in at New() time.
	discipline discipline.Store

	// sessionID is the per-process identifier written into every
	// discipline_events row. We have no real "MCP session" abstraction so we
	// derive it from PID + start time; this is sufficient for the 15-minute
	// drift window and survives a restart cleanly (the new ID won't conflate
	// with the old session's mutating calls).
	sessionID string

	// workspaceID is populated from WORKSPACE_ID env at New time for use by
	// tools that need to scope snapshot writes without a pgxpool reference.
	workspaceID *uuid.UUID

	// drafter is the LLM-backed decision proposer used by the
	// decisionProposerMiddleware. nil = middleware is a no-op (no LLM
	// configured or operator opted out via WBT_DISABLE_AUTO_DECISIONS).
	drafter *ai.DecisionDrafter

	// completionCandidates is the optional completion-candidate store used by
	// dashboard automation MCP tools. nil = feature disabled.
	completionCandidates completionCandidateStore

	// mergedPRsStore persists observed merged PRs for the Phase 2 fuzzy
	// matcher (sprint feature/0519-gtd-reconcile-phase2, GTD-fix 10/12).
	// nil = persistence + fuzzy candidate emission disabled; the exact-match
	// path continues to work.
	mergedPRsStore mergedprs.Store

	// deleteTokens holds one-time confirmation tokens issued by the first
	// invocation of delete_task. Keys are uuid.UUID strings; values are
	// deletionToken records. Entries are pruned lazily on read; ungated
	// tokens expire after the TTL even if no second call arrives.
	deleteTokens sync.Map

	// reconcileTokens holds one-time confirmation tokens issued by the first
	// (preview) invocation of reconcile_merged_prs. Unlike delete_task, this
	// tool has no natural resource-id to key by, so the token ITSELF is the
	// sync.Map key. Values are reconcileConfirmation records holding the
	// EXACT match/ambiguous/no-match set computed during preview. The confirm
	// call never re-parses or re-trusts a resent payload argument — it can
	// only apply what was computed and stored here. This forecloses a
	// bait-and-switch attack: a token obtained from a small legitimate
	// preview cannot be replayed against a confirm call carrying a
	// different/larger fabricated payload. Same accepted risk model as
	// deleteTokens above (lost on process restart within the 60s window;
	// not a new risk class).
	reconcileTokens sync.Map

	// nowFn is overridable in tests so deletion-token expiry can be tested
	// deterministically without time.Sleep. Defaults to time.Now.
	nowFn func() time.Time

	// expansions tracks, per MCP session, which tool groups that session has
	// revealed via expand_tools (tools_expand.go). Bounded + TTL'd. Accessed
	// through toolExpansions(), which lazily builds the default store so a
	// hand-constructed &Server{} in a test behaves like a New()'d one; a test
	// may pre-set this field to inject a clock or a smaller cap.
	expansions     *expansionStore
	expansionsOnce sync.Once
}

// deletionToken records a pending delete_task confirmation. The token is
// generated server-side and returned to the caller in the first response;
// the caller MUST echo it back on the second call along with confirm=true.
type deletionToken struct {
	token     string
	expiresAt time.Time
}

// deleteTokenTTL is the window during which an issued deletion token remains
// valid. 60 seconds is enough for an LLM to inspect the response and call
// back, short enough that a leaked token isn't useful long.
const deleteTokenTTL = 60 * time.Second

// reconcileConfirmation records a pending reconcile_merged_prs confirmation.
// Stored keyed by the token (not by a resource-id, since a reconcile call has
// no single natural resource-id) at preview time and consumed exactly once by
// the confirm call — see reconcileTokens field doc for the anti-tamper
// rationale.
type reconcileConfirmation struct {
	matches   []gtd.Match
	ambiguous []gtd.Ambiguous
	noMatch   int
	expiresAt time.Time
}

// reconcileTokenTTL is the window during which an issued reconcile token
// remains valid. Mirrors deleteTokenTTL.
const reconcileTokenTTL = 60 * time.Second

// maxPendingReconciles caps the number of valid (non-expired) reconcile
// tokens that can be outstanding at once. Mirrors maxPendingDeletions
// (tools_gtd.go) for consistency.
const maxPendingReconciles = 256

// errMsgInvalidTaskIDUUID is the canonical error message returned to the MCP
// caller when a task_id argument cannot be parsed as a UUID. Centralised here
// to satisfy the goconst linter and to keep the message consistent across all
// tool handlers that accept a task_id parameter.
const errMsgInvalidTaskIDUUID = "invalid task_id UUID"

// errMsgInvalidProjectIDUUID is the canonical error message returned to the MCP
// caller when a project_id argument cannot be parsed as a UUID.
const errMsgInvalidProjectIDUUID = "invalid project_id UUID"

// New creates a Server backed by the given pre-built ServerStores bundle.
// The bundle is responsible for the workspace-id scoping and the underlying
// connection lifecycle; cmd/mcp/main.go MUST defer stores.Close() after this
// call returns.
func New(stores storage.ServerStores) (*Server, error) {
	wsID := stores.WorkspaceID()

	// Wire cost recorder on the MCP atomizer (mirrors cmd/server/main.go:471-472).
	// NewAtomizer() returns nil when CLAUDE_API_KEY is absent; nil-guard before
	// calling WithCostRecorder to avoid a nil-deref (do NOT chain directly).
	atomizer := ai.NewAtomizer()
	var costRecorder aicost.Recorder = aicost.NopRecorder{}
	if pool := stores.PgxPool(); pool != nil {
		costRecorder = aicost.NewPgRecorder(pool)
	}
	if atomizer != nil {
		atomizer.WithCostRecorder(costRecorder, wsID)
	}

	contextAssembler, err := contextpack.NewAssembler(
		stores.GTD(), stores.Decision(), stores.Knowledge(), stores.Atom(),
		stores.Procedural(), stores.Skill(), stores.Outcome(), stores.Reflection(),
		stores.BehaviorRule(), stores.Session(), stores.WorkSession(),
	)
	if err != nil {
		return nil, fmt.Errorf("wiring context assembler: %w", err)
	}

	return &Server{
		pool:                 stores.PgxPool(),
		gtd:                  stores.GTD(),
		workspace:            stores.Workspace(),
		decision:             stores.Decision(),
		session:              stores.Session(),
		knowledge:            stores.Knowledge(),
		learning:             stores.Learning(),
		proposal:             stores.Proposal(),
		arch:                 stores.Arch(),
		workSession:          stores.WorkSession(),
		vision:               stores.Vision(),
		playbook:             stores.Playbook(),
		procedural:           stores.Procedural(),
		atom:                 stores.Atom(),
		outcome:              stores.Outcome(),
		skill:                stores.Skill(),
		reflection:           stores.Reflection(),
		behaviorRule:         stores.BehaviorRule(),
		contextAssembler:     contextAssembler,
		atomizer:             atomizer,
		atomizeSem:           make(chan struct{}, 5),
		autologSem:           make(chan struct{}, 50),
		pgGTD:                stores.PgGTD(),
		pgProposal:           stores.PgProposal(),
		pgLearning:           stores.PgLearning(),
		pgDecision:           stores.PgDecision(),
		sqliteGTD:            stores.SqliteGTD(),
		sqliteProposal:       stores.SqliteProposal(),
		sqliteLearning:       stores.SqliteLearning(),
		sqliteDecision:       stores.SqliteDecision(),
		notion:               notion.NewClient(),
		watchdog:             watchdog.New(200),
		discipline:           stores.Discipline(),
		disciplineEventStore: stores.DisciplineEventStore(),
		sessionID:            newSessionID(),
		workspaceID:          wsID,
		nowFn:                time.Now,
	}, nil
}

// now returns the current time using the server's nowFn (overridable in tests).
func (s *Server) now() time.Time {
	if s.nowFn == nil {
		return time.Now()
	}
	return s.nowFn()
}

// newSessionID returns a per-process identifier used as session_id when
// recording discipline_events. Since the MCP server has no real session
// concept yet, we derive it from PID + start time (millis). Format keeps it
// shorter than a UUID and human-readable in logs.
func newSessionID() string {
	return fmt.Sprintf("mcp-%d-%d", os.Getpid(), time.Now().UnixMilli())
}

// WithSnapshot wires a snapshot store and generator into the server so that
// the generate_project_status MCP tool is available. Passing nil store or gen
// is valid and disables the feature (e.g. when CLAUDE_API_KEY is not set).
func (s *Server) WithSnapshot(store snapshot.StoreIface, gen snapshot.GeneratorIface) *Server {
	s.snapshotStore = store
	s.snapshotGen = gen
	return s
}

// workspaceUUID returns the workspace UUID pointer for use in snapshot writes.
func (s *Server) workspaceUUID() *uuid.UUID {
	return s.workspaceID
}

// backendKind reports which storage backend this Server is running against,
// for the wayneblacktea://system/build-info resource (resources.go). Mirrors
// the nil-check the pg*/sqlite* store field pairs already use elsewhere in
// this struct (e.g. acceptProposal's pgGTD/sqliteGTD branch) rather than
// adding a new accessor to storage.ServerStores — s.pool is already the
// signal New() derives every other pg-vs-sqlite decision from.
func (s *Server) backendKind() string {
	if s.pool != nil {
		return string(storage.BackendPostgres)
	}
	return string(storage.BackendSQLite)
}

// WithClassifier wires an ActivityClassifier into the server so that
// significant MCP tool calls are automatically classified for implicit
// decisions and follow-up tasks. Passing nil is valid and disables
// auto-classification (e.g. when CLAUDE_API_KEY is not set).
func (s *Server) WithClassifier(clf *ai.ActivityClassifier) *Server {
	s.classifier = clf
	return s
}

// WithDecisionDrafter wires the LLM-backed decision drafter used by the
// decisionProposerMiddleware. nil drafter (or a drafter with a nil chain)
// disables the proposer — the middleware itself remains registered but
// returns early without calling the LLM.
func (s *Server) WithDecisionDrafter(d *ai.DecisionDrafter) *Server {
	s.drafter = d
	return s
}

// WithCompletionCandidates wires the completion candidate store into the server so
// that the detect_completion_candidates and reconcile_dashboard MCP tools are
// available. Passing nil disables the feature (store not configured or operator opted out).
func (s *Server) WithCompletionCandidates(store completionCandidateStore) *Server {
	s.completionCandidates = store
	return s
}

// WithMergedPRsStore wires the merged_prs_observed store into the server.
// Used by the reconcile_merged_prs MCP tool to (a) persist every observed PR
// for audit + replay and (b) enable the Phase 2 fuzzy matcher. nil disables
// both behaviours; the exact-match path remains available.
func (s *Server) WithMergedPRsStore(store mergedprs.Store) *Server {
	s.mergedPRsStore = store
	return s
}

// mcpInstructions is the protocol text injected into every `initialize`
// response (server.WithInstructions below). It is on the critical context
// path: every connecting client pays for it before doing any work, so it is
// budgeted to <= mcpInstructionsMaxRunes and carries only rules that change
// behaviour. The exhaustive routing table, full trigger vocabularies and
// per-tool guidance live in mcpProtocolAppendix (tools_onboarding.go) and are
// served on demand by initial_instructions, which returns mcpProtocolFull =
// this string + that appendix. Every tool named here MUST be in
// coreToolNames and vice versa (toolgroups.go, semantic-closure invariant).
const mcpInstructions = `WAYNEBLACKTEA PERSONAL OS — CORE PROTOCOL

tools/list = core set only; hidden tools stay callable by name, and
expand_tools reveals more. Call initial_instructions once per session for the
full routing table.

## Session start
get_today_context (pulled_forward = important not-yet-due tasks = real
candidate work, not FYI) -> resolve_handoff if one is pending ->
get_project_arch; if last_commit_sha != HEAD, re-read changed files and
upsert_project_arch.

## Mandatory
- Architecture / past decisions -> list_decisions(repo_name) BEFORE answering;
  verify in code too.
- Dispatch an agent OR start Lead-direct work -> MUST update_task(task_id,
  status="in_progress") for EVERY task worked, unprompted.
- Build passes / PR merged / commit pushed -> MUST complete_task(task_id,
  artifact="<PR URL or SHA>") now.
- NEVER ask "should I update the GTD?" — just call it; a missing call is a bug.
- complete_task seeds a draft outcome (result="unknown"); upgrade via
  evaluate_outcome / record_outcome when the real result is known.
- Read 3+ internal/ files of a repo -> MUST upsert_project_arch (slug=repo,
  summary, file_map=path->purpose).
- Auto-log + Stop-hook snapshots are a safety net, NOT a replacement — still
  call the tools.
- NEVER store these rules in agent memory; they ship with the binary. Change
  by PR to internal/mcp/server.go.

## Routing
- Multi-phase plan confirmed ("好"/"go"/"開始" or any equivalent affirmative) ->
  confirm_plan (phases+decisions atomic; use instead of add_task +
  log_decision).
- One decision confirmed -> log_decision BEFORE implementation. Scope:
  architecture, API, deploy config, 3rd-party choice, scope pivot, course
  correction, rule-source (CLAUDE.md / mcpInstructions / .mcp.json).
- New follow-up -> add_task immediately (list_tasks first if it may exist).
- "未來想做"/"之後再說" or equivalent later-intent -> add_vision_item.
- "收工"/"下班" or equivalent sign-off -> set_session_handoff.
- Saved-knowledge question -> search_knowledge first.`

// MCPServer returns a configured MCP server with all tools registered.
//
// The watchdog middleware records every tool invocation in process memory so
// the system_health tool can surface "stuck" patterns (Claude updated a task
// to in_progress but never called complete_task, etc.).
// Progressive tool disclosure (tools_expand.go) is installed here as a
// tools/list filter. mcp-go applies filters only in handleListTools; tool
// CALLS bypass them entirely, so a filtered-out tool stays invocable by name —
// that asymmetry is the feature's safety net, and TestAllTools_CallableWhenHidden
// pins it. Both transports (HTTP at cmd/server/main.go and stdio via
// internal/mcprunner) go through this one constructor, so both get it —
// including buildinfo.Version below (GTD 61838147): neither transport threads
// a version value in separately, so there is exactly one place that can drift
// from the other. See buildinfo's package doc for the ldflags that populate it.
func (s *Server) MCPServer() *server.MCPServer {
	opts := []server.ServerOption{
		server.WithInstructions(mcpInstructions),
		// Title/Description are the human-readable display fields the MCP
		// spec (2025-11-25 Implementation) defines for "logging, display, and
		// debugging" — static, unlike Version below, since they describe what
		// the server IS rather than which build is running.
		server.WithTitle("wayneblacktea Personal OS"),
		server.WithDescription(
			"Personal OS / GTD MCP server — single-tenant workspace exposing GTD tasks/projects/goals, " +
				"session handoffs, decisions, and knowledge over MCP tools and resources.",
		),
		server.WithToolHandlerMiddleware(s.watchdog.Middleware()),
		server.WithToolHandlerMiddleware(s.autoLogMiddleware()),
		server.WithToolHandlerMiddleware(s.disciplineMiddleware()),
		// Auto-decision proposer: default ON, opt-out via
		// WBT_DISABLE_AUTO_DECISIONS=1. Drafts a pending_proposals row
		// after every mutating tool when no log_decision/confirm_plan
		// happened in the last 15 min. See middleware_decision_proposer.go.
		server.WithToolHandlerMiddleware(s.decisionProposerMiddleware()),
		// Declare resource and prompt capabilities (subscribe=false,
		// listChanged=false — static read-only resources/prompts only).
		server.WithResourceCapabilities(false, false),
		server.WithPromptCapabilities(false),
	}
	// Kill switch: WBT_DISABLE_PROGRESSIVE_DISCLOSURE=1 skips the filter
	// entirely, so tools/list returns every tool exactly as it did before the
	// feature. expand_tools stays registered either way.
	if progressiveDisclosureEnabled() {
		opts = append(opts, server.WithToolFilter(s.filterToolsForSession))
	}
	ms := server.NewMCPServer("wayneblacktea", buildinfo.Version, opts...)
	s.registerOnboardingTools(ms)
	s.registerExpandTools(ms)
	s.registerContextTools(ms)
	s.registerGTDTools(ms)
	s.registerDecisionTools(ms)
	s.registerSessionTools(ms)
	s.registerKnowledgeTools(ms)
	s.registerKnowledgeNavTools(ms)
	s.registerLearningTools(ms)
	s.registerPlanTools(ms)
	s.registerProposalTools(ms)
	s.registerHealthTools(ms)
	s.registerArchTools(ms)
	s.registerStatusTools(ms)
	s.registerWorkSessionTools(ms)
	s.registerVisionTools(ms)
	s.registerPlaybookTools(ms)
	s.registerProceduralTools(ms)
	s.registerReflectionTools(ms)
	s.registerAtomTools(ms)
	s.registerOutcomeTools(ms)
	s.registerSkillTools(ms)
	s.registerBehaviorRuleTools(ms)
	s.registerContextPackTools(ms)
	s.registerDashboardTools(ms)
	s.registerReconcileTools(ms)
	s.registerCloseoutTools(ms)
	s.registerWatchdogTools(ms)
	s.registerResources(ms)
	s.registerPrompts(ms)
	return ms
}

// stringArg extracts a string argument from MCP tool arguments.
func stringArg(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

// numberArg extracts a float64 argument and returns it as int32.
func numberArg(args map[string]any, key string) int32 {
	v, _ := args[key].(float64)
	return int32(v)
}

// boolArg extracts a boolean argument from MCP tool arguments. Missing or
// non-bool keys return false (so callers can rely on the default-deny default).
func boolArg(args map[string]any, key string) bool {
	v, _ := args[key].(bool)
	return v
}

// floatArg extracts a float64 argument from MCP tool arguments.
func floatArg(args map[string]any, key string) float64 {
	v, _ := args[key].(float64)
	return v
}

// requireUUIDArg extracts args[key], validates it is present and a
// well-formed UUID, and returns a ready-to-send MCP error result on failure.
// The empty-value message is always "<key> is required"; invalidMsg is used
// verbatim when the value is non-empty but fails uuid.Parse. Callers that
// need the underlying parse error embedded in the message (a handful of
// handlers do) keep their own inline uuid.Parse instead of this helper.
//
// Usage:
//
//	id, errResult := requireUUIDArg(args, "task_id", errMsgInvalidTaskIDUUID)
//	if errResult != nil {
//	    return errResult, nil
//	}
func requireUUIDArg(args map[string]any, key, invalidMsg string) (uuid.UUID, *mcp.CallToolResult) {
	raw := stringArg(args, key)
	if raw == "" {
		return uuid.UUID{}, mcp.NewToolResultError(key + " is required")
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.UUID{}, mcp.NewToolResultError(invalidMsg)
	}
	return id, nil
}

// jsonText marshals v to compact JSON and returns a tool result text.
//
// Compact, no exceptions: the consumer of every MCP tool response is an LLM,
// not a human eyeballing a terminal, so two-space indentation is pure prompt
// overhead paid on every call. This used to be jsonText's own trade-off
// (pretty-print, "worth the bytes for tools a human reads on demand") with a
// separate compactJSONText carved out for the one payload that could not
// afford it (get_today_context). That split is gone: every byte any tool
// response costs is either session-start-adjacent or repeated often enough
// that the whitespace was never worth it. Compact JSON parses identically to
// indented JSON for every real client (all of them use json.Unmarshal or
// equivalent), so nothing is lost but formatting.
func jsonText(v any) (*mcp.CallToolResult, error) {
	out, err := json.Marshal(v)
	if err != nil {
		return mcp.NewToolResultError("marshaling response"), nil
	}
	return mcp.NewToolResultText(string(out)), nil
}
