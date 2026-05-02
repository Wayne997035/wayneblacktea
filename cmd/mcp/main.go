// cmd/mcp is the canonical wayneblacktea MCP server binary intended for
// systemd / docker deployments. End users typically invoke `wbt mcp`
// instead, which delegates to the same internal/mcprunner.Run entry point.
package main

import (
	"log"

	"github.com/Wayne997035/wayneblacktea/internal/mcprunner"
)

func main() {
	if err := mcprunner.Run(); err != nil {
		log.Fatal(err)
	}
}
