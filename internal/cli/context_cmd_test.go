package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/contextpack"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestTruncateUnicodeBoundary(t *testing.T) {
	input := "我愛台灣"
	tests := []struct {
		name   string
		maxLen int
		want   string
	}{
		{name: "maxLen minus one", maxLen: 3, want: "我愛台…"},
		{name: "exact rune length", maxLen: 4, want: input},
		{name: "greater than rune length", maxLen: 5, want: input},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := truncate(input, tc.maxLen); got != tc.want {
				t.Fatalf("truncate(%q, %d) = %q, want %q", input, tc.maxLen, got, tc.want)
			}
		})
	}
}

func TestDsnFromFallbackReadsTempEnvFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".wayneblacktea")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	envPath := filepath.Join(configDir, ".env.local")
	wantDSN := "postgres://u:" + "p" + "@host/db"
	if err := os.WriteFile(envPath, []byte("OTHER=x\nDATABASE_URL="+wantDSN+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if got := DSNFromFallback(); got != wantDSN {
		t.Fatalf("DSNFromFallback() = %q", got)
	}
}

func TestEmitContextAlwaysValidJSON(t *testing.T) {
	stdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() {
		os.Stdout = stdout
		_ = r.Close()
	})

	EmitContext("hello\nworld")
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	var out SessionStartOutput
	if err := json.NewDecoder(r).Decode(&out); err != nil {
		t.Fatalf("Decode emitted JSON: %v", err)
	}
	if out.SystemMessage != "hello\nworld" {
		t.Fatalf("SystemMessage = %q", out.SystemMessage)
	}
}

// ---------------------------------------------------------------------------
// objectiveFromHandoff
// ---------------------------------------------------------------------------

func TestObjectiveFromHandoff(t *testing.T) {
	if got := objectiveFromHandoff(nil); got != "" {
		t.Errorf("objectiveFromHandoff(nil) = %q, want empty", got)
	}

	h := &db.SessionHandoff{Intent: "ship P1", ContextSummary: pgtype.Text{String: "9 issues found", Valid: true}}
	if got, want := objectiveFromHandoff(h), "ship P1 9 issues found"; got != want {
		t.Errorf("objectiveFromHandoff() = %q, want %q", got, want)
	}

	noSummary := &db.SessionHandoff{Intent: "ship P1"}
	if got, want := objectiveFromHandoff(noSummary), "ship P1"; got != want {
		t.Errorf("objectiveFromHandoff(no ContextSummary) = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// renderSessionContext + the four section renderers
// ---------------------------------------------------------------------------

func TestRenderSessionContext_EmptyEverythingReturnsEmptyString(t *testing.T) {
	if got := renderSessionContext(nil, nil, nil); got != "" {
		t.Errorf("renderSessionContext(nil, nil, nil) = %q, want empty string", got)
	}
	emptyPack := &contextpack.Pack{}
	if got := renderSessionContext(emptyPack, nil, nil); got != "" {
		t.Errorf("renderSessionContext(empty pack, nil, nil) = %q, want empty string", got)
	}
}

func TestRenderSessionContext_NonEmptyOutputWrappedInUntrustedDBBoundary(t *testing.T) {
	handoff := &db.SessionHandoff{Intent: "resume work"}
	got := renderSessionContext(nil, handoff, nil)
	if !strings.HasPrefix(got, untrustedLocalDBWrapStart) {
		t.Errorf("renderSessionContext output does not start with the untrusted-local-db boundary marker:\n%s", got)
	}
	if !strings.HasSuffix(got, untrustedLocalDBWrapEnd) {
		t.Errorf("renderSessionContext output does not end with the untrusted-local-db boundary marker:\n%s", got)
	}
}

func TestRenderSessionContext_UsesContextSummaryNotSummaryText(t *testing.T) {
	// A5a behaviour change #3: display field is ContextSummary, not
	// SummaryText (the async Stop-hook-summarizer-only field). Setting
	// SummaryText and leaving ContextSummary unset must render nothing for
	// the "Context:" line — proving SummaryText is never read here.
	handoff := &db.SessionHandoff{
		Intent:      "resume work",
		SummaryText: pgtype.Text{String: "must not appear", Valid: true},
	}
	got := renderSessionContext(nil, handoff, nil)
	if strings.Contains(got, "must not appear") {
		t.Errorf("renderSessionContext rendered SummaryText content; want only ContextSummary ever surfaced:\n%s", got)
	}

	withSummary := &db.SessionHandoff{
		Intent:         "resume work",
		ContextSummary: pgtype.Text{String: "context summary text", Valid: true},
	}
	got2 := renderSessionContext(nil, withSummary, nil)
	if !strings.Contains(got2, "context summary text") {
		t.Errorf("renderSessionContext did not render ContextSummary:\n%s", got2)
	}
}

func TestRenderDecisionsSection_PreservesInputOrder(t *testing.T) {
	// pack.Items arrives already score-sorted (Assemble()'s job); the
	// renderer must not re-sort — it only filters. Feeding items in a
	// deliberately non-alphabetical order and asserting the SAME order comes
	// back out proves no incidental re-sort happened (A5a behaviour change #1:
	// "Recent decisions" is score-order, not recency-order — this renderer's
	// only contract is to preserve whatever order Assemble already produced).
	items := []contextpack.Item{
		{Type: contextpack.TypeDecision, Summary: "second decision"},
		{Type: contextpack.TypeKnowledge, Summary: "unrelated knowledge item"},
		{Type: contextpack.TypeDecision, Summary: "first decision"},
	}
	got := renderDecisionsSection(items)
	want := "- second decision\n- first decision"
	if got != want {
		t.Errorf("renderDecisionsSection() = %q, want %q", got, want)
	}
}

func TestRenderRelevantContextSection_ExcludesDecisionsAndHandoffDuplicate(t *testing.T) {
	items := []contextpack.Item{
		{Type: contextpack.TypeDecision, Summary: "should be excluded (own section)"},
		{
			Type: contextpack.TypeSession, Summary: "should be excluded (duplicates handoff section)",
			Provenance: map[string]string{"source_table": "session_handoffs"},
		},
		{
			Type: contextpack.TypeSession, Summary: "work session, not a handoff",
			Provenance: map[string]string{"source_table": "work_sessions"},
		},
		{Type: contextpack.TypeKnowledge, Summary: "a knowledge item"},
		{Type: contextpack.TypeTask, Summary: "a current task"},
	}
	got := renderRelevantContextSection(items)

	if strings.Contains(got, "should be excluded") {
		t.Errorf("renderRelevantContextSection leaked an excluded item:\n%s", got)
	}
	for _, want := range []string{"[session] work session, not a handoff", "[knowledge] a knowledge item", "[task] a current task"} {
		if !strings.Contains(got, want) {
			t.Errorf("renderRelevantContextSection missing %q in:\n%s", want, got)
		}
	}
}

func TestRenderDueReviewsSection_TruncatesLongTitles(t *testing.T) {
	long := strings.Repeat("x", dueReviewTitleTruncateRunes+50)
	got := renderDueReviewsSection([]string{"short title", long})
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("renderDueReviewsSection lines = %d, want 2", len(lines))
	}
	if lines[0] != "- short title" {
		t.Errorf("lines[0] = %q, want %q", lines[0], "- short title")
	}
	// "- " prefix + truncated title + "…" ellipsis.
	wantLen := 2 + dueReviewTitleTruncateRunes + 1
	if got := len([]rune(lines[1])); got != wantLen {
		t.Errorf("truncated line rune length = %d, want %d", got, wantLen)
	}
}

// ---------------------------------------------------------------------------
// hook path must never construct a knowledge.Store with a live embed client
// (A5a dispatch §5 — no network calls from this hook). Verified directly
// against the unexported "embed" field via reflection (IsNil does not
// require Interface(), so it works on an unexported field without panicking)
// rather than a live network probe, which would be non-deterministic /
// require network access in CI.
// ---------------------------------------------------------------------------

func TestHookKnowledgeStoreConstruction_HasNilEmbedClient(t *testing.T) {
	// Exercises the exact construction expression runSessionStartPostgres
	// uses: knowledge.NewStore(pool, nil, wsID).
	store := knowledge.NewStore(nil, nil, nil)
	embedField := reflect.ValueOf(store).Elem().FieldByName("embed")
	if !embedField.IsValid() {
		t.Fatal(`knowledge.Store has no "embed" field (internal layout changed) — update this test`)
	}
	if !embedField.IsNil() {
		t.Error("knowledge.Store.embed is non-nil for the SessionStart hook's construction call; " +
			"SearchReadOnly would attempt a network embedding call")
	}
}
