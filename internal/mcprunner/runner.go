// Package mcprunner is the shared MCP stdio transport entry point invoked
// by `cmd/wbt mcp` (wired into .mcp.json by `wbt init`). Keeping the wiring
// in one place keeps the MCP boot path consistent across any future MCP
// entry points (e.g. HTTP transport added by Phase 2 `wbt setup`).
package mcprunner

import (
	"context"
	"fmt"
	"log"

	"github.com/Wayne997035/wayneblacktea/internal/llm"
	mcpsrv "github.com/Wayne997035/wayneblacktea/internal/mcp"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
	"github.com/mark3labs/mcp-go/server"
)

// Run resolves the storage backend, builds the stores, wires the same
// optional capability set the HTTP transport gets (activity classifier,
// decision drafter, snapshot support, completion-candidate + merged-PRs
// stores — see internal/mcp.WireOptionalCapabilities), and serves MCP over
// stdio. It blocks until ServeStdio returns. Callers typically log.Fatal on
// error.
func Run() error {
	backend, err := storage.ResolveFromEnv()
	if err != nil {
		return fmt.Errorf("resolving storage backend: %w", err)
	}
	log.Printf("storage backend: %s", backend)

	stores, err := buildStores(backend)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := stores.Close(); cerr != nil {
			log.Printf("closing stores: %v", cerr)
		}
	}()

	s, err := mcpsrv.New(stores)
	if err != nil {
		return fmt.Errorf("initializing MCP server: %w", err)
	}

	// Build the provider-neutral chain from env. Backward compat: with only
	// CLAUDE_API_KEY set, this resolves to a single-Claude chain identical
	// to the pre-refactor behaviour.
	llmChain := llm.BuildChainFromEnv()
	if llmChain.Len() > 0 {
		log.Printf("llm: provider chain = %v", llmChain.Names())
	} else {
		log.Println("llm: memory-only mode (no provider configured)")
	}

	// stdio opens its own dedicated connection (buildStores above), so the
	// snapshot / completion-candidate / merged-PRs stores below are built
	// against that dedicated pool/SQLite handle — not the HTTP process's
	// shared pool. WireOptionalCapabilities is the same function
	// cmd/server/main.go calls; this is what keeps the two transports from
	// drifting apart again (see docs/architecture.md "MCP server wiring").
	snapStore, snapGen := mcpsrv.ResolveSnapshotStore(stores)
	candidateStore := mcpsrv.ResolveCandidateStore(stores)
	mergedPRsStore := mcpsrv.ResolveMergedPRsStore(stores)
	capReport := s.WireOptionalCapabilities(mcpsrv.CapabilityInputs{
		Chain:          llmChain,
		SnapshotStore:  snapStore,
		SnapshotGen:    snapGen,
		CandidateStore: candidateStore,
		MergedPRsStore: mergedPRsStore,
	})
	log.Printf("mcp: stdio transport capabilities = %+v", capReport)

	if err := server.ServeStdio(s.MCPServer()); err != nil {
		return fmt.Errorf("serving MCP: %w", err)
	}
	return nil
}

// buildStores wraps storage.BuildServerStores with the standard error context.
func buildStores(backend storage.Backend) (storage.ServerStores, error) {
	stores, err := storage.BuildServerStores(context.Background(), backend)
	if err != nil {
		return nil, fmt.Errorf("building stores for backend %s: %w", backend, err)
	}
	return stores, nil
}
