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

import (
	"runtime/debug"
	"time"
)

// devModuleVersion is what the Go toolchain reports for a binary built from a
// working tree rather than fetched from the module proxy. It is not a version.
const devModuleVersion = "(devel)"

// moduleIdentity returns (version, sum) that `go install <module>@<version>`
// baked into this binary, or ("", "") when there is none.
//
// This is the distribution model this project actually uses: the git tag is
// the release, the module proxy does the delivery, and `go install
// github.com/Wayne997035/wayneblacktea/cmd/wbt@v1.0.0` is the install command.
// Nothing injects ldflags on that path — the version arrives inside the binary
// instead, put there by the toolchain, along with the module's h1 sum, which
// the checksum database verifies at install time.
//
// A `go build` from a checkout reports "(devel)" here; that is not an identity
// and MUST NOT be treated as one, or every local dev build would claim to be a
// release. Railway's image is exactly that case — it builds from a checkout
// and gets its identity from ldflags instead.
func moduleIdentity() (string, string) {
	bi, ok := debug.ReadBuildInfo()
	if !ok || bi == nil {
		return "", ""
	}
	v := bi.Main.Version
	if v == "" || v == devModuleVersion {
		return "", ""
	}
	return v, bi.Main.Sum
}

// Version, Commit, and Date are set at link time via:
//
//	-X github.com/Wayne997035/wayneblacktea/internal/buildinfo.Version=<tag>
//	-X github.com/Wayne997035/wayneblacktea/internal/buildinfo.Commit=<sha>
//	-X github.com/Wayne997035/wayneblacktea/internal/buildinfo.Date=<iso8601>
//
// build/Dockerfile sets Commit/Date for the Railway production image via
// COMMIT/BUILD_DATE build args (see that file's builder stage); it leaves
// Version at the sentinel, because a branch build is not a release.
//
// **Nothing sets Version any more.** The binary-release pipeline that used to
// (goreleaser on a tag push) was removed: this project distributes through the
// git tag plus the Go module proxy, and `go install <module>@<tag>` injects no
// ldflags. The version travels inside the build info instead — moduleIdentity()
// reads it, and EffectiveVersion() prefers it over the pseudo-version. The
// ldflags path is kept because build/Dockerfile can still use it.
//
// A plain `go build` / `go test` with no ldflags — every local dev build and
// every test run — leaves these at their sentinel defaults. The defaults are
// deliberately NOT "0.1.0", "", or any other string that could pass for a
// real version: "dev"/"none"/"unknown" are unambiguous "this build has no
// injected identity" markers, matching the existing convention in
// cmd/wbt/main.go's own version/commit/date vars (that binary's `wbt
// version` command).
// sentinelVersion, sentinelCommit, and sentinelDate name the three defaults
// above so every other reference to "no injected identity" in this file (and
// in this package's tests, which share this package and can see these
// directly) is the SAME string, never a second independently-typed literal
// that could quietly drift from the var block's own default.
const (
	sentinelVersion = "dev"
	sentinelCommit  = "none"
	sentinelDate    = "unknown"
)

var (
	Version = sentinelVersion
	Commit  = sentinelCommit
	Date    = sentinelDate
)

// commitShaLen is how many hex characters of Commit BuildID uses. Chosen to
// match Go's own pseudo-version convention (golang.org/x/mod/module,
// "vX.0.0-yyyymmddhhmmss-abcdefabcdef" uses a 12-character short SHA) so a
// build ID looks familiar to anyone who has read a `go list -m` pseudo-
// version before, without actually claiming to BE one (see BuildID's doc
// comment for the one deliberate deviation: build time instead of commit
// time).
const commitShaLen = 12

// buildIDTimeLayout renders Date (RFC3339, e.g. "2026-08-16T07:43:48Z") into
// the compact yyyymmddhhmmss form Go pseudo-versions use.
const buildIDTimeLayout = "20060102150405"

// BuildID returns a Go-pseudo-version-shaped synthetic identifier derived
// from Commit and Date: "v0.0.0-<yyyymmddhhmmss>-<sha12>". It exists for
// Railway production deploys, which set Commit/Date (from
// RAILWAY_GIT_COMMIT_SHA, build/Dockerfile) but never Version — there is no
// git tag driving those builds, so EffectiveVersion falls back to this instead
// of reporting the bare "dev" sentinel for every production build forever.
//
// Deliberately NOT a real Go pseudo-version: the timestamp is BUILD time
// (Date, set at Docker build time), not COMMIT time (which Railway's build
// args don't expose — see build/Dockerfile's RAILWAY_GIT_COMMIT_SHA comment
// block). The two differ by however long the build took (measured ~20s for
// this repo) — close enough to be a useful "which build is this" fingerprint,
// not close enough to be interchangeable with `go list -m`'s own pseudo-
// version string for the same commit. No "+" suffix either (real pseudo-
// versions never have one at the module-path level; a trailing "+dirty" or
// similar is a different convention this deliberately does not adopt).
//
// Returns the "dev" sentinel — never a partial or malformed identifier — when
// Commit is at its sentinel, Date fails to parse as RFC3339, or Commit is
// shorter than commitShaLen. A build identity that looks real but isn't
// (e.g. a truncated or zero-padded SHA) is worse than an obviously-fake one,
// per this package's own doc comment.
func BuildID() string {
	if Commit == sentinelCommit || len(Commit) < commitShaLen {
		return sentinelVersion
	}
	t, err := time.Parse(time.RFC3339, Date)
	if err != nil {
		return sentinelVersion
	}
	return "v0.0.0-" + t.UTC().Format(buildIDTimeLayout) + "-" + Commit[:commitShaLen]
}

// FullBuildID identifies the exact build, where BuildID identifies the release
// line. They answer different questions and must not be collapsed into one
// value: "which release is this?" is answered by a sortable pseudo-version
// (BuildID, and through it EffectiveVersion); "which build exactly?" is
// answered here.
//
// Shape: "<commit>@<RFC3339 UTC build time>". Two deliberate differences from
// BuildID's shape:
//
//   - the commit is emitted VERBATIM, never truncated to commitShaLen. A
//     12-hex prefix is enough to be readable but not enough to hand to
//     `git show` with certainty; this field exists so a reader can go from a
//     running service to the exact object without guessing.
//   - the timestamp keeps its RFC3339 punctuation instead of collapsing to
//     buildIDTimeLayout. Nothing sorts on this field — it is an identifier,
//     not a version — so readability wins over compactness.
//
// Returns the same "dev" sentinel as BuildID under the same conditions, for
// the same reason: an identity that looks real but isn't is worse than an
// obviously-fake one.
func FullBuildID() string {
	if Commit == sentinelCommit || len(Commit) < commitShaLen {
		// 沒有 ldflags,但可能是 `go install <module>@<version>` 裝的。那條路徑
		// 沒有 commit 可注入,卻有更強的東西:module 的 h1 sum —— 它釘住的是這個
		// 版本的**完整位元組**,而 checksum database 在安裝當下驗過。拿它當「哪一次
		// 建置」比回哨兵誠實:那個 binary 確實知道自己是誰。
		if v, sum := moduleIdentity(); v != "" && sum != "" {
			return v + " " + sum
		}
		return sentinelVersion
	}
	t, err := time.Parse(time.RFC3339, Date)
	if err != nil {
		return sentinelVersion
	}
	return Commit + "@" + t.UTC().Format(time.RFC3339)
}

// EffectiveVersion returns Version when it carries a real (non-sentinel)
// value — an -ldflags-injected tag — then the module version a
// `go install <module>@<tag>` baked in, and finally BuildID()
// (itself "dev" when no build identity is available at all). This is the
// single function internal/mcp reads for both serverInfo.version
// (server.go) and the wayneblacktea://system/build-info resource
// (resources.go): reading the same function from both places is what keeps
// them from independently drifting apart, the same structural guarantee
// buildinfo's package doc already describes for Version/Commit/Date
// themselves.
// 三層,由強到弱。NEVER 調換順序:ldflags 是建置者的明示宣告,module 版本是
// 工具鏈的事實,pseudo-version 是從 commit+時間推導出來的最後手段。
//  1. ldflags 注入的真 tag(Railway 映像走這條,由 build/Dockerfile 設定)
//  2. `go install <module>@<version>` baked 進去的 module 版本
//  3. BuildID() 的 pseudo-version;三者皆無時它自己回 dev 哨兵
func EffectiveVersion() string {
	if Version != sentinelVersion {
		return Version
	}
	if v, _ := moduleIdentity(); v != "" {
		return v
	}
	return BuildID()
}
