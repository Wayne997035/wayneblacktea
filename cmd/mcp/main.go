package main

import (
	"context"
	"fmt"
	"log"
	"os"

	mcpsrv "github.com/Wayne997035/wayneblacktea/internal/mcp"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
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
	if err := server.ServeStdio(s.MCPServer()); err != nil {
		return fmt.Errorf("serving MCP: %w", err)
	}
	return nil
}

// buildStores wires the storage factory the same way cmd/server does so the
// two binaries stay in sync on backend selection / DSN handling.
func buildStores(backend storage.Backend) (storage.ServerStores, error) {
	cfg := storage.FactoryConfig{Backend: backend}
	switch backend {
	case storage.BackendPostgres:
		dsn := os.Getenv("DATABASE_URL")
		if dsn == "" {
			return nil, fmt.Errorf("DATABASE_URL not set")
		}
		cfg.PostgresDSN = dsn
		cfg.PostgresInsecureTLS = true
	case storage.BackendSQLite:
		cfg.SQLitePath = storage.SQLitePathFromEnv()
	}
	stores, err := storage.NewServerStores(context.Background(), cfg)
	if err != nil {
		return nil, fmt.Errorf("building stores for backend %s: %w", backend, err)
	}
	return stores, nil
}
