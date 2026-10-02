package mcp

import (
	"github.com/Wayne997035/wayneblacktea/internal/discipline"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// applyReadOnlyAnnotations is the single post-registration pass that sets
// readOnlyHint=true/destructiveHint=false for every tool named in
// discipline.ReadOnlyTools (D1). It runs once, after all 29
// register*Tools(ms) calls in MCPServer() finish — this is why none of the 99
// mcp.NewTool() call sites need an individual annotation option.
//
// Verified against mcp-go v0.49.0 server.MCPServer.{ListTools,SetTools}
// (server/server.go:836-866): ListTools returns per-entry copies (safe under
// Go 1.22+ per-iteration loop vars — this repo is Go 1.26.8), and SetTools
// replaces the whole tool map and fires ToolsListChanged, which is a no-op
// here because MCPServer() runs before any transport hands ms to a client —
// zero registered sessions exist at this point (session.go's notification
// path iterates an empty session set).
func applyReadOnlyAnnotations(ms *server.MCPServer) {
	tools := ms.ListTools()
	all := make([]server.ServerTool, 0, len(tools))
	for name, entry := range tools {
		if discipline.ReadOnlyTools[name] {
			// Reassign the whole pointer rather than dereference-mutate
			// (*entry.Tool.Annotations.ReadOnlyHint = true): each tool's
			// hint pointers are independently allocated by mcp.NewTool
			// (mcp/tools.go:793-797), so dereference-mutation happens not to
			// alias another tool's annotation today, but that is a fragile
			// invariant to depend on — reassignment doesn't need it to hold.
			entry.Tool.Annotations.ReadOnlyHint = mcp.ToBoolPtr(true)
			entry.Tool.Annotations.DestructiveHint = mcp.ToBoolPtr(false)
		}
		// Tools outside ReadOnlyTools are appended untouched, keeping
		// mcp-go's default annotations (ReadOnlyHint=false,
		// DestructiveHint=true).
		all = append(all, *entry)
	}
	ms.SetTools(all...)
}
