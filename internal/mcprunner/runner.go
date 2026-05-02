// Package mcprunner is the shared MCP stdio transport entry point used by
// both `cmd/mcp` (the canonical binary) and `cmd/wbt mcp` (the user-facing
// subcommand wired into .mcp.json by `wbt init`). Keeping the wiring in one
// place ensures both binaries serve identical MCP behaviour.
package mcprunner

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	mcpsrv "github.com/Wayne997035/wayneblacktea/internal/mcp"
	"github.com/Wayne997035/wayneblacktea/internal/snapshot"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
	"github.com/mark3labs/mcp-go/server"
)

// Run resolves the storage backend, builds the stores, wires the optional
// ActivityClassifier (when CLAUDE_API_KEY is set), and serves MCP over stdio.
// It blocks until ServeStdio returns. Callers typically log.Fatal on error.
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

	if claudeKey := os.Getenv("CLAUDE_API_KEY"); claudeKey != "" {
		s.WithClassifier(ai.NewActivityClassifier(claudeKey))
	}

	// Wire snapshot store + generator when CLAUDE_API_KEY is set.
	// The snapshot store requires a Postgres pool; on SQLite the feature is
	// silently disabled — the tool returns a "not configured" error message.
	if claudeKey := os.Getenv("CLAUDE_API_KEY"); claudeKey != "" {
		if pool := stores.PgxPool(); pool != nil {
			snapStore := snapshot.NewStore(pool, stores.WorkspaceID())
			snapGen := snapshot.NewGenerator(claudeKey)
			s.WithSnapshot(snapStore, snapGen)
		}
	}

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
