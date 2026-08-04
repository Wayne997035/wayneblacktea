package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/contextpack"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
	"github.com/jackc/pgx/v5/pgtype"
)

// captureSlogWarn redirects slog default output to a buffer for the duration
// of the test. The buffer is returned for substring assertions. Restores the
// previous default handler on cleanup. Mirrors internal/guard/config_test.go's
// helper of the same name (package-local copy; not exported cross-package).
func captureSlogWarn(t *testing.T) *bytes.Buffer {
	t.Helper()
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	buf := &bytes.Buffer{}
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	return buf
}

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
// newHookPgxPool — both error branches. Neither reaches pgxpool.NewWithConfig
// (which lazily dials, so this stays a pure unit test with no network/DB
// access): ParseConfig and BuildTLSConfig both fail closed before that call.
// ---------------------------------------------------------------------------

func TestNewHookPgxPool_InvalidDSNReturnsParseError(t *testing.T) {
	_, err := newHookPgxPool(context.Background(), "postgres://user:pass@host:notaport/db")
	if err == nil {
		t.Fatal("newHookPgxPool(invalid dsn) = nil error, want a DSN parse error")
	}
	if !strings.Contains(err.Error(), "parsing DSN") {
		t.Errorf("newHookPgxPool error = %q, want it to wrap %q", err.Error(), "parsing DSN")
	}
}

func TestNewHookPgxPool_MissingPGSSLROOTCERTInProductionReturnsTLSError(t *testing.T) {
	// PGSSLROOTCERT must be genuinely empty (not merely unreadable) for this
	// test: pgxpool.ParseConfig itself consults the PGSSLROOTCERT env var
	// (a standard libpq var) and fails at parse time if it points to an
	// unreadable file — that would exercise the ParseConfig branch above,
	// not storage.BuildTLSConfig's own APP_ENV=production+empty-cert check.
	// An empty value lets ParseConfig succeed (no CA file to validate) so
	// storage.BuildTLSConfig's ErrMissingPGSSLROOTCERT is what actually
	// fails newHookPgxPool.
	t.Setenv("APP_ENV", "production")
	t.Setenv("PGSSLROOTCERT", "")

	_, err := newHookPgxPool(context.Background(), "postgres://user:pass@localhost:5432/db")
	if err == nil {
		t.Fatal("newHookPgxPool(production, no PGSSLROOTCERT) = nil error, want a TLS config error")
	}
	if !strings.Contains(err.Error(), "TLS config") {
		t.Errorf("newHookPgxPool error = %q, want it to wrap %q", err.Error(), "TLS config")
	}
	if !errors.Is(err, storage.ErrMissingPGSSLROOTCERT) {
		t.Errorf("newHookPgxPool error = %v, want it to wrap storage.ErrMissingPGSSLROOTCERT", err)
	}
}

// ---------------------------------------------------------------------------
// latestHandoffOrNil — the two error branches (session.ErrNotFound is silent;
// any other error warns). Every existing test that reaches this function
// first seeds a handoff via SetHandoff, so only the "has handoff" path was
// ever exercised before this pair — review round-2 GTD f9d101db item 1.
// ---------------------------------------------------------------------------

// fakeHandoffStore implements contextpack.SessionReadPort (LatestHandoff is
// its only method) so both error branches can be driven directly without a
// real DB — same narrow-fake pattern as
// internal/contextpack/retrieval_test.go's fakeSessionStore for the same
// port.
type fakeHandoffStore struct {
	err error
}

func (f *fakeHandoffStore) LatestHandoff(_ context.Context) (*db.SessionHandoff, error) {
	return nil, f.err
}

func TestLatestHandoffOrNil_ErrNotFoundReturnsNilWithoutWarning(t *testing.T) {
	buf := captureSlogWarn(t)
	store := &fakeHandoffStore{err: session.ErrNotFound}

	got := latestHandoffOrNil(context.Background(), store)

	if got != nil {
		t.Errorf("latestHandoffOrNil() = %+v, want nil", got)
	}
	if buf.Len() != 0 {
		t.Errorf("latestHandoffOrNil logged a warning for the common no-handoff case:\n%s", buf.String())
	}
}

func TestLatestHandoffOrNil_OtherErrorReturnsNilAndWarns(t *testing.T) {
	buf := captureSlogWarn(t)
	store := &fakeHandoffStore{err: errors.New("db exploded")}

	got := latestHandoffOrNil(context.Background(), store)

	if got != nil {
		t.Errorf("latestHandoffOrNil() = %+v, want nil", got)
	}
	if !strings.Contains(buf.String(), "fetching latest handoff") {
		t.Errorf("latestHandoffOrNil did not warn on a genuine DB error:\n%s", buf.String())
	}
}

// ---------------------------------------------------------------------------
// assembleSessionPack — the err != nil branch. Assemble()'s real
// implementation (internal/contextpack/contextpack.go) never returns a
// non-nil error today; packAssembler exists solely so this otherwise
// unreachable defensive branch can be driven directly — review round-2 GTD
// f9d101db item 3.
// ---------------------------------------------------------------------------

// fakeErrAssembler implements packAssembler and always returns a non-nil
// error.
type fakeErrAssembler struct{}

func (fakeErrAssembler) Assemble(_ context.Context, _ contextpack.Request) (*contextpack.Pack, error) {
	return nil, errors.New("assemble exploded")
}

func TestAssembleSessionPack_AssembleErrorReturnsNilAndWarns(t *testing.T) {
	buf := captureSlogWarn(t)

	got := assembleSessionPack(context.Background(), fakeErrAssembler{}, nil)

	if got != nil {
		t.Errorf("assembleSessionPack() = %+v, want nil on Assemble error", got)
	}
	if !strings.Contains(buf.String(), "Assemble failed") {
		t.Errorf("assembleSessionPack did not warn on Assemble error:\n%s", buf.String())
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

// TestObjectiveFromHandoff_TruncatesLongFields pins the optional 🔵 review
// round-2 GTD dcdbabee item 4 fix: Intent and ContextSummary are each capped
// at handoffFieldTruncateRunes (the same constant context_render.go already
// applies to these two fields when rendering) before being concatenated into
// Objective, so an attacker-controlled handoff row can no longer send an
// unbounded string into a future non-SQLite retrieval port that might not
// truncate on its own.
func TestObjectiveFromHandoff_TruncatesLongFields(t *testing.T) {
	longIntent := strings.Repeat("i", handoffFieldTruncateRunes+50)
	longSummary := strings.Repeat("s", handoffFieldTruncateRunes+50)
	h := &db.SessionHandoff{
		Intent:         longIntent,
		ContextSummary: pgtype.Text{String: longSummary, Valid: true},
	}

	got := objectiveFromHandoff(h)
	parts := strings.SplitN(got, " ", 2)
	if len(parts) != 2 {
		t.Fatalf("objectiveFromHandoff() = %q, want two space-separated truncated fields", got)
	}
	wantFieldLen := handoffFieldTruncateRunes + 1 // truncated body + "…" ellipsis
	if got := len([]rune(parts[0])); got != wantFieldLen {
		t.Errorf("truncated Intent rune length = %d, want %d", got, wantFieldLen)
	}
	if got := len([]rune(parts[1])); got != wantFieldLen {
		t.Errorf("truncated ContextSummary rune length = %d, want %d", got, wantFieldLen)
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
