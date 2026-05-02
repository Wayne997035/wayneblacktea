package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/arch"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	wbtruntime "github.com/Wayne997035/wayneblacktea/internal/runtime"
	"github.com/Wayne997035/wayneblacktea/internal/search"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/Wayne997035/wayneblacktea/internal/worksession"
	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	"github.com/google/uuid"
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
	// PGSSLRootCert is the file path to a PEM-encoded CA certificate bundle
	// used to verify the Postgres server's TLS certificate. When empty and
	// AppEnv is "production", NewServerStores returns ErrMissingPGSSLROOTCERT.
	// When empty and AppEnv is not "production", the system CA pool is used.
	PGSSLRootCert string
	// AppEnv is the deployment environment (e.g. "production", "staging").
	// Used by BuildTLSConfig to enforce PGSSLROOTCERT in production.
	AppEnv string
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
	if raw == ":memory:" {
		return raw // special in-memory DSN; filepath.Clean would corrupt it
	}
	return filepath.Clean(raw)
}

// BuildServerStores is the single env-reading entry point for cmd binaries.
// It reads DATABASE_URL / SQLITE_PATH / PGSSLROOTCERT / APP_ENV from the
// environment and calls NewServerStores so both cmd/server and cmd/mcp always
// use the same env variables and defaults without duplicating the switch.
func BuildServerStores(ctx context.Context, backend Backend) (ServerStores, error) {
	cfg := FactoryConfig{
		Backend: backend,
		AppEnv:  os.Getenv("APP_ENV"),
	}
	switch backend {
	case BackendPostgres:
		dsn := os.Getenv("DATABASE_URL")
		if dsn == "" {
			return nil, fmt.Errorf("DATABASE_URL not set")
		}
		cfg.PostgresDSN = dsn
		cfg.PGSSLRootCert = os.Getenv("PGSSLROOTCERT")
	case BackendSQLite:
		cfg.SQLitePath = SQLitePathFromEnv()
	}
	stores, err := NewServerStores(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("building stores for backend %s: %w", backend, err)
	}
	return stores, nil
}

// ----- Postgres bundle -----

type postgresServerStores struct {
	pool        *pgxpool.Pool
	workspaceID *uuid.UUID

	gtd         *gtd.Store
	workspace   *workspace.Store
	decision    *decision.Store
	session     *session.Store
	knowledge   *knowledge.Store
	learning    *learning.Store
	proposal    *proposal.Store
	archStore   *arch.Store
	workSession *worksession.Store
}

var _ ServerStores = (*postgresServerStores)(nil)

func newPostgresServerStores(ctx context.Context, cfg FactoryConfig) (*postgresServerStores, error) {
	if cfg.PostgresDSN == "" {
		return nil, ErrMissingPostgresDSN
	}

	// Auto-migrate: apply pending migrations before opening the store pool.
	// Fail-fast: if migration fails, abort startup to prevent running against
	// a stale schema. Set WBT_AUTO_MIGRATE=false to disable (e.g. in CI).
	if err := RunMigrations(ctx, cfg.PostgresDSN); err != nil {
		return nil, fmt.Errorf("auto-migrate: %w", err)
	}

	pool, err := buildPgxPool(ctx, cfg.PostgresDSN, cfg.AppEnv, cfg.PGSSLRootCert)
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
		pool:        pool,
		workspaceID: wsID,
		gtd:         gtd.NewStore(pool, wsID),
		workspace:   workspace.NewStore(pool, wsID),
		decision:    decision.NewStore(pool, wsID),
		session:     session.NewStore(pool, wsID),
		knowledge:   knowledge.NewStore(pool, embedClient, wsID),
		learning:    learning.NewStore(pool, wsID),
		proposal:    proposal.NewStore(pool, wsID),
		archStore:   arch.NewStore(pool),
		workSession: worksession.NewStore(pool, wsID),
	}, nil
}

func (p *postgresServerStores) Close() error {
	if p == nil || p.pool == nil {
		return nil
	}
	p.pool.Close()
	return nil
}

func (p *postgresServerStores) GTD() gtd.StoreIface                 { return p.gtd }
func (p *postgresServerStores) Workspace() workspace.StoreIface     { return p.workspace }
func (p *postgresServerStores) Decision() decision.StoreIface       { return p.decision }
func (p *postgresServerStores) Session() session.StoreIface         { return p.session }
func (p *postgresServerStores) Knowledge() knowledge.StoreIface     { return p.knowledge }
func (p *postgresServerStores) Learning() learning.StoreIface       { return p.learning }
func (p *postgresServerStores) Proposal() proposal.StoreIface       { return p.proposal }
func (p *postgresServerStores) Arch() arch.StoreIface               { return p.archStore }
func (p *postgresServerStores) WorkSession() worksession.StoreIface { return p.workSession }
func (p *postgresServerStores) WorkspaceID() *uuid.UUID             { return p.workspaceID }
func (p *postgresServerStores) PgxPool() *pgxpool.Pool              { return p.pool }
func (p *postgresServerStores) PgGTD() *gtd.Store                   { return p.gtd }
func (p *postgresServerStores) PgProposal() *proposal.Store         { return p.proposal }
func (p *postgresServerStores) PgLearning() *learning.Store         { return p.learning }

// buildPgxPool centralises the pgxpool config we use across cmd/server and
// cmd/mcp so the TLS / pgvector wiring lives in one place.
func buildPgxPool(ctx context.Context, dsn, appEnv, pgsslrootcert string) (*pgxpool.Pool, error) {
	pgcfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parsing database URL: %w", err)
	}
	tlsCfg, err := BuildTLSConfig(appEnv, pgsslrootcert)
	if err != nil {
		return nil, fmt.Errorf("building TLS config: %w", err)
	}
	if tlsCfg != nil {
		// Merge our RootCAs into the URL-derived TLS config so ServerName
		// (set by pgx from the host parameter) is preserved. Replacing the
		// whole struct drops ServerName and Go's tls handshake then refuses.
		if pgcfg.ConnConfig.TLSConfig == nil {
			pgcfg.ConnConfig.TLSConfig = tlsCfg
		} else {
			pgcfg.ConnConfig.TLSConfig.RootCAs = tlsCfg.RootCAs
			pgcfg.ConnConfig.TLSConfig.InsecureSkipVerify = false
		}
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
	db          *wbtsqlite.DB
	workspaceID *uuid.UUID

	gtd         *wbtsqlite.GTDStore
	workspace   *wbtsqlite.WorkspaceStore
	decision    *wbtsqlite.DecisionStore
	session     *wbtsqlite.SessionStore
	knowledge   *wbtsqlite.KnowledgeStore
	learning    *wbtsqlite.LearningStore
	proposal    *wbtsqlite.ProposalStore
	archStore   *wbtsqlite.ArchStore
	workSession *wbtsqlite.WorkSessionStore
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
		db:          sdb,
		workspaceID: wsID,
		gtd:         wbtsqlite.NewGTDStore(sdb),
		workspace:   wbtsqlite.NewWorkspaceStore(sdb),
		decision:    wbtsqlite.NewDecisionStore(sdb),
		session:     wbtsqlite.NewSessionStore(sdb),
		knowledge:   wbtsqlite.NewKnowledgeStore(sdb),
		learning:    wbtsqlite.NewLearningStore(sdb),
		proposal:    wbtsqlite.NewProposalStore(sdb),
		archStore:   wbtsqlite.NewArchStore(sdb),
		workSession: wbtsqlite.NewWorkSessionStore(sdb),
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

func (s *sqliteServerStores) GTD() gtd.StoreIface                 { return s.gtd }
func (s *sqliteServerStores) Workspace() workspace.StoreIface     { return s.workspace }
func (s *sqliteServerStores) Decision() decision.StoreIface       { return s.decision }
func (s *sqliteServerStores) Session() session.StoreIface         { return s.session }
func (s *sqliteServerStores) Knowledge() knowledge.StoreIface     { return s.knowledge }
func (s *sqliteServerStores) Learning() learning.StoreIface       { return s.learning }
func (s *sqliteServerStores) Proposal() proposal.StoreIface       { return s.proposal }
func (s *sqliteServerStores) Arch() arch.StoreIface               { return s.archStore }
func (s *sqliteServerStores) WorkSession() worksession.StoreIface { return s.workSession }
func (s *sqliteServerStores) WorkspaceID() *uuid.UUID             { return s.workspaceID }
func (s *sqliteServerStores) PgxPool() *pgxpool.Pool              { return nil }
func (s *sqliteServerStores) PgGTD() *gtd.Store                   { return nil }
func (s *sqliteServerStores) PgProposal() *proposal.Store         { return nil }
func (s *sqliteServerStores) PgLearning() *learning.Store         { return nil }
