// Package buildinfo is the single source of truth for the process's build
// identity: the version/commit/date this specific binary was built from.
//
// GTD 61838147: before this package existed, the MCP server reported a
// hardcoded "0.1.0" as its serverInfo.version in every build, forever — no
// mechanism reported which actual build was running, so answering that
// question meant reverse-engineering it from runtime behaviour instead of
// reading it directly. A hardcoded string that LOOKS like a real version is
// worse than an obviously-fake one: it is indistinguishable from a real
// release until something goes wrong and the false trail wastes debugging
// time. This package's defaults are deliberately non-version-shaped
// sentinels for exactly that reason — see the var block below.
//
// internal/mcp is the only importer of this package (server.go's serverInfo
// and resources.go's wayneblacktea://system/build-info resource). Both the
// HTTP transport (cmd/server) and the stdio transport (internal/mcprunner,
// invoked by `wbt mcp`) construct their MCP server through internal/mcp, so
// neither needs to thread these values through separately — importing
// buildinfo once, here, is what keeps the two transports from reporting
// different build identities for the same binary (see server.go's
// MCPServer doc comment).
package buildinfo

// Version, Commit, and Date are set at link time via:
//
//	-X github.com/Wayne997035/wayneblacktea/internal/buildinfo.Version=<tag>
//	-X github.com/Wayne997035/wayneblacktea/internal/buildinfo.Commit=<sha>
//	-X github.com/Wayne997035/wayneblacktea/internal/buildinfo.Date=<iso8601>
//
// .goreleaser.yaml sets these for the `server` and `wbt` build ids (the
// binaries that can run an MCP server — HTTP and stdio respectively) on every
// tagged release, so a release build reports its real tag/commit/date.
// build/Dockerfile sets them for the Railway production image via
// VERSION/COMMIT/BUILD_DATE build args (see that file's builder stage).
//
// A plain `go build` / `go test` with no ldflags — every local dev build and
// every test run — leaves these at their sentinel defaults. The defaults are
// deliberately NOT "0.1.0", "", or any other string that could pass for a
// real version: "dev"/"none"/"unknown" are unambiguous "this build has no
// injected identity" markers, matching the existing convention in
// cmd/wbt/main.go's own version/commit/date vars (that binary's `wbt
// version` command).
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)
