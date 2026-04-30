package main

import (
	"context"
	"fmt"
	"log"

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

func buildStores(backend storage.Backend) (storage.ServerStores, error) {
	stores, err := storage.BuildServerStores(context.Background(), backend)
	if err != nil {
		return nil, fmt.Errorf("building stores for backend %s: %w", backend, err)
	}
	return stores, nil
}
