// cmd/mcp is the canonical wayneblacktea MCP server binary intended for
// systemd / docker deployments. End users typically invoke `wbt mcp`
// instead, which delegates to the same internal/mcprunner.Run entry point.
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/Wayne997035/wayneblacktea/internal/mcprunner"
)

// Version metadata injected at link time via goreleaser ldflags.
var (
	version = "dev"
	commit  = "none"
)

func main() {
	if len(os.Args) >= 2 && (os.Args[1] == "version" || os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Printf("%s %s (%s)\n", filepath.Base(os.Args[0]), version, commit)
		return
	}
	if err := mcprunner.Run(); err != nil {
		log.Fatal(err)
	}
}
