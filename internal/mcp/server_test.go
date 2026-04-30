package mcp_test

import (
	"context"
	"path/filepath"
	"testing"

	mcpsrv "github.com/Wayne997035/wayneblacktea/internal/mcp"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
)

// TestNew_AcceptsSQLiteBundle verifies the MCP server constructor wires up
// against a SQLite-backed ServerStores bundle (PgxPool == nil) without
// panicking. This is the regression guard for the SQLite v2 cmd dispatch:
// if the constructor regresses to requiring *pgxpool.Pool, this fails fast.
func TestNew_AcceptsSQLiteBundle(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "mcp-bundle.db")
	stores, err := storage.NewServerStores(context.Background(), storage.FactoryConfig{
		Backend:    storage.BackendSQLite,
		SQLitePath: dbPath,
	})
	if err != nil {
		t.Fatalf("NewServerStores: %v", err)
	}
	t.Cleanup(func() { _ = stores.Close() })

	srv, err := mcpsrv.New(stores)
	if err != nil {
		t.Fatalf("mcpsrv.New(sqlite bundle): %v", err)
	}
	if srv == nil {
		t.Fatal("mcpsrv.New returned nil server")
	}
	// Smoke: registering tools must not panic for the sqlite path either.
	if ms := srv.MCPServer(); ms == nil {
		t.Fatal("MCPServer() returned nil")
	}
}
