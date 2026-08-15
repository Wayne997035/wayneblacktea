package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/arch"
)

// TestHandleGetProjectArch_FileMapOptIn_DefaultOff pins W2: without
// include_file_map, a snapshot with a non-empty file_map must come back with
// no file_map key on the wire at all — not "file_map":null, not
// "file_map":{}. arch.Snapshot.FileMap carries the omitempty json tag
// specifically so a nil map disappears from the wire rather than serializing
// as null.
func TestHandleGetProjectArch_FileMapOptIn_DefaultOff(t *testing.T) {
	s := &Server{arch: fakeArchStore{snap: &arch.Snapshot{
		ID:      "1",
		Slug:    "wayneblacktea",
		Summary: "Echo HTTP + MCP in one binary.",
		FileMap: map[string]string{
			"cmd/server/main.go":     "HTTP+MCP entry point",
			"internal/mcp/server.go": "MCP server wiring",
		},
		LastCommitSHA: "deadbeef",
	}}}

	raw := getProjectArchTextArgs(t, s, map[string]any{"slug": "wayneblacktea"})
	if strings.Contains(raw, "file_map") {
		t.Errorf("include_file_map omitted (default false) must drop file_map entirely, got: %s", raw)
	}
	// The rest of the snapshot must still be present.
	if !strings.Contains(raw, "Echo HTTP") {
		t.Errorf("summary content lost when file_map was gated off: %s", raw)
	}
}

// TestHandleGetProjectArch_FileMapOptIn_ExplicitFalse pins the same contract
// when the caller passes include_file_map=false explicitly rather than
// omitting it, since MCP clients may send either shape.
func TestHandleGetProjectArch_FileMapOptIn_ExplicitFalse(t *testing.T) {
	s := &Server{arch: fakeArchStore{snap: &arch.Snapshot{
		ID: "1", Slug: "wayneblacktea", Summary: "arch",
		FileMap:       map[string]string{"a.go": "purpose"},
		LastCommitSHA: "deadbeef",
	}}}

	raw := getProjectArchTextArgs(t, s, map[string]any{"slug": "wayneblacktea", "include_file_map": false})
	if strings.Contains(raw, "file_map") {
		t.Errorf("include_file_map=false must drop file_map entirely, got: %s", raw)
	}
}

// TestHandleGetProjectArch_FileMapOptIn_True pins the opt-in path: passing
// include_file_map=true returns the full, neutralised file_map — the same
// content the tool always returned before W2 (see
// TestHandleGetProjectArch_FencesUntrustedSummary in boundary_markers_test.go
// for the injection-neutralisation assertions, which reuse this same true
// path via getProjectArchText's default).
func TestHandleGetProjectArch_FileMapOptIn_True(t *testing.T) {
	fileMap := map[string]string{
		"cmd/server/main.go":     "HTTP+MCP entry point",
		"internal/mcp/server.go": "MCP server wiring",
	}
	s := &Server{arch: fakeArchStore{snap: &arch.Snapshot{
		ID: "1", Slug: "wayneblacktea", Summary: "arch",
		FileMap:       fileMap,
		LastCommitSHA: "deadbeef",
	}}}

	raw := getProjectArchTextArgs(t, s, map[string]any{"slug": "wayneblacktea", "include_file_map": true})
	var got arch.Snapshot
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.FileMap) != len(fileMap) {
		t.Fatalf("file_map has %d entries, want %d: %v", len(got.FileMap), len(fileMap), got.FileMap)
	}
	for path, purpose := range fileMap {
		if got.FileMap[path] != purpose {
			t.Errorf("file_map[%q] = %q, want %q", path, got.FileMap[path], purpose)
		}
	}
}

// TestHandleGetProjectArch_FileMapOptIn_ByteSizeDrop measures and pins the
// actual reduction include_file_map=false buys: a new test rather than
// trusting the "3,170 chars / 2,045 for file_map" figure from the dispatch,
// which the spec's self-verification pass could not reproduce from any
// fixture in-repo.
func TestHandleGetProjectArch_FileMapOptIn_ByteSizeDrop(t *testing.T) {
	fileMap := make(map[string]string, 40)
	for i := 0; i < 40; i++ {
		fileMap[archTestFileKey(i)] = archTestFileVal(i)
	}
	s := &Server{arch: fakeArchStore{snap: &arch.Snapshot{
		ID: "1", Slug: "wayneblacktea",
		Summary:       "Echo HTTP + MCP in one binary, dual backend (sqlite / pgx+pgvector).",
		FileMap:       fileMap,
		LastCommitSHA: "deadbeef",
	}}}

	withMap := getProjectArchTextArgs(t, s, map[string]any{"slug": "wayneblacktea", "include_file_map": true})
	withoutMap := getProjectArchTextArgs(t, s, map[string]any{"slug": "wayneblacktea"})

	t.Logf("get_project_arch with file_map (%d entries)    = %d bytes", len(fileMap), len(withMap))
	t.Logf("get_project_arch without file_map (default)    = %d bytes", len(withoutMap))
	if len(withoutMap) >= len(withMap) {
		t.Errorf("include_file_map=false (%d bytes) did not shrink the response versus true (%d bytes)",
			len(withoutMap), len(withMap))
	}
}

// TestHandleGetProjectArch_ByteSizeMeasurement_ProductionScale is the
// committed, reproducible replacement for the PR body's original "8,300 B
// -> 255 B (-96.9%)" get_project_arch headline claim. Review round 1
// (testing-reality-checker) found that number unreproducible: the scratch
// test that produced it (internal/mcp/zz_measure_test.go) was never
// committed anywhere in the repo, and an independent reconstruction against
// the real production row (slug=wayneblacktea, 18 file_map entries, an
// 862-byte summary) run through this exact unmodified handler gave
// ~-65.5%, not -96.9%.
//
// This fixture uses 18 entries specifically to match that real production
// row's file_map count — it is NOT the same fixture as
// TestHandleGetProjectArch_FileMapOptIn_ByteSizeDrop above (40 entries):
// that test is a synthetic worst-case-shape regression guard, this one is
// a best-effort production-scale measurement. The two answer different
// questions and their percentages are not interchangeable; don't quote one
// as if it were the other.
func TestHandleGetProjectArch_ByteSizeMeasurement_ProductionScale(t *testing.T) {
	const productionFileMapEntries = 18
	fileMap := make(map[string]string, productionFileMapEntries)
	for i := 0; i < productionFileMapEntries; i++ {
		fileMap[archTestFileKey(i)] = archTestFileVal(i)
	}

	// Summary length approximates the real production row's 862-byte
	// summary (per PR #157 review round 1, security-engineer / testing-
	// reality-checker reports); the content itself is illustrative
	// architecture prose, not copied production text.
	const productionSummaryBytes = 862
	summary := strings.Repeat(
		"Echo HTTP + MCP server in one Go binary, dual backend (sqlite for local "+
			"dev, pgx+pgvector on Aiven for production), domain-oriented internal "+
			"packages with no service/repository split. ",
		5,
	)
	summary = summary[:productionSummaryBytes]

	s := &Server{arch: fakeArchStore{snap: &arch.Snapshot{
		ID:            "1",
		Slug:          "wayneblacktea",
		Summary:       summary,
		FileMap:       fileMap,
		LastCommitSHA: "deadbeef",
	}}}

	withMap := getProjectArchTextArgs(t, s, map[string]any{"slug": "wayneblacktea", "include_file_map": true})
	withoutMap := getProjectArchTextArgs(t, s, map[string]any{"slug": "wayneblacktea"})

	before, after := len(withMap), len(withoutMap)
	reduction := 100 * float64(before-after) / float64(before)
	t.Logf("get_project_arch, %d-entry production-scale fixture, include_file_map=true  = %d bytes", productionFileMapEntries, before)
	t.Logf("get_project_arch, %d-entry production-scale fixture, include_file_map=false = %d bytes", productionFileMapEntries, after)
	t.Logf("reduction = %.1f%%", reduction)

	if after >= before {
		t.Errorf("include_file_map=false (%d bytes) did not shrink the response versus true (%d bytes)", after, before)
	}
}

func archTestFileKey(i int) string {
	return "internal/pkg" + string(rune('a'+i%26)) + "/file" + string(rune('0'+i%10)) + ".go"
}

func archTestFileVal(i int) string {
	return "handles domain logic for subsystem " + string(rune('a'+i%26)) + ", entry point and store wiring"
}
