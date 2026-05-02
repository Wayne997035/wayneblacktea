// Package storage's ServerStores bundle is the backend-agnostic surface used
// by the HTTP server (cmd/server) and the MCP server (cmd/mcp) to obtain a
// working set of domain stores without compile-time coupling to a specific
// backend (Postgres pgxpool vs. SQLite database/sql).
//
// Adding a new domain store: extend the interface here, then satisfy it in
// both internal/storage/factory.go bundles (postgresServerStores and
// sqliteServerStores). The compile-time `var _ ServerStores = ...` assertions
// at the bottom of factory.go will keep both backends honest.
package storage

import (
	"io"

	"github.com/Wayne997035/wayneblacktea/internal/arch"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/Wayne997035/wayneblacktea/internal/worksession"
	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ServerStores is the backend-agnostic store bundle that cmd/server and
// cmd/mcp consume. It exposes the domain Store interfaces plus a Close
// hook for the underlying connection (pgx pool or SQLite *sql.DB).
//
// PgxPool returns the live *pgxpool.Pool when the bundle is Postgres-backed,
// or nil when the bundle is SQLite-backed. Callers that absolutely require a
// pgx transaction (currently only the MCP proposal-acceptance flow) MUST
// guard with `if pool := stores.PgxPool(); pool != nil { ... }` and provide a
// non-tx fallback for the SQLite path.
//
// Concrete pg Store accessors (PgGTD / PgProposal / PgLearning) return the
// concrete *Store on the Postgres bundle so that the few code paths that
// need pgx-typed transactions (proposal materialisation across gtd /
// learning / proposal) can call WithTx(tx); they return nil on the SQLite
// bundle. New backend-agnostic transactional code SHOULD NOT add more such
// accessors — the longer-term direction is a TxCoordinator inside the
// storage package, not pgx leaking into MCP.
type ServerStores interface {
	io.Closer

	GTD() gtd.StoreIface
	Workspace() workspace.StoreIface
	Decision() decision.StoreIface
	Session() session.StoreIface
	Knowledge() knowledge.StoreIface
	Learning() learning.StoreIface
	Proposal() proposal.StoreIface
	Arch() arch.StoreIface
	WorkSession() worksession.StoreIface

	// WorkspaceID returns the workspace UUID configured at startup, or nil
	// when operating in legacy single-workspace mode. Used by MCP tools that
	// need to scope writes (e.g. snapshot) without a raw pgxpool reference.
	WorkspaceID() *uuid.UUID

	// PgxPool returns the underlying pgx pool when this bundle is the
	// Postgres backend, or nil for any other backend. Used only by code
	// paths that legitimately need a pgx-typed transaction.
	PgxPool() *pgxpool.Pool

	// PgGTD / PgProposal / PgLearning return concrete *Store handles only
	// when the bundle is Postgres-backed (so callers can WithTx(tx) on a
	// pgx.Tx). They return nil on the SQLite bundle. See the type doc for
	// the future migration path.
	PgGTD() *gtd.Store
	PgProposal() *proposal.Store
	PgLearning() *learning.Store
}
