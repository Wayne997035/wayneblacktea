package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/arch"
	"github.com/Wayne997035/wayneblacktea/internal/atom"
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
	"github.com/Wayne997035/wayneblacktea/internal/session"
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
	pool        *pgxpool.Pool
	gtd         gtd.StoreIface
	workspace   workspace.StoreIface
	decision    decision.StoreIface
	session     session.StoreIface
	knowledge   knowledge.StoreIface
	learning    learning.StoreIface
	proposal    proposal.StoreIface
	arch        arch.StoreIface
	workSession worksession.StoreIface
	vision      vision.StoreIface
	playbook    playbook.StoreIface
	procedural  procedural.StoreIface
	atom        atom.StoreIface
	outcome     outcome.StoreIface
	atomizer    *ai.Atomizer
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

	// nowFn is overridable in tests so deletion-token expiry can be tested
	// deterministically without time.Sleep. Defaults to time.Now.
	nowFn func() time.Time
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
	return &Server{
		pool:           stores.PgxPool(),
		gtd:            stores.GTD(),
		workspace:      stores.Workspace(),
		decision:       stores.Decision(),
		session:        stores.Session(),
		knowledge:      stores.Knowledge(),
		learning:       stores.Learning(),
		proposal:       stores.Proposal(),
		arch:           stores.Arch(),
		workSession:    stores.WorkSession(),
		vision:         stores.Vision(),
		playbook:       stores.Playbook(),
		procedural:     stores.Procedural(),
		atom:           stores.Atom(),
		outcome:        stores.Outcome(),
		atomizer:       ai.NewAtomizer(),
		atomizeSem:     make(chan struct{}, 5),
		autologSem:     make(chan struct{}, 50),
		pgGTD:          stores.PgGTD(),
		pgProposal:     stores.PgProposal(),
		pgLearning:     stores.PgLearning(),
		pgDecision:     stores.PgDecision(),
		sqliteGTD:      stores.SqliteGTD(),
		sqliteProposal: stores.SqliteProposal(),
		sqliteLearning: stores.SqliteLearning(),
		sqliteDecision: stores.SqliteDecision(),
		notion:         notion.NewClient(),
		watchdog:       watchdog.New(200),
		discipline:     stores.Discipline(),
		sessionID:      newSessionID(),
		workspaceID:    wsID,
		nowFn:          time.Now,
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

const mcpInstructions = `WAYNEBLACKTEA PERSONAL OS — USAGE PROTOCOL

## Session Start
Call get_today_context first. If there is a pending handoff, resolve_handoff after reading it.

## Architecture / past decisions
Call list_decisions with the relevant repo_name before answering. Always verify in code too.

## Tool routing

| Situation | Tool |
|-----------|------|
| Multi-phase plan confirmed ("好"/"go"/"衝"/"開始"/option pick) | confirm_plan — ALL phases + decisions atomically |
| User confirms a single decision | log_decision BEFORE implementation |
| **Start a task** (dispatch agent OR begin Lead-direct) | **update_task → in_progress immediately, no user reminder** |
| Build passes / PR merged / task done | complete_task with artifact URL |
| "收工" / "下班" / "later" / "good night" | set_session_handoff (Stop hook also fires; call this too for richer context) |
| Scope/priority changes mid-session | log_decision + update_task + set_session_handoff |
| New follow-up discovered | add_task immediately |
| Question about saved knowledge | search_knowledge first |
| "未來想做" / "之後再說" / "現在還不能" / "等 X 完成才能做" / "記一下以後" | add_vision_item immediately |
| "Before complex task" / finding relevant patterns | list_playbooks — returns procedural rules from past decisions |
| Recording a new how-to / reusable approach from this session | add_procedural — saves title, when_to_use, approach_md |
| Retrieving how-to / procedural approach | query_procedural with keywords |
| Marking a procedural memory as successfully used | mark_procedural_used with id |
| Cross-type memory search (episodic + semantic + procedural) | recall with query |

## MANDATORY GTD DISCIPLINE (enforced on every task)

Before dispatching any engineer/agent OR starting any Lead-direct implementation:
→ MUST call update_task(task_id, status="in_progress") for EVERY task being worked

When a task is done (build passes, PR merged, or Lead-direct commit pushed):
→ MUST call complete_task(task_id, artifact="<PR URL or commit SHA>") immediately

NEVER ask "should I update the GTD?" — just do it. Missing these calls = process bug.
Triggers:
- "dispatch engineer" → update_task in_progress first
- "PR merged" → complete_task with PR URL
- "commit SHA" → complete_task with SHA
- "task check passes" → complete_task if this was the acceptance criterion

## confirm_plan triggers
"可以" "好" "OK" "yes" "go" "對" "衝" "開工" "開始" "執行" "按這個" — or any single letter/number picking from a list the assistant just proposed.
After confirm_plan fires: assess each task immediately — Lead-direct tasks start now,
complex tasks dispatch engineer now. Both in parallel. **At dispatch (or Lead-direct start),
MUST call update_task → in_progress for every task being worked. No user prompt needed.**

## update_task triggers (in_progress)
Mandatory the moment work begins on a task — dispatch engineer/codex/frontend-engineer,
or Lead-direct execution starts. For multi-phase plans just created via confirm_plan,
mark every phase task being worked in parallel as in_progress. Skipping this is a process
bug; user should not have to remind. Pair with complete_task at finish.

## log_decision scope
Architecture, API design, deployment config, third-party service choice, scope pivot,
mid-session course correction, rule-source changes (CLAUDE.md / mcpInstructions / .mcp.json).

## complete_task triggers
"好了" "搞定" "done" "ship it" "looks good" "讚" "漂亮" — only when a task is currently in_progress and the assistant just reported completion.

## Note on auto-logging
High-signal tools (complete_task, confirm_plan, add_task, log_decision, set_session_handoff)
are auto-logged server-side. Stop hook auto-creates a session snapshot. These tools are still
required — auto-log is a safety net, not a replacement.

## Architecture snapshots
After reading 3+ internal/ files from a project, MUST call upsert_project_arch to store the
architecture snapshot (slug = repo name, summary = one-paragraph description, file_map = path→purpose).
At session start, call get_project_arch first — if stale (last_commit_sha differs from git rev-parse HEAD),
re-read changed files and call upsert_project_arch again.

## Behavior rules live here, not in private memory
Do not store wayneblacktea protocol rules in agent memory. Rules ship with the binary so
all MCP clients get identical behavior. To change a rule, propose a PR to internal/mcp/server.go.`

// MCPServer returns a configured MCP server with all tools registered.
//
// The watchdog middleware records every tool invocation in process memory so
// the system_health tool can surface "stuck" patterns (Claude updated a task
// to in_progress but never called complete_task, etc.).
func (s *Server) MCPServer() *server.MCPServer {
	ms := server.NewMCPServer("wayneblacktea", "0.1.0",
		server.WithInstructions(mcpInstructions),
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
	)
	s.registerOnboardingTools(ms)
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
	s.registerAtomTools(ms)
	s.registerOutcomeTools(ms)
	s.registerDashboardTools(ms)
	s.registerReconcileTools(ms)
	s.registerCloseoutTools(ms)
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

// jsonText marshals v to indented JSON and returns a tool result text.
func jsonText(v any) (*mcp.CallToolResult, error) {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultError("marshaling response"), nil
	}
	return mcp.NewToolResultText(string(out)), nil
}
