package storage

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	wbtruntime "github.com/Wayne997035/wayneblacktea/internal/runtime"
	"github.com/Wayne997035/wayneblacktea/internal/search"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	"github.com/jackc/pgx/v5/pgxpool"
	pgvectorpgx "github.com/pgvector/pgvector-go/pgx"
)

// FactoryConfig collects the inputs NewServerStores needs at startup. The
// fields are intentionally small so cmd/server and cmd/mcp can populate it
// from env (or from flags during tests) without dragging in framework state.
type FactoryConfig struct {
	// Backend selects the storage engine. Defaults to BackendPostgres when
	// the zero value is passed.
	Backend Backend
	// PostgresDSN is the libpq-style connection string for the Postgres
	// backend. Required when Backend == BackendPostgres.
	PostgresDSN string
	// SQLitePath is the file path the SQLite backend opens (e.g.
	// "./wayneblacktea.db" or ":memory:" for tests). Required when
	// Backend == BackendSQLite.
	SQLitePath string
	// PostgresInsecureTLS is set when the upstream Postgres provider uses a
	// custom CA not in the system trust store (Aiven, Railway internal).
	// When true, pgxpool runs with InsecureSkipVerify on its TLSConfig.
	PostgresInsecureTLS bool
}

// ErrMissingPostgresDSN signals NewServerStores was asked for a Postgres
// bundle without a DSN. Callers report it with a fail-fast log.Fatal.
var ErrMissingPostgresDSN = errors.New("postgres backend requires a non-empty DSN")

// ErrMissingSQLitePath signals NewServerStores was asked for a SQLite bundle
// without a file path.
var ErrMissingSQLitePath = errors.New("sqlite backend requires a non-empty file path")

// NewServerStores returns a fully wired ServerStores bundle for the requested
// backend. It is the single entry point both cmd/server and cmd/mcp call so
// they stay free of backend-specific imports.
//
// Caller MUST defer stores.Close() to release the underlying pool / DB.
func NewServerStores(ctx context.Context, cfg FactoryConfig) (ServerStores, error) {
	backend := cfg.Backend
	if backend == "" {
		backend = BackendPostgres
	}
	switch backend {
	case BackendPostgres:
		return newPostgresServerStores(ctx, cfg)
	case BackendSQLite:
		return newSQLiteServerStores(ctx, cfg)
	default:
		return nil, fmt.Errorf("%w: got %q", ErrInvalidBackend, string(backend))
	}
}

// SQLitePathFromEnv reads the SQLITE_PATH environment variable and returns it
// trimmed of surrounding whitespace. Empty input falls back to
// "./wayneblacktea.db" so the friend-grade install path "just works" when the
// user only sets STORAGE_BACKEND=sqlite.
func SQLitePathFromEnv() string {
	raw := strings.TrimSpace(os.Getenv("SQLITE_PATH"))
	if raw == "" {
		return "./wayneblacktea.db"
	}
	return raw
}

// ----- Postgres bundle -----

type postgresServerStores struct {
	pool *pgxpool.Pool

	gtd       *gtd.Store
	workspace *workspace.Store
	decision  *decision.Store
	session   *session.Store
	knowledge *knowledge.Store
	learning  *learning.Store
	proposal  *proposal.Store
}

var _ ServerStores = (*postgresServerStores)(nil)

func newPostgresServerStores(ctx context.Context, cfg FactoryConfig) (*postgresServerStores, error) {
	if cfg.PostgresDSN == "" {
		return nil, ErrMissingPostgresDSN
	}
	pool, err := buildPgxPool(ctx, cfg.PostgresDSN, cfg.PostgresInsecureTLS)
	if err != nil {
		return nil, err
	}
	wsID, err := wbtruntime.WorkspaceIDFromEnv()
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("reading WORKSPACE_ID env: %w", err)
	}
	embedClient := search.NewEmbeddingClient()
	return &postgresServerStores{
		pool:      pool,
		gtd:       gtd.NewStore(pool, wsID),
		workspace: workspace.NewStore(pool, wsID),
		decision:  decision.NewStore(pool, wsID),
		session:   session.NewStore(pool, wsID),
		knowledge: knowledge.NewStore(pool, embedClient, wsID),
		learning:  learning.NewStore(pool, wsID),
		proposal:  proposal.NewStore(pool, wsID),
	}, nil
}

func (p *postgresServerStores) Close() error {
	if p == nil || p.pool == nil {
		return nil
	}
	p.pool.Close()
	return nil
}

func (p *postgresServerStores) GTD() gtd.StoreIface             { return p.gtd }
func (p *postgresServerStores) Workspace() workspace.StoreIface { return p.workspace }
func (p *postgresServerStores) Decision() decision.StoreIface   { return p.decision }
func (p *postgresServerStores) Session() session.StoreIface     { return p.session }
func (p *postgresServerStores) Knowledge() knowledge.StoreIface { return p.knowledge }
func (p *postgresServerStores) Learning() learning.StoreIface   { return p.learning }
func (p *postgresServerStores) Proposal() proposal.StoreIface   { return p.proposal }
func (p *postgresServerStores) PgxPool() *pgxpool.Pool          { return p.pool }
func (p *postgresServerStores) PgGTD() *gtd.Store               { return p.gtd }
func (p *postgresServerStores) PgProposal() *proposal.Store     { return p.proposal }
func (p *postgresServerStores) PgLearning() *learning.Store     { return p.learning }

// buildPgxPool centralises the pgxpool config we use across cmd/server and
// cmd/mcp so the TLS / pgvector wiring lives in one place.
func buildPgxPool(ctx context.Context, dsn string, insecureTLS bool) (*pgxpool.Pool, error) {
	pgcfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parsing database URL: %w", err)
	}
	if insecureTLS {
		// Aiven / Railway-internal use a custom CA not in the system trust
		// store. The factory only sets this when the caller explicitly
		// opts in via FactoryConfig.PostgresInsecureTLS.
		pgcfg.ConnConfig.TLSConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in via config
	}
	pgcfg.AfterConnect = pgvectorpgx.RegisterTypes
	pool, err := pgxpool.NewWithConfig(ctx, pgcfg)
	if err != nil {
		return nil, fmt.Errorf("connecting to database: %w", err)
	}
	return pool, nil
}

// ----- SQLite bundle -----

type sqliteServerStores struct {
	db *wbtsqlite.DB

	gtd       *wbtsqlite.GTDStore
	workspace *wbtsqlite.WorkspaceStore
	decision  *wbtsqlite.DecisionStore
	session   *wbtsqlite.SessionStore
	knowledge *wbtsqlite.KnowledgeStore
	learning  *wbtsqlite.LearningStore
	proposal  *wbtsqlite.ProposalStore
}

var _ ServerStores = (*sqliteServerStores)(nil)

func newSQLiteServerStores(ctx context.Context, cfg FactoryConfig) (*sqliteServerStores, error) {
	if cfg.SQLitePath == "" {
		return nil, ErrMissingSQLitePath
	}
	wsID, err := wbtruntime.WorkspaceIDFromEnv()
	if err != nil {
		return nil, fmt.Errorf("reading WORKSPACE_ID env: %w", err)
	}
	wsStr := ""
	if wsID != nil {
		wsStr = wsID.String()
	}
	sdb, err := wbtsqlite.Open(ctx, cfg.SQLitePath, wsStr)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite at %q: %w", cfg.SQLitePath, err)
	}
	return &sqliteServerStores{
		db:        sdb,
		gtd:       wbtsqlite.NewGTDStore(sdb),
		workspace: wbtsqlite.NewWorkspaceStore(sdb),
		decision:  wbtsqlite.NewDecisionStore(sdb),
		session:   wbtsqlite.NewSessionStore(sdb),
		knowledge: wbtsqlite.NewKnowledgeStore(sdb),
		learning:  wbtsqlite.NewLearningStore(sdb),
		proposal:  wbtsqlite.NewProposalStore(sdb),
	}, nil
}

func (s *sqliteServerStores) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("closing sqlite: %w", err)
	}
	return nil
}

func (s *sqliteServerStores) GTD() gtd.StoreIface             { return s.gtd }
func (s *sqliteServerStores) Workspace() workspace.StoreIface { return s.workspace }
func (s *sqliteServerStores) Decision() decision.StoreIface   { return s.decision }
func (s *sqliteServerStores) Session() session.StoreIface     { return s.session }
func (s *sqliteServerStores) Knowledge() knowledge.StoreIface { return s.knowledge }
func (s *sqliteServerStores) Learning() learning.StoreIface   { return s.learning }
func (s *sqliteServerStores) Proposal() proposal.StoreIface   { return s.proposal }
func (s *sqliteServerStores) PgxPool() *pgxpool.Pool          { return nil }
func (s *sqliteServerStores) PgGTD() *gtd.Store               { return nil }
func (s *sqliteServerStores) PgProposal() *proposal.Store     { return nil }
func (s *sqliteServerStores) PgLearning() *learning.Store     { return nil }
