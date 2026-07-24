package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/arch"
	"github.com/Wayne997035/wayneblacktea/internal/atom"
	"github.com/Wayne997035/wayneblacktea/internal/behaviorrule"
	"github.com/Wayne997035/wayneblacktea/internal/decay"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/discipline"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/outcome"
	"github.com/Wayne997035/wayneblacktea/internal/playbook"
	"github.com/Wayne997035/wayneblacktea/internal/procedural"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/reflection"
	wbtruntime "github.com/Wayne997035/wayneblacktea/internal/runtime"
	"github.com/Wayne997035/wayneblacktea/internal/search"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/Wayne997035/wayneblacktea/internal/skill"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/Wayne997035/wayneblacktea/internal/vision"
	"github.com/Wayne997035/wayneblacktea/internal/watchdog"
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
// environment and calls NewServerStores so both cmd/server and `wbt mcp`
// (via internal/mcprunner) always use the same env variables and defaults
// without duplicating the switch.
func BuildServerStores(ctx context.Context, backend Backend) (ServerStores, error) {
	cfg := FactoryConfig{
		Backend: backend,
		AppEnv:  os.Getenv("APP_ENV"),
	}
	switch backend {
	case BackendPostgres:
		dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
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

	gtd                    *gtd.Store
	workspace              *workspace.Store
	decision               *decision.Store
	session                *session.Store
	knowledge              *knowledge.Store
	learning               *learning.Store
	proposal               *proposal.Store
	archStore              *arch.Store
	workSession            *worksession.Store
	visionStore            *vision.Store
	playbookStore          *playbook.Store
	proceduralStore        *procedural.Store
	atomStore              *atom.Store
	outcomeStore           *outcome.Store
	skillStore             *skill.Store
	disciplineStore        *discipline.PgStore
	reflectionStore        *reflection.Store
	behaviorRuleStore      *behaviorrule.Store
	disciplineEventM8Store *watchdog.PgDisciplineEventStore
}

var _ ServerStores = (*postgresServerStores)(nil)

func newPostgresServerStores(ctx context.Context, cfg FactoryConfig) (*postgresServerStores, error) {
	if cfg.PostgresDSN == "" {
		return nil, ErrMissingPostgresDSN
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
	if wsID == nil {
		// Fail-closed: in Postgres mode an unset WORKSPACE_ID writes NULL
		// workspace_id, which would silently desync from workspace-scoped reads
		// once M3 strict scoping lands (see backend-security-design.md §1 +
		// incident 2026-05-09 / 117 rows backfilled). Refuse to start so the
		// operator either sets the env explicitly or migrates to SQLite.
		pool.Close()
		return nil, fmt.Errorf(
			"WORKSPACE_ID env is required in postgres mode " +
				"(set WORKSPACE_ID in .env.local or runtime env; " +
				"to opt out of workspace scoping use STORAGE_BACKEND=sqlite)",
		)
	}
	embedClient := search.NewEmbeddingClientFromEnv()
	return &postgresServerStores{
		pool:                   pool,
		workspaceID:            wsID,
		gtd:                    gtd.NewStore(pool, wsID),
		workspace:              workspace.NewStore(pool, wsID),
		decision:               decision.NewStore(pool, wsID),
		session:                session.NewStore(pool, wsID),
		knowledge:              knowledge.NewStore(pool, embedClient, wsID),
		learning:               learning.NewStore(pool, wsID),
		proposal:               proposal.NewStore(pool, wsID),
		archStore:              arch.NewStore(pool),
		workSession:            worksession.NewStore(pool, wsID),
		visionStore:            vision.NewStore(pool, wsID),
		playbookStore:          playbook.NewStore(pool, wsID),
		proceduralStore:        procedural.New(pool, wsID),
		atomStore:              atom.New(pool, wsID),
		outcomeStore:           outcome.NewStore(pool, wsID),
		skillStore:             skill.New(pool, wsID),
		disciplineStore:        discipline.NewPgStore(pool, wsID),
		reflectionStore:        reflection.New(pool, wsID),
		behaviorRuleStore:      behaviorrule.New(pool, wsID),
		disciplineEventM8Store: watchdog.NewPgDisciplineEventStore(pool, wsID),
	}, nil
}

func (p *postgresServerStores) Close() error {
	if p == nil || p.pool == nil {
		return nil
	}
	p.pool.Close()
	return nil
}

func (p *postgresServerStores) GTD() gtd.StoreIface                   { return p.gtd }
func (p *postgresServerStores) Workspace() workspace.StoreIface       { return p.workspace }
func (p *postgresServerStores) Decision() decision.StoreIface         { return p.decision }
func (p *postgresServerStores) Session() session.StoreIface           { return p.session }
func (p *postgresServerStores) Knowledge() knowledge.StoreIface       { return p.knowledge }
func (p *postgresServerStores) Learning() learning.StoreIface         { return p.learning }
func (p *postgresServerStores) Proposal() proposal.StoreIface         { return p.proposal }
func (p *postgresServerStores) Arch() arch.StoreIface                 { return p.archStore }
func (p *postgresServerStores) WorkSession() worksession.StoreIface   { return p.workSession }
func (p *postgresServerStores) Vision() vision.StoreIface             { return p.visionStore }
func (p *postgresServerStores) Playbook() playbook.StoreIface         { return p.playbookStore }
func (p *postgresServerStores) Procedural() procedural.StoreIface     { return p.proceduralStore }
func (p *postgresServerStores) Atom() atom.StoreIface                 { return p.atomStore }
func (p *postgresServerStores) Outcome() outcome.StoreIface           { return p.outcomeStore }
func (p *postgresServerStores) Skill() skill.StoreIface               { return p.skillStore }
func (p *postgresServerStores) Discipline() discipline.Store          { return p.disciplineStore }
func (p *postgresServerStores) Reflection() reflection.StoreIface     { return p.reflectionStore }
func (p *postgresServerStores) BehaviorRule() behaviorrule.StoreIface { return p.behaviorRuleStore }
func (p *postgresServerStores) DisciplineEventStore() watchdog.DisciplineEventStoreIface {
	return p.disciplineEventM8Store
}

// KnowledgePruner / LearningPruner implement the decay.PrunerStore assertion
// once, here, so cmd/server never type-asserts a backend-specific store.
// *knowledge.Store and *learning.Store both implement decay.PrunerStore via
// SoftPruneDecayed (see internal/knowledge/store.go, internal/learning/store.go).
func (p *postgresServerStores) KnowledgePruner() decay.PrunerStore {
	if ps, ok := p.Knowledge().(decay.PrunerStore); ok {
		return ps
	}
	return nil
}

func (p *postgresServerStores) LearningPruner() decay.PrunerStore {
	if ps, ok := p.Learning().(decay.PrunerStore); ok {
		return ps
	}
	return nil
}

func (p *postgresServerStores) WorkspaceID() *uuid.UUID                  { return p.workspaceID }
func (p *postgresServerStores) PgxPool() *pgxpool.Pool                   { return p.pool }
func (p *postgresServerStores) PgGTD() *gtd.Store                        { return p.gtd }
func (p *postgresServerStores) PgProposal() *proposal.Store              { return p.proposal }
func (p *postgresServerStores) PgLearning() *learning.Store              { return p.learning }
func (p *postgresServerStores) PgDecision() *decision.Store              { return p.decision }
func (p *postgresServerStores) SqliteGTD() *wbtsqlite.GTDStore           { return nil }
func (p *postgresServerStores) SqliteProposal() *wbtsqlite.ProposalStore { return nil }
func (p *postgresServerStores) SqliteLearning() *wbtsqlite.LearningStore { return nil }
func (p *postgresServerStores) SqliteDecision() *wbtsqlite.DecisionStore { return nil }
func (p *postgresServerStores) SqliteDB() *wbtsqlite.DB                  { return nil }

// buildPgxPoolConfig parses the DSN and applies our TLS / pgvector wiring plus
// the personal-OS pool caps. It opens no connection, so the cap policy is
// unit-testable without a live Postgres server (see factory_test.go).
func buildPgxPoolConfig(dsn, appEnv, pgsslrootcert string) (*pgxpool.Config, error) {
	// pgxpool.ParseConfig honours libpq-style PGSSLROOTCERT env var and
	// unconditionally calls os.ReadFile on it. When the env holds inline PEM
	// content (cloud deploys without file mounting), this fails before
	// BuildTLSConfig is reached. Shadow the env var for the parse — the
	// inline PEM is already captured in pgsslrootcert.
	if strings.HasPrefix(strings.TrimSpace(pgsslrootcert), pemCertPrefix) {
		if orig, present := os.LookupEnv("PGSSLROOTCERT"); present {
			if err := os.Unsetenv("PGSSLROOTCERT"); err == nil {
				defer func() { _ = os.Setenv("PGSSLROOTCERT", orig) }()
			}
		}
	}
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
	// Personal-OS scale: cap the pool so a single server (× Railway redeploys +
	// short-lived hook processes + DBeaver) cannot exhaust Aiven's connection
	// limit. pgx defaults MaxConns to max(4, NumCPU) — 8-16 on a multi-core host,
	// far too many for one tenant. Respect explicit pool_* in the DSN; otherwise
	// apply conservative caps and let idle connections cycle back to the server.
	// Guards match the param name with a trailing "=" so a longer param that
	// shares the prefix (e.g. pool_max_conn_lifetime_jitter) does not suppress
	// our cap.
	if !strings.Contains(dsn, "pool_max_conns=") {
		pgcfg.MaxConns = 4
	}
	if !strings.Contains(dsn, "pool_min_conns=") {
		pgcfg.MinConns = 0
	}
	if !strings.Contains(dsn, "pool_max_conn_idle_time=") {
		pgcfg.MaxConnIdleTime = 5 * time.Minute
	}
	if !strings.Contains(dsn, "pool_max_conn_lifetime=") {
		pgcfg.MaxConnLifetime = 30 * time.Minute
	}
	return pgcfg, nil
}

// buildPgxPool centralises the pgxpool config we use across cmd/server and
// cmd/mcp so the TLS / pgvector wiring lives in one place.
func buildPgxPool(ctx context.Context, dsn, appEnv, pgsslrootcert string) (*pgxpool.Pool, error) {
	pgcfg, err := buildPgxPoolConfig(dsn, appEnv, pgsslrootcert)
	if err != nil {
		return nil, err
	}
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

	gtd                    *wbtsqlite.GTDStore
	workspace              *wbtsqlite.WorkspaceStore
	decision               *wbtsqlite.DecisionStore
	session                *wbtsqlite.SessionStore
	knowledge              *wbtsqlite.KnowledgeStore
	learning               *wbtsqlite.LearningStore
	proposal               *wbtsqlite.ProposalStore
	archStore              *wbtsqlite.ArchStore
	workSession            *wbtsqlite.WorkSessionStore
	visionStore            *wbtsqlite.VisionStore
	playbookStore          *wbtsqlite.PlaybookStore
	proceduralStore        *wbtsqlite.ProceduralStore
	atomStore              *wbtsqlite.AtomStore
	outcomeStore           *wbtsqlite.OutcomeStore
	skillStore             *wbtsqlite.SkillStore
	disciplineStore        *wbtsqlite.DisciplineStore
	reflectionStore        *wbtsqlite.ReflectionStore
	behaviorRuleStore      *wbtsqlite.BehaviorRuleStore
	disciplineEventM8Store *wbtsqlite.DisciplineEventM8Store
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
		db:                     sdb,
		workspaceID:            wsID,
		gtd:                    wbtsqlite.NewGTDStore(sdb),
		workspace:              wbtsqlite.NewWorkspaceStore(sdb),
		decision:               wbtsqlite.NewDecisionStore(sdb),
		session:                wbtsqlite.NewSessionStore(sdb),
		knowledge:              wbtsqlite.NewKnowledgeStore(sdb),
		learning:               wbtsqlite.NewLearningStore(sdb),
		proposal:               wbtsqlite.NewProposalStore(sdb),
		archStore:              wbtsqlite.NewArchStore(sdb),
		workSession:            wbtsqlite.NewWorkSessionStore(sdb),
		visionStore:            wbtsqlite.NewVisionStore(sdb),
		playbookStore:          wbtsqlite.NewPlaybookStore(sdb),
		proceduralStore:        wbtsqlite.NewProceduralStore(sdb),
		atomStore:              wbtsqlite.NewAtomStore(sdb),
		outcomeStore:           wbtsqlite.NewOutcomeStore(sdb),
		skillStore:             wbtsqlite.NewSkillStore(sdb),
		disciplineStore:        wbtsqlite.NewDisciplineStore(sdb),
		reflectionStore:        wbtsqlite.NewReflectionStore(sdb),
		behaviorRuleStore:      wbtsqlite.NewBehaviorRuleStore(sdb),
		disciplineEventM8Store: wbtsqlite.NewDisciplineEventM8Store(sdb),
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

func (s *sqliteServerStores) GTD() gtd.StoreIface                   { return s.gtd }
func (s *sqliteServerStores) Workspace() workspace.StoreIface       { return s.workspace }
func (s *sqliteServerStores) Decision() decision.StoreIface         { return s.decision }
func (s *sqliteServerStores) Session() session.StoreIface           { return s.session }
func (s *sqliteServerStores) Knowledge() knowledge.StoreIface       { return s.knowledge }
func (s *sqliteServerStores) Learning() learning.StoreIface         { return s.learning }
func (s *sqliteServerStores) Proposal() proposal.StoreIface         { return s.proposal }
func (s *sqliteServerStores) Arch() arch.StoreIface                 { return s.archStore }
func (s *sqliteServerStores) WorkSession() worksession.StoreIface   { return s.workSession }
func (s *sqliteServerStores) Vision() vision.StoreIface             { return s.visionStore }
func (s *sqliteServerStores) Playbook() playbook.StoreIface         { return s.playbookStore }
func (s *sqliteServerStores) Procedural() procedural.StoreIface     { return s.proceduralStore }
func (s *sqliteServerStores) Atom() atom.StoreIface                 { return s.atomStore }
func (s *sqliteServerStores) Outcome() outcome.StoreIface           { return s.outcomeStore }
func (s *sqliteServerStores) Skill() skill.StoreIface               { return s.skillStore }
func (s *sqliteServerStores) Discipline() discipline.Store          { return s.disciplineStore }
func (s *sqliteServerStores) Reflection() reflection.StoreIface     { return s.reflectionStore }
func (s *sqliteServerStores) BehaviorRule() behaviorrule.StoreIface { return s.behaviorRuleStore }
func (s *sqliteServerStores) DisciplineEventStore() watchdog.DisciplineEventStoreIface {
	return s.disciplineEventM8Store
}

// KnowledgePruner / LearningPruner: see the postgresServerStores doc comment
// above — same assertion, SQLite-backed. *wbtsqlite.KnowledgeStore and
// *wbtsqlite.LearningStore implement decay.PrunerStore via SoftPruneDecayed.
func (s *sqliteServerStores) KnowledgePruner() decay.PrunerStore {
	if ps, ok := s.Knowledge().(decay.PrunerStore); ok {
		return ps
	}
	return nil
}

func (s *sqliteServerStores) LearningPruner() decay.PrunerStore {
	if ps, ok := s.Learning().(decay.PrunerStore); ok {
		return ps
	}
	return nil
}

func (s *sqliteServerStores) WorkspaceID() *uuid.UUID                  { return s.workspaceID }
func (s *sqliteServerStores) PgxPool() *pgxpool.Pool                   { return nil }
func (s *sqliteServerStores) PgGTD() *gtd.Store                        { return nil }
func (s *sqliteServerStores) PgProposal() *proposal.Store              { return nil }
func (s *sqliteServerStores) PgLearning() *learning.Store              { return nil }
func (s *sqliteServerStores) PgDecision() *decision.Store              { return nil }
func (s *sqliteServerStores) SqliteGTD() *wbtsqlite.GTDStore           { return s.gtd }
func (s *sqliteServerStores) SqliteProposal() *wbtsqlite.ProposalStore { return s.proposal }
func (s *sqliteServerStores) SqliteLearning() *wbtsqlite.LearningStore { return s.learning }
func (s *sqliteServerStores) SqliteDecision() *wbtsqlite.DecisionStore { return s.decision }
func (s *sqliteServerStores) SqliteDB() *wbtsqlite.DB                  { return s.db }
