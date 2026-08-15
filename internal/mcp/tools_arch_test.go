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

func archTestFileKey(i int) string {
	return "internal/pkg" + string(rune('a'+i%26)) + "/file" + string(rune('0'+i%10)) + ".go"
}

func archTestFileVal(i int) string {
	return "handles domain logic for subsystem " + string(rune('a'+i%26)) + ", entry point and store wiring"
}
