// SessionStart hook logic for `wbt context session-start` (formerly
// the standalone wbt-context binary). Emits a JSON object with a
// "systemMessage" field that Claude Code injects into the first user
// message of a new session.
//
// A5a (architecture-review-wbt-20260724-full.html item 13): this hook no
// longer hand-rolls its own PG-only retrieval/ranking/budget logic. It is a
// thin CLI adapter around contextpack.Assemble() — the same engine the
// assemble_context MCP tool uses — wired to whichever backend
// storage.BackendFor resolves (Postgres or SQLite), so both backends share
// one ranking/budget implementation instead of drifting. See
// internal/contextpack for Assemble()/scorer/budget; this file's job is only
// to build the right read ports for the resolved backend, call Assemble()
// once, and render the result (context_render.go).
//
// Exit semantics: this function returns nil unconditionally so the wbt
// dispatcher (and the wbt-context shim) always exit 0 — Claude Code MUST
// never be blocked by a hook error. All failures are logged via slog to
// a 0600 file in os.TempDir.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/atom"
	"github.com/Wayne997035/wayneblacktea/internal/behaviorrule"
	"github.com/Wayne997035/wayneblacktea/internal/contextpack"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/Wayne997035/wayneblacktea/internal/outcome"
	"github.com/Wayne997035/wayneblacktea/internal/procedural"
	"github.com/Wayne997035/wayneblacktea/internal/reflection"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/Wayne997035/wayneblacktea/internal/skill"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/Wayne997035/wayneblacktea/internal/worksession"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// dueReviewsLimit matches the pre-A5a hook's fixed cap for the "## Due
// reviews" section (concepts table has no natural ranking here — see
// backend-security-design.md-adjacent §8 notes in the A5a dispatch on why
// this stays a small, unranked CLI-adapter-only query).
const dueReviewsLimit = 10

// RunContext dispatches `wbt context <subcmd>`. args is os.Args[2:] (subcmd
// onward). Currently supports only `session-start`. Returns nil so wbt always
// exits 0 — Claude Code MUST never be blocked by a hook error.
func RunContext(args []string) error {
	InitHookSlog("wbt-context")
	if len(args) < 1 || args[0] != "session-start" {
		fmt.Fprintf(os.Stderr, "usage: wbt context session-start\n")
		// Exit 0 so an unknown subcommand never blocks Claude Code hooks.
		return nil
	}
	runSessionStart()
	return nil
}

// SessionStartOutput matches the Claude Code hook spec: a JSON object with a
// "systemMessage" string that is prepended to the first user message.
type SessionStartOutput struct {
	SystemMessage string `json:"systemMessage"`
}

// runSessionStart resolves the storage backend (dual-backend, matching every
// other wbt entry point — see storage.ResolveFromEnv doc, whose two steps
// (BackendFor + EnsureSupported) this inlines so the resolved dsn can be
// threaded through explicitly instead of round-tripping through os.Environ —
// backend-security-design.md §4.2) and dispatches to the Postgres or SQLite
// implementation. Both branches build the same 11 contextpack read ports,
// call Assemble() once, and hand the result to renderSessionContext.
// Always calls EmitContext exactly once so the hook's fail-soft, always-exit-0
// contract holds on every code path.
func runSessionStart() {
	// Preserve the historical fallback-DSN lookup (~/.wayneblacktea/.env.local)
	// used by friend-grade self-host installs that configure Postgres via a
	// config file instead of process env (hook binaries don't go through
	// godotenv.Load like cmd/server does). The resolved dsn is threaded
	// through as an explicit parameter rather than written back into the
	// process environment.
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = DSNFromFallback()
	}

	backend, err := storage.BackendFor(os.Getenv("STORAGE_BACKEND"), dsn)
	if err != nil {
		slog.Warn("wbt-context: resolving storage backend", "err", err)
		EmitContext("")
		return
	}
	if err := storage.EnsureSupported(backend); err != nil {
		slog.Warn("wbt-context: resolving storage backend", "err", err)
		EmitContext("")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wsID := WorkspaceFromEnv()

	var (
		pack       *contextpack.Pack
		handoff    *db.SessionHandoff
		dueReviews []string
	)
	if backend == storage.BackendSQLite {
		pack, handoff, dueReviews = runSessionStartSQLite(ctx, wsID)
	} else {
		pack, handoff, dueReviews = runSessionStartPostgres(ctx, wsID, dsn)
	}

	EmitContext(renderSessionContext(pack, handoff, dueReviews))
}

// runSessionStartPostgres builds a hook-tuned pgxpool (MaxConns=2, 30s
// lifetime — backend-security-design.md §5.3; NOT the generic
// storage.BuildServerStores pool config, which is sized for the
// long-running cmd/server process instead), wires the 11 contextpack read
// ports directly off it (deliberately NOT storage.NewServerStores/
// ServerStores — that pulls in stores contextpack never uses and enforces a
// startup-time WORKSPACE_ID requirement this hook has never had), assembles
// the pack, and fetches due-review titles. Returns (nil, nil, nil) on any
// fail-soft path.
func runSessionStartPostgres(ctx context.Context, wsID *uuid.UUID, dsn string) (*contextpack.Pack, *db.SessionHandoff, []string) {
	if dsn == "" {
		return nil, nil, nil
	}

	pool, err := newHookPgxPool(ctx, dsn)
	if err != nil {
		slog.Warn("wbt-context: postgres connection failed", "err", err)
		return nil, nil, nil
	}
	defer pool.Close()

	sessionStore := session.NewStore(pool, wsID)
	handoff := latestHandoffOrNil(ctx, sessionStore)

	asm, err := contextpack.NewAssembler(
		gtd.NewStore(pool, wsID),
		decision.NewStore(pool, wsID),
		// embed=nil: this hook must never call an external embedding API —
		// A5a dispatch §5 ("hook 路徑不得走網路"). knowledge.Store.SearchReadOnly
		// degrades to FTS-only when embed==nil (internal/knowledge/store.go
		// searchCore's `if s.embed == nil` branch), which is exactly the
		// desired fail-soft-offline behaviour here.
		knowledge.NewStore(pool, nil, wsID),
		atom.New(pool, wsID),
		procedural.New(pool, wsID),
		skill.New(pool, wsID),
		outcome.NewStore(pool, wsID),
		reflection.New(pool, wsID),
		behaviorrule.New(pool, wsID),
		sessionStore,
		worksession.NewStore(pool, wsID),
	)
	if err != nil {
		slog.Warn("wbt-context: building postgres assembler failed", "err", err)
		return nil, handoff, nil
	}

	pack := assembleSessionPack(ctx, asm, handoff)
	dueReviews := fetchDueReviewsPostgres(ctx, pool, wsID, dueReviewsLimit)
	return pack, handoff, dueReviews
}

// runSessionStartSQLite opens SQLITE_PATH (storage.SQLitePathFromEnv, same
// default/env resolution as every other wbt entry point) strictly
// read-only via wbtsqlite.OpenReadOnly (M-1: never migrates, never writes —
// see that function's doc comment for the full threat model), wires the
// same 11 contextpack read ports off it, assembles the pack, and fetches
// due-review titles. Returns (nil, nil, nil) on any fail-soft path,
// including a stale/dirty schema (ErrSchemaNotCurrent) or a missing file.
func runSessionStartSQLite(ctx context.Context, wsID *uuid.UUID) (*contextpack.Pack, *db.SessionHandoff, []string) {
	path := storage.SQLitePathFromEnv()
	wsStr := ""
	if wsID != nil {
		wsStr = wsID.String()
	}

	sdb, err := wbtsqlite.OpenReadOnly(ctx, path, wsStr)
	if err != nil {
		slog.Warn("wbt-context: opening sqlite read-only failed", "err", err)
		return nil, nil, nil
	}
	defer func() {
		if cerr := sdb.Close(); cerr != nil {
			slog.Warn("wbt-context: closing sqlite read-only connection", "err", cerr)
		}
	}()

	sessionStore := wbtsqlite.NewSessionStore(sdb)
	handoff := latestHandoffOrNil(ctx, sessionStore)

	asm, err := contextpack.NewAssembler(
		wbtsqlite.NewGTDStore(sdb),
		wbtsqlite.NewDecisionStore(sdb),
		wbtsqlite.NewKnowledgeStore(sdb),
		wbtsqlite.NewAtomStore(sdb),
		wbtsqlite.NewProceduralStore(sdb),
		wbtsqlite.NewSkillStore(sdb),
		wbtsqlite.NewOutcomeStore(sdb),
		wbtsqlite.NewReflectionStore(sdb),
		wbtsqlite.NewBehaviorRuleStore(sdb),
		sessionStore,
		wbtsqlite.NewWorkSessionStore(sdb),
	)
	if err != nil {
		slog.Warn("wbt-context: building sqlite assembler failed", "err", err)
		return nil, handoff, nil
	}

	pack := assembleSessionPack(ctx, asm, handoff)
	dueReviews := fetchDueReviewsSQLite(ctx, sdb, dueReviewsLimit)
	return pack, handoff, dueReviews
}

// newHookPgxPool builds a pgxpool tuned for a short-lived, high-frequency
// hook process (backend-security-design.md §5.3): MaxConns=2, MinConns=0,
// MaxConnLifetime=30s. Deliberately NOT the same sizing as
// internal/storage/factory.go's buildPgxPoolConfig, which targets the single
// long-running cmd/server process.
func newHookPgxPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parsing DSN: %w", err)
	}
	cfg.MaxConns = 2
	cfg.MinConns = 0
	cfg.MaxConnLifetime = 30 * time.Second
	tlsCfg, err := storage.BuildTLSConfig(os.Getenv("APP_ENV"), os.Getenv("PGSSLROOTCERT"))
	if err != nil {
		return nil, fmt.Errorf("TLS config: %w", err)
	}
	if tlsCfg != nil {
		cfg.ConnConfig.TLSConfig = tlsCfg
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connecting: %w", err)
	}
	return pool, nil
}

// latestHandoffOrNil fetches the latest unresolved handoff via any
// contextpack.SessionReadPort implementation (both the Postgres *session.Store
// and the SQLite *wbtsqlite.SessionStore satisfy it identically). Used both
// to build req.Objective (assembleSessionPack) and to render the "## Last
// session handoff" section directly (context_render.go) — session.ErrNotFound
// (the common "no unresolved handoff" case) is not logged as a warning.
func latestHandoffOrNil(ctx context.Context, s contextpack.SessionReadPort) *db.SessionHandoff {
	h, err := s.LatestHandoff(ctx)
	if err != nil {
		if !errors.Is(err, session.ErrNotFound) {
			slog.Warn("wbt-context: fetching latest handoff", "err", err)
		}
		return nil
	}
	return h
}

// assembleSessionPack calls Assemble() once with an Objective derived from
// the latest handoff (Intent + ContextSummary — A5a dispatch §6), returning
// nil on any Assemble error (fail-soft; Assemble() does not currently return
// a non-nil error on any path, but this stays defensive against future
// changes rather than assuming that invariant).
func assembleSessionPack(ctx context.Context, asm *contextpack.Assembler, handoff *db.SessionHandoff) *contextpack.Pack {
	pack, err := asm.Assemble(ctx, contextpack.Request{Objective: objectiveFromHandoff(handoff)})
	if err != nil {
		slog.Warn("wbt-context: Assemble failed", "err", err)
		return nil
	}
	return pack
}

// objectiveFromHandoff builds req.Objective from the latest handoff's
// Intent + ContextSummary (A5a §6). "" when there is no handoff — every
// retrieveX source in contextpack.retrieve() degrades gracefully on an empty
// query (current-task/project retrieval is unaffected by Objective at all;
// keyword-search sources simply match nothing rather than erroring).
func objectiveFromHandoff(h *db.SessionHandoff) string {
	if h == nil {
		return ""
	}
	obj := h.Intent
	if h.ContextSummary.Valid && h.ContextSummary.String != "" {
		obj += " " + h.ContextSummary.String
	}
	return obj
}

// fetchDueReviewsPostgres returns due-review concept titles, most-overdue
// first. contextpack has no concepts/review_schedule read port at all (A5a
// dispatch §8 — this is the one deliberately-permitted CLI-adapter-only
// source), so this query — a single JOIN, no ranking/budget — stays in this
// file exactly as it was pre-A5a; only the return type changed (string →
// []string) to match renderSessionContext's contract. Untruncated: caps are
// applied uniformly for both backends in renderDueReviewsSection.
func fetchDueReviewsPostgres(ctx context.Context, pool *pgxpool.Pool, wsID *uuid.UUID, limit int) []string {
	const q = `SELECT c.title FROM concepts c
		JOIN review_schedule rs ON rs.concept_id = c.id
		WHERE rs.due_date <= NOW()
		  AND c.status = 'active'
		  AND ($1::uuid IS NULL OR c.workspace_id = $1)
		ORDER BY rs.due_date ASC
		LIMIT $2`
	rows, err := pool.Query(ctx, q, uuidArg(wsID), limit)
	if err != nil {
		slog.Warn("wbt-context: postgres due reviews query failed", "err", err)
		return nil
	}
	defer rows.Close()
	var titles []string
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			continue
		}
		titles = append(titles, title)
	}
	if err := rows.Err(); err != nil {
		slog.Warn("wbt-context: postgres due reviews rows iteration failed", "err", err)
	}
	return titles
}

// fetchDueReviewsSQLite is fetchDueReviewsPostgres's structural twin (A5a
// dispatch §8): same JOIN, same predicate shape, same ordering, same limit —
// only the placeholder syntax and the NOW()-equivalent
// (strftime('%Y-%m-%dT%H:%M:%fZ','now'), matching every other "current
// timestamp" comparison in this package's SQLite migrations) differ.
// Covered by TestFetchDueReviews_PostgresSQLiteGoldenParity for byte-for-byte
// section-content parity against fetchDueReviewsPostgres.
func fetchDueReviewsSQLite(ctx context.Context, sdb *wbtsqlite.DB, limit int) []string {
	var wsArg any
	if ws := sdb.WorkspaceID(); ws != "" {
		wsArg = ws
	}
	const q = `SELECT c.title FROM concepts c
		JOIN review_schedule rs ON rs.concept_id = c.id
		WHERE rs.due_date <= strftime('%Y-%m-%dT%H:%M:%fZ','now')
		  AND c.status = 'active'
		  AND (?1 IS NULL OR c.workspace_id = ?1)
		ORDER BY rs.due_date ASC
		LIMIT ?2`
	rows, err := sdb.SqlConn().QueryContext(ctx, q, wsArg, limit)
	if err != nil {
		slog.Warn("wbt-context: sqlite due reviews query failed", "err", err)
		return nil
	}
	defer func() { _ = rows.Close() }()
	var titles []string
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			continue
		}
		titles = append(titles, title)
	}
	if err := rows.Err(); err != nil {
		slog.Warn("wbt-context: sqlite due reviews rows iteration failed", "err", err)
	}
	return titles
}

// EmitContext writes the SessionStart hook JSON envelope to stdout.
// Exported for test access.
func EmitContext(msg string) {
	out, _ := json.Marshal(SessionStartOutput{SystemMessage: msg})
	fmt.Println(string(out))
}

// uuidArg converts *uuid.UUID to an interface{} suitable for pgx placeholder.
// nil maps to nil so that `($1::uuid IS NULL OR ...)` matches all rows.
func uuidArg(id *uuid.UUID) any {
	if id == nil {
		return nil
	}
	return id
}

func truncate(s string, maxLen int) string {
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	return string(r[:maxLen]) + "…"
}
