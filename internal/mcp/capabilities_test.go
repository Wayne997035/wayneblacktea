package mcp_test

import (
	"context"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/llm"
	mcpsrv "github.com/Wayne997035/wayneblacktea/internal/mcp"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
)

// TestServerCapabilityRegistry_MatchesKnownSet is the drift guard the A3/G2
// task asked for: if a new optional capability setter (With*) is added to
// *Server without anyone updating this test, it fails immediately — forcing
// the author to also wire the new capability into WireOptionalCapabilities
// so the two MCP transports (HTTP cmd/server, stdio internal/mcprunner)
// cannot silently drift apart again the way stdio drifted before this PR
// (missing WithDecisionDrafter / WithCompletionCandidates /
// WithMergedPRsStore).
//
// This is deliberately reflection-driven against the real *Server type
// rather than a hand-maintained list checked by inspection — it catches
// method-set changes, not just call-site changes.
func TestServerCapabilityRegistry_MatchesKnownSet(t *testing.T) {
	st := reflect.TypeOf(&mcpsrv.Server{})
	var found []string
	for i := 0; i < st.NumMethod(); i++ {
		name := st.Method(i).Name
		if strings.HasPrefix(name, "With") {
			found = append(found, name)
		}
	}
	sort.Strings(found)

	// Known set as of this PR. Adding/removing a With* method on Server
	// MUST update this list AND WireOptionalCapabilities together.
	want := []string{
		"WithClassifier",
		"WithCompletionCandidates",
		"WithDecisionDrafter",
		"WithMergedPRsStore",
		"WithSnapshot",
	}
	sort.Strings(want)

	if !reflect.DeepEqual(found, want) {
		t.Fatalf("Server's With* capability setters drifted from the known registry.\n got:  %v\nwant: %v\n"+
			"If you added or removed a With* method, update this list AND internal/mcp/capabilities.go's "+
			"WireOptionalCapabilities together — that is what keeps cmd/server/main.go and "+
			"internal/mcprunner/runner.go from drifting apart again.", found, want)
	}
}

// sqliteFixture builds a real (file-backed) SQLite storage.ServerStores bundle,
// matching the pattern used by TestNew_AcceptsSQLiteBundle in server_test.go.
func sqliteFixture(t *testing.T) storage.ServerStores {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "capabilities.db")
	stores, err := storage.NewServerStores(context.Background(), storage.FactoryConfig{
		Backend:    storage.BackendSQLite,
		SQLitePath: dbPath,
	})
	if err != nil {
		t.Fatalf("NewServerStores: %v", err)
	}
	t.Cleanup(func() { _ = stores.Close() })
	return stores
}

// allLLMEnvKeys mirrors internal/llm/env_test.go's allKeys so this test can
// force a clean, single-provider (Claude) chain regardless of ambient env.
var allLLMEnvKeys = []string{
	"AI_PROVIDER", "AI_FALLBACK_PROVIDERS",
	"CLAUDE_API_KEY",
	"OPENROUTER_API_KEY", "OPENROUTER_MODEL", "OPENROUTER_MODELS",
	"GROQ_API_KEY", "GROQ_MODEL",
	"OPENAI_COMPATIBLE_BASE_URL", "OPENAI_COMPATIBLE_MODEL", "OPENAI_COMPATIBLE_API_KEY",
}

// TestWireOptionalCapabilities_SQLiteBackendWithClaudeKey is the behavioral
// counterpart to the registry test above: it exercises the actual assembly
// function end-to-end against a real SQLite ServerStores bundle with
// CLAUDE_API_KEY set, and asserts every capability the SQLite backend CAN
// support was actually wired. This is the test the A3/G2 dispatch's "remove
// a capability → red → restore → green" drift experiment was run against
// (see engineer report) — commenting out any `With*` call inside
// WireOptionalCapabilities turns this red.
//
// Snapshot is the one capability that stays unset here by design: it
// requires a Postgres pgxpool.Pool (ResolveSnapshotStore), which the SQLite
// backend never provides. That is a documented capability gap, not a bug —
// asserted explicitly below via SnapshotSkipReason.
func TestWireOptionalCapabilities_SQLiteBackendWithClaudeKey(t *testing.T) {
	for _, k := range allLLMEnvKeys {
		t.Setenv(k, "")
	}
	t.Setenv("CLAUDE_API_KEY", "test-key-for-capability-wiring")

	stores := sqliteFixture(t)
	s, err := mcpsrv.New(stores)
	if err != nil {
		t.Fatalf("mcpsrv.New: %v", err)
	}

	chain := llm.BuildChainFromEnv()
	if chain.Len() == 0 {
		t.Fatalf("test setup: expected a non-empty chain with CLAUDE_API_KEY set, got Len()=0")
	}

	report := s.WireOptionalCapabilities(mcpsrv.CapabilityInputs{
		Chain:          chain,
		SnapshotStore:  nil, // SQLite backend: ResolveSnapshotStore(stores) would also return nil.
		SnapshotGen:    nil,
		CandidateStore: mcpsrv.ResolveCandidateStore(stores),
		MergedPRsStore: mcpsrv.ResolveMergedPRsStore(stores),
	})

	if !report.Classifier {
		t.Error("expected Classifier=true (non-empty chain)")
	}
	if !report.DecisionDrafter {
		t.Error("expected DecisionDrafter=true (non-empty chain)")
	}
	if !report.CompletionCandidates {
		t.Error("expected CompletionCandidates=true (SQLite backend resolves a candidate store)")
	}
	if !report.MergedPRsStore {
		t.Error("expected MergedPRsStore=true (SQLite backend resolves a merged-PRs store)")
	}
	if report.Snapshot {
		t.Error("expected Snapshot=false on SQLite backend (no PgxPool) — got true")
	}
	if report.SnapshotSkipReason == "" {
		t.Error("expected a non-empty SnapshotSkipReason explaining why snapshot stayed unset")
	}
}

// TestResolveCandidateAndMergedPRsStore_SQLiteBackend locks the specific
// acceptance criterion from the A3/G2 dispatch: stdio's own dedicated SQLite
// connection (internal/mcprunner.buildStores) must be able to resolve both
// stores stdio previously lacked entirely.
func TestResolveCandidateAndMergedPRsStore_SQLiteBackend(t *testing.T) {
	stores := sqliteFixture(t)

	if got := mcpsrv.ResolveCandidateStore(stores); got == nil {
		t.Error("ResolveCandidateStore(sqlite bundle) = nil, want a *completioncandidate.SQLiteStore")
	}
	if got := mcpsrv.ResolveMergedPRsStore(stores); got == nil {
		t.Error("ResolveMergedPRsStore(sqlite bundle) = nil, want a *mergedprs.SQLiteStore")
	}
	store, gen := mcpsrv.ResolveSnapshotStore(stores)
	if store != nil || gen != nil {
		t.Errorf("ResolveSnapshotStore(sqlite bundle) = (%v, %v), want (nil, nil) — snapshot is Postgres-only", store, gen)
	}
}
