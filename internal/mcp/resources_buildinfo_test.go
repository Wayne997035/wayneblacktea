package mcp

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/buildinfo"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// sentinelVersion is buildinfo.Version's own zero-ldflags default (see that
// package's var block) — these tests assert against this exact value rather
// than an arbitrary "dev"-looking string, so a failure here means the
// sentinel itself changed behaviour, not that the test drifted from it.
const sentinelVersion = "dev"

// rpcServerInfo issues a real "initialize" call over the JSON-RPC entry point
// (the same one both transports use — server.go's MCPServer doc comment) and
// returns the decoded serverInfo block.
func rpcServerInfo(t *testing.T, ms *server.MCPServer) mcpmsg.Implementation {
	t.Helper()
	const req = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25",` +
		`"capabilities":{},"clientInfo":{"name":"test","version":"0.0.0"}}}`
	resp := ms.HandleMessage(context.Background(), json.RawMessage(req))
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal initialize response: %v", err)
	}
	var decoded struct {
		Result struct {
			ServerInfo mcpmsg.Implementation `json:"serverInfo"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode initialize response: %v\nraw: %s", err, raw)
	}
	if decoded.Error != nil {
		t.Fatalf("initialize returned error: %s", decoded.Error.Message)
	}
	return decoded.Result.ServerInfo
}

// TestMCPServer_VersionComesFromBuildinfo pins GTD 61838147: serverInfo.version
// must be read live from buildinfo.Version, not a hardcoded literal — the
// pre-fix behaviour was server.go:445 always returning "0.1.0", for every
// build, forever. Overriding buildinfo.Version with an arbitrary canary
// string that cannot exist anywhere in source as a literal is what makes
// this a proof rather than a claim: only a live read of the variable can
// reproduce that exact string in the response.
func TestMCPServer_VersionComesFromBuildinfo(t *testing.T) {
	const canary = "test-canary-9f3a1c2e-not-a-real-version"
	orig := buildinfo.Version
	buildinfo.Version = canary
	t.Cleanup(func() { buildinfo.Version = orig })

	s := newTestResourceServer(t)
	info := rpcServerInfo(t, s.MCPServer())

	if info.Version != canary {
		t.Errorf("serverInfo.version = %q, want the buildinfo.Version canary %q — version is not being "+
			"read live from buildinfo (possible hardcoded-literal regression)", info.Version, canary)
	}
	if info.Version == "0.1.0" {
		t.Error("serverInfo.version = \"0.1.0\" — the pre-fix hardcoded literal is back")
	}
}

// TestMCPServer_NoLdflagsSentinel pins the "no fake version" half of GTD
// 61838147: a test binary is built with no -ldflags, so buildinfo.Version
// must be the explicit "dev" sentinel — never "0.1.0", never "", never any
// other string that could pass for a real release tag. A build with no
// injected identity must be OBVIOUSLY unreleased, not silently
// indistinguishable from a tagged one (that indistinguishability was the
// original bug: "0.1.0" looked real and never changed).
func TestMCPServer_NoLdflagsSentinel(t *testing.T) {
	if buildinfo.Version != sentinelVersion {
		t.Errorf("buildinfo.Version = %q in an ldflags-less test build, want sentinel %q",
			buildinfo.Version, sentinelVersion)
	}
	if buildinfo.Commit != "none" {
		t.Errorf("buildinfo.Commit = %q in an ldflags-less test build, want sentinel %q", buildinfo.Commit, "none")
	}
	if buildinfo.Date != "unknown" {
		t.Errorf("buildinfo.Date = %q in an ldflags-less test build, want sentinel %q", buildinfo.Date, "unknown")
	}

	s := newTestResourceServer(t)
	info := rpcServerInfo(t, s.MCPServer())
	if info.Version != sentinelVersion {
		t.Errorf("serverInfo.version = %q, want the sentinel %q to survive unchanged into serverInfo",
			info.Version, sentinelVersion)
	}
}

// TestMCPServer_VersionFallsBackToBuildIDWhenSentinel is U6's acceptance
// criterion for serverInfo.version reading buildinfo.EffectiveVersion()
// (server.go:471, 2026-08-20-mcp-surface-spec.md): when Version is at its
// "dev" sentinel but Commit/Date carry real production values (the state
// every current Railway deploy is in — no git tag has ever driven one, see
// README's new "Checking what's running" section), serverInfo.version must
// report the synthetic build ID instead of the bare "dev" sentinel.
func TestMCPServer_VersionFallsBackToBuildIDWhenSentinel(t *testing.T) {
	origV, origC, origD := buildinfo.Version, buildinfo.Commit, buildinfo.Date
	buildinfo.Version = sentinelVersion
	buildinfo.Commit = "5b87fcbf4b78dd0dc290e25e3b220da110dd316f"
	buildinfo.Date = "2026-08-16T07:43:48Z"
	t.Cleanup(func() {
		buildinfo.Version, buildinfo.Commit, buildinfo.Date = origV, origC, origD
	})

	const wantBuildID = "v0.0.0-20260816074348-5b87fcbf4b78"

	s := newTestResourceServer(t)
	info := rpcServerInfo(t, s.MCPServer())

	if info.Version != wantBuildID {
		t.Errorf("serverInfo.version = %q, want the synthetic build ID %q (Version was at its \"dev\" "+
			"sentinel — EffectiveVersion must fall back to BuildID(), not report \"dev\" verbatim)",
			info.Version, wantBuildID)
	}
	if info.Version == sentinelVersion {
		t.Error("serverInfo.version = \"dev\" — EffectiveVersion did not fall back to BuildID() even " +
			"though Commit/Date carried real values")
	}
}

// TestResourceBuildInfo_BuildIDMatchesServerInfo is the build_id half of
// TestResourceBuildInfo_MatchesServerInfo below: build_id and
// serverInfo.version both read buildinfo.EffectiveVersion() (server.go's
// MCPServer doc comment), so they can never independently drift — even when
// Version is at its sentinel and both are falling back to a synthetic
// BuildID() value rather than echoing buildinfo.Version directly.
func TestResourceBuildInfo_BuildIDMatchesServerInfo(t *testing.T) {
	origV, origC, origD := buildinfo.Version, buildinfo.Commit, buildinfo.Date
	buildinfo.Version = sentinelVersion
	buildinfo.Commit = "5b87fcbf4b78dd0dc290e25e3b220da110dd316f"
	buildinfo.Date = "2026-08-16T07:43:48Z"
	t.Cleanup(func() {
		buildinfo.Version, buildinfo.Commit, buildinfo.Date = origV, origC, origD
	})

	s := newTestResourceServer(t)
	info := rpcServerInfo(t, s.MCPServer())

	contents, err := s.handleResourceBuildInfo(context.Background(), mcpmsg.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("handleResourceBuildInfo: %v", err)
	}
	var got buildInfoResource
	parseResourceJSON(t, contents, &got)

	if got.BuildID != info.Version {
		t.Errorf("resource build_id %q != serverInfo.version %q — the two have drifted apart",
			got.BuildID, info.Version)
	}
	if got.BuildID == sentinelVersion {
		t.Error("resource build_id = \"dev\" — EffectiveVersion did not fall back to BuildID()")
	}
	// version applies the SAME fallback as build_id and serverInfo.version.
	// This assertion previously demanded the opposite — that version stay at
	// the raw "dev" sentinel — which contradicted the ruling it was written
	// under (decision 1a7a310a: "Version 仍是哨兵時 serverInfo.version 與
	// resource.version 同步回傳 build_id") and shipped to production, where a
	// deploy that knew its own commit still reported version="dev". A test
	// that pins the violation as the contract is worse than no test: it makes
	// the defect look verified.
	if got.Version != info.Version {
		t.Errorf("resource version %q != serverInfo.version %q — decision 1a7a310a requires both to "+
			"report the build identity when buildinfo.Version is at its sentinel", got.Version, info.Version)
	}
	if got.Version != got.BuildID {
		t.Errorf("resource version %q != build_id %q — both must read buildinfo.EffectiveVersion()",
			got.Version, got.BuildID)
	}
	if got.Version == sentinelVersion {
		t.Errorf("resource version = %q on a build that has a real commit (%q) and build date (%q) — "+
			"a reader of this field alone would conclude production is running an identity-less build",
			got.Version, buildinfo.Commit, buildinfo.Date)
	}
}

// TestResourceBuildInfo_MatchesServerInfo pins the "single source, no drift"
// requirement (dispatch §B④): the wayneblacktea://system/build-info resource
// and serverInfo.version must never independently diverge. Overrides all
// three buildinfo vars with canary values and asserts the resource echoes
// them exactly — the same technique
// TestMCPServer_VersionComesFromBuildinfo uses for serverInfo.
func TestResourceBuildInfo_MatchesServerInfo(t *testing.T) {
	origV, origC, origD := buildinfo.Version, buildinfo.Commit, buildinfo.Date
	buildinfo.Version = "canary-version-7a1b"
	buildinfo.Commit = "canary-commit-2e9f"
	buildinfo.Date = "canary-date-c4d8"
	t.Cleanup(func() {
		buildinfo.Version, buildinfo.Commit, buildinfo.Date = origV, origC, origD
	})

	s := newTestResourceServer(t)
	info := rpcServerInfo(t, s.MCPServer())

	contents, err := s.handleResourceBuildInfo(context.Background(), mcpmsg.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("handleResourceBuildInfo: %v", err)
	}
	var got buildInfoResource
	parseResourceJSON(t, contents, &got)

	if got.Version != info.Version {
		t.Errorf("resource version %q != serverInfo.version %q — the two have drifted apart", got.Version, info.Version)
	}
	if got.Version != "canary-version-7a1b" {
		t.Errorf("resource version = %q, want canary %q", got.Version, "canary-version-7a1b")
	}
	// 真 tag 分支的 build_id 斷言。缺了它,把 resources.go 的 BuildID 換成姐妹函式
	// buildinfo.BuildID() 會編譯得過、全套測試仍綠,而這個分支實際分岔成
	// version="canary-version-7a1b" vs build_id="dev" —— 已用 mutation 實測證實。
	// sentinel 分支由 TestResourceBuildInfo_BuildIDMatchesServerInfo 守,兩支合起來
	// 才蓋住 buildIDNote 對外宣稱的「兩欄同值」。
	if got.BuildID != got.Version {
		t.Errorf("resource build_id %q != version %q on the real-tag branch — both must read "+
			"buildinfo.EffectiveVersion(); buildIDNote tells readers the two carry the same value",
			got.BuildID, got.Version)
	}
	if got.Commit != "canary-commit-2e9f" {
		t.Errorf("resource commit = %q, want canary %q", got.Commit, "canary-commit-2e9f")
	}
	if got.BuildDate != "canary-date-c4d8" {
		t.Errorf("resource build_date = %q, want canary %q", got.BuildDate, "canary-date-c4d8")
	}
}

// TestResourceBuildInfo_ProtocolVersionAndBackend asserts the two fields not
// sourced from buildinfo: protocol_version must be mcp-go's own compiled-in
// LATEST_PROTOCOL_VERSION (not a value this server invented and could drift
// from what it actually speaks), and backend must match the store bundle the
// test server was actually constructed with (SQLite, per
// newTestResourceServer).
func TestResourceBuildInfo_ProtocolVersionAndBackend(t *testing.T) {
	s := newTestResourceServer(t)

	contents, err := s.handleResourceBuildInfo(context.Background(), mcpmsg.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("handleResourceBuildInfo: %v", err)
	}
	var got buildInfoResource
	parseResourceJSON(t, contents, &got)

	if got.ProtocolVersion != mcpmsg.LATEST_PROTOCOL_VERSION {
		t.Errorf("protocol_version = %q, want mcp.LATEST_PROTOCOL_VERSION = %q",
			got.ProtocolVersion, mcpmsg.LATEST_PROTOCOL_VERSION)
	}
	if got.Backend != "sqlite" {
		t.Errorf("backend = %q, want %q (newTestResourceServer is SQLite-backed)", got.Backend, "sqlite")
	}
}

// TestResourceBuildInfo_PostgresBackend is backendKind's Postgres half
// (backend-security-design.md §6.5 — every PG-vs-SQLite-differing bit of
// logic gets both a SQLite test and a real testcontainers Postgres test, not
// "logic is trivial so one suffices"). Reuses the package's existing
// Postgres testcontainer (tools_plan_pg_test.go's TestMain / mcpPlanTestPgPool)
// rather than starting a second one — handleResourceBuildInfo only touches
// s.pool and s.now (both safe on a bare &Server{}), so no store bundle is
// needed.
func TestResourceBuildInfo_PostgresBackend(t *testing.T) {
	if testing.Short() {
		t.Skip("postgres testcontainer is not started under -short (tools_plan_pg_test.go TestMain)")
	}
	if mcpPlanTestPgPool == nil {
		t.Fatal("mcpPlanTestPgPool is nil — the package's postgres testcontainer did not start")
	}

	s := &Server{pool: mcpPlanTestPgPool}
	contents, err := s.handleResourceBuildInfo(context.Background(), mcpmsg.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("handleResourceBuildInfo: %v", err)
	}
	var got buildInfoResource
	parseResourceJSON(t, contents, &got)

	if got.Backend != "postgres" {
		t.Errorf("backend = %q, want %q (server has a non-nil pgxpool.Pool)", got.Backend, "postgres")
	}
}

// TestResourceBuildInfo_NoUnexpectedFields is the compensating control for
// the dispatch threat surface (build info is easy to accidentally widen into
// an environment/secret dump): the resource's JSON MUST carry exactly the 8
// declared fields (6 pre-U6 + build_id/build_id_note, U6:
// 2026-08-20-mcp-surface-spec.md) and nothing else. A future author adding a
// field to buildInfoResource without updating this allowlist fails loudly
// here instead of silently shipping whatever they added.
func TestResourceBuildInfo_NoUnexpectedFields(t *testing.T) {
	s := newTestResourceServer(t)

	contents, err := s.handleResourceBuildInfo(context.Background(), mcpmsg.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("handleResourceBuildInfo: %v", err)
	}
	raw := resourceRawText(t, contents)

	var asMap map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &asMap); err != nil {
		t.Fatalf("unmarshal as map: %v", err)
	}

	want := []string{
		"generated_at", "version", "commit", "build_date", "protocol_version", "backend",
		"build_id", "build_id_note",
	}
	sort.Strings(want)
	got := make([]string, 0, len(asMap))
	for k := range asMap {
		got = append(got, k)
	}
	sort.Strings(got)

	if len(got) != len(want) {
		t.Fatalf("build-info resource has %d fields %v, want exactly %d %v — this resource must never grow "+
			"an environment/secret field by accident", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("field[%d] = %q, want %q (full got=%v)", i, got[i], want[i], got)
		}
	}

	// Belt-and-suspenders substring check against the dispatch threat model:
	// this resource must never carry a DSN, credential, or connection-string
	// shape even if a future field addition slips past the allowlist above.
	lower := strings.ToLower(raw)
	for _, forbidden := range []string{"postgres://", "postgresql://", "mongodb://", "password", "dsn", "://localhost", "api_key", "apikey"} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("build-info resource contains forbidden substring %q — possible secret/env leak: %s", forbidden, raw)
		}
	}
}

// TestToolsList_BuildInfoResourceNotAdvertisedInToolDescriptions is the
// permanent regression guard for the dispatch's tools/list invariant: the
// new build-info resource must stay out of the core tool surface — no tool
// description may mention its URI or push a client toward calling it as
// though it were a tool. Resources are listed via resources/list, a
// separate, unbudgeted RPC method; this test is what stops a future author
// from "helpfully" mentioning it in a tool description the way
// get_today_context's description already names
// wayneblacktea://session/handoff/latest (which is deliberate, existing,
// pre-dates this PR, and is intentionally excluded from the check below).
func TestToolsList_BuildInfoResourceNotAdvertisedInToolDescriptions(t *testing.T) {
	s := newTestResourceServer(t)
	ms := s.MCPServer()

	resp := ms.HandleMessage(context.Background(), json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal tools/list response: %v", err)
	}

	got := string(raw)
	if strings.Contains(got, "wayneblacktea://system/build-info") {
		t.Error("a tool description mentions wayneblacktea://system/build-info — the resource must stay " +
			"out of the tools/list token budget")
	}
	if strings.Contains(strings.ToLower(got), "build-info") || strings.Contains(strings.ToLower(got), "build_info") {
		t.Error("a tool description references the build-info resource by name — keep it discoverable only " +
			"via resources/list")
	}

	t.Logf("tools/list response size = %d bytes (reproducible measurement — re-run this test after any "+
		"tool-description change to see the new number; see internal/mcp/tools_arch_test.go's "+
		"TestHandleGetProjectArch_ByteSizeMeasurement_ProductionScale doc comment for why an ad-hoc, "+
		"uncommitted scratch measurement is not an acceptable way to report this)", len(got))
}
