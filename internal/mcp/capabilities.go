package mcp

import (
	"os"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/completioncandidate"
	"github.com/Wayne997035/wayneblacktea/internal/llm"
	"github.com/Wayne997035/wayneblacktea/internal/mergedprs"
	"github.com/Wayne997035/wayneblacktea/internal/snapshot"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
)

// CapabilityReport records which optional MCP capabilities WireOptionalCapabilities
// actually attached to a *Server, plus a reason for anything it deliberately
// left unset. Both MCP transports log this after wiring (see
// cmd/server/main.go and internal/mcprunner/runner.go) so a capability gap is
// always an observed, explicit fact instead of a silent omission.
type CapabilityReport struct {
	Classifier           bool
	DecisionDrafter      bool
	Snapshot             bool
	SnapshotSkipReason   string
	CompletionCandidates bool
	CandidatesSkipReason string
	MergedPRsStore       bool
	MergedPRsSkipReason  string
}

// CapabilityInputs bundles the already-resolved collaborators
// WireOptionalCapabilities attaches to a *Server. Every field is optional
// (nil is valid and means "this capability stays unset"). Callers build these
// via llm.BuildChainFromEnv + ResolveSnapshotStore + ResolveCandidateStore +
// ResolveMergedPRsStore — WireOptionalCapabilities itself does no backend
// detection, it only attaches what it is given and reports what happened.
type CapabilityInputs struct {
	Chain          *llm.Chain
	SnapshotStore  snapshot.StoreIface
	SnapshotGen    snapshot.GeneratorIface
	CandidateStore completioncandidate.Store
	MergedPRsStore mergedprs.Store
}

// WireOptionalCapabilities is the single place that decides which optional
// AI/observability capabilities an MCP *Server instance gets, and attaches
// them via the existing With* setters. cmd/server/main.go (HTTP transport)
// and internal/mcprunner.Run (stdio transport) both call this immediately
// after mcp.New(stores) instead of hand-wiring With* calls independently —
// that duplication is exactly what let stdio drift out of sync with HTTP
// (missing WithDecisionDrafter / WithCompletionCandidates /
// WithMergedPRsStore; see docs/architecture.md "MCP server wiring").
//
// This function takes no transport-specific type (echo.Context, API key,
// HTTP handler, *server.MCPServer) — only CapabilityInputs, which is built
// purely from storage.ServerStores + process environment. That is what keeps
// it from becoming a service locator: it has exactly one job (decide +
// attach capabilities from an already-resolved backend descriptor) and
// callers stay fully responsible for everything transport-shaped.
func (s *Server) WireOptionalCapabilities(in CapabilityInputs) CapabilityReport {
	var report CapabilityReport

	if in.Chain != nil && in.Chain.Len() > 0 {
		s.WithClassifier(ai.NewActivityClassifierFromLLM(in.Chain))
		report.Classifier = true
		// Decision drafter shares the same chain — Haiku picks up first via
		// the env-driven priority ordering. A nil/empty chain leaves the
		// drafter unset and middleware_decision_proposer.go short-circuits
		// (isAutoDecisionProposerEnabled requires s.drafter != nil).
		s.WithDecisionDrafter(ai.NewDecisionDrafter(in.Chain))
		report.DecisionDrafter = true
	}

	if in.SnapshotStore != nil && in.SnapshotGen != nil {
		s.WithSnapshot(in.SnapshotStore, in.SnapshotGen)
		report.Snapshot = true
	} else {
		report.SnapshotSkipReason = "snapshot store/generator not resolved — see ResolveSnapshotStore " +
			"(requires CLAUDE_API_KEY + a Postgres backend; SQLite has no ANN index support for this feature)"
	}

	if in.CandidateStore != nil {
		s.WithCompletionCandidates(in.CandidateStore)
		report.CompletionCandidates = true
	} else {
		report.CandidatesSkipReason = "no completion-candidate store resolved — see ResolveCandidateStore " +
			"(expects either SqliteDB or PgxPool on storage.ServerStores)"
	}

	if in.MergedPRsStore != nil {
		s.WithMergedPRsStore(in.MergedPRsStore)
		report.MergedPRsStore = true
	} else {
		report.MergedPRsSkipReason = "no merged-PRs store resolved — see ResolveMergedPRsStore " +
			"(expects either SqliteDB or PgxPool on storage.ServerStores)"
	}

	return report
}

// ResolveSnapshotStore picks the project-status snapshot store + generator
// for the active backend, or (nil, nil) when the precondition isn't met.
// Snapshot generation is Postgres-only (the store requires a pgxpool.Pool)
// and Claude-only (Phase-5 deferred); on SQLite or without CLAUDE_API_KEY the
// feature stays unset. Exported so cmd/server/main.go can build the same
// instance for its non-MCP call sites (context handler, scheduler pruner)
// without re-deriving this precondition a second time.
func ResolveSnapshotStore(stores storage.ServerStores) (snapshot.StoreIface, snapshot.GeneratorIface) {
	claudeKey := os.Getenv("CLAUDE_API_KEY")
	if claudeKey == "" {
		return nil, nil
	}
	pool := stores.PgxPool()
	if pool == nil {
		return nil, nil
	}
	return snapshot.NewStore(pool, stores.WorkspaceID()), snapshot.NewGenerator(claudeKey)
}

// ResolveCandidateStore picks the completion-candidate store implementation
// matching the live backend inside stores, or nil if neither a Postgres pool
// nor a SQLite handle is available (should not happen in practice — every
// storage.ServerStores implementation provides exactly one). Exported so
// cmd/server/main.go can build the same store instance for its non-MCP call
// sites (dashboard handler, scheduler pruner) without re-deriving the
// backend-selection logic a second time.
func ResolveCandidateStore(stores storage.ServerStores) completioncandidate.Store {
	if sqliteDB := stores.SqliteDB(); sqliteDB != nil {
		return completioncandidate.NewSQLiteStore(sqliteDB.SqlConn(), sqliteDB.WorkspaceID())
	}
	if pool := stores.PgxPool(); pool != nil {
		return completioncandidate.NewPgStore(pool, stores.WorkspaceID())
	}
	return nil
}

// ResolveMergedPRsStore mirrors ResolveCandidateStore for the
// merged_prs_observed audit store (used by the reconcile handler + MCP tool
// + prune scheduler).
func ResolveMergedPRsStore(stores storage.ServerStores) mergedprs.Store {
	if sqliteDB := stores.SqliteDB(); sqliteDB != nil {
		return mergedprs.NewSQLiteStore(sqliteDB.SqlConn(), sqliteDB.WorkspaceID())
	}
	if pool := stores.PgxPool(); pool != nil {
		return mergedprs.NewPgStore(pool, stores.WorkspaceID())
	}
	return nil
}
