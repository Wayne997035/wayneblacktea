package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"os"

	mcpsrv "github.com/Wayne997035/wayneblacktea/internal/mcp"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
	"github.com/jackc/pgx/v5/pgxpool"
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
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL not set")
	}

	log.Printf("storage backend: %s", backend)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("parsing database URL: %w", err)
	}
	// Aiven uses a custom CA not in the system trust store.
	cfg.ConnConfig.TLSConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // Aiven custom CA

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer pool.Close()

	s, err := mcpsrv.New(pool)
	if err != nil {
		return fmt.Errorf("initializing MCP server: %w", err)
	}
	if err := server.ServeStdio(s.MCPServer()); err != nil {
		return fmt.Errorf("serving MCP: %w", err)
	}
	return nil
}
