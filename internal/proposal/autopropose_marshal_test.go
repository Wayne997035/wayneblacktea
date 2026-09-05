package proposal_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

// [F0902-51] [F0902-52] This file mirrors store_payload_cap_test.go's
// convention (package proposal_test, testcontainers PG via
// openBatchTestPgPool defined in batch_confirm_postgres_test.go) and covers
// the density-regression and payload-cap tests for
// proposal.MarshalConceptCandidate — backend-security-design.md §2.1 ("LLM
// tool input is hostile"): a knowledge item's Content field is user/LLM
// controlled and up to 1 MiB; before [F0902-51] its HTML-density alone
// could push a legitimate item's concept proposal past MaxPayloadBytes.
//
// Fixture shape reused across these tests — Title/SourceItemID/
// SourceItemType match the exact values used to independently measure the
// struct's fixed JSON envelope overhead (133 bytes)
// and re-verified against the actual MarshalConceptCandidate implementation.
const (
	fixtureTitle          = "some knowledge item title"
	fixtureSourceItemID   = "00000000-0000-0000-0000-000000000000"
	fixtureSourceItemType = "article"
	oneMiB                = 1 << 20 // 1,048,576 bytes
)

// jsonEscapeSequence builds the literal N-character ASCII text of a
// encoding/json Unicode escape sequence (e.g. backslash, u, 0, 0, 3, c) from
// individual bytes instead of a string literal containing the sequence
// itself — this is deliberate, not decorative: a source string literal
// containing this exact escape text is indistinguishable, to some tooling in
// this environment, from the actual control character it denotes, and gets
// silently substituted for it. Building it from a byte slice guarantees the
// test asserts against the real 6-byte escape text encoding/json emits.
func jsonEscapeSequence(hex4 string) []byte {
	b := []byte{'\\', 'u'}
	b = append(b, hex4...)
	return b
}

// TestMarshalConceptCandidate_NoHTMLEscape — [F0902-51] spec row 1
// (Acceptance table row 1).
func TestMarshalConceptCandidate_NoHTMLEscape(t *testing.T) {
	c := proposal.ConceptCandidate{Title: "t", Content: `<b>&"hi"</b>`}
	got, err := proposal.MarshalConceptCandidate(c)
	if err != nil {
		t.Fatalf("MarshalConceptCandidate: %v", err)
	}
	// `"` inside a JSON string value is always escaped as \" regardless of
	// SetEscapeHTML (mandatory JSON string-escaping, unrelated to this fix
	// — see MarshalConceptCandidate's doc comment on residual risk), so the
	// positive assertion checks the `<`/`>`/`&`-bearing substrings around
	// the (still-escaped) quotes rather than the whole fixture verbatim.
	if !bytes.Contains(got, []byte(`<b>&`)) || !bytes.Contains(got, []byte(`</b>`)) {
		t.Fatalf("output %s does not contain literal <b>&...</b> — SetEscapeHTML(false) not applied", got)
	}
	if got[len(got)-1] != '}' {
		t.Fatalf("last byte = %q, want '}' — Encoder.Encode's trailing newline was not trimmed", got[len(got)-1])
	}
	// Regression guard: if SetEscapeHTML(false) is missing/reverted, `<`
	// becomes the 6-byte escape sequence built by jsonEscapeSequence("003c")
	// instead of the literal byte. A future accidental revert to plain
	// json.Marshal or a default Encoder must fail this assertion.
	if bytes.Contains(got, jsonEscapeSequence("003c")) {
		t.Errorf("output %s contains the HTML-escaped form of '<' — SetEscapeHTML(false) is not in effect", got)
	}
}

// TestMarshalConceptCandidate_DensityRegression — [F0902-52] spec row 2.
// 1 MiB content, exactly 20% '<' density (209715 '<' + 838861 'a', reusing
// the exact construction independently verified). Pins both: (a) the pre-fix failure this
// ticket closes — plain json.Marshal exceeds MaxPayloadBytes at this
// density, a permanent red-before-fix regression guard that stays green
// forever since it only checks plain json.Marshal's own (unmodified)
// behavior — and (b) the fix itself: MarshalConceptCandidate stays within
// the cap for the identical content.
func TestMarshalConceptCandidate_DensityRegression(t *testing.T) {
	c := proposal.ConceptCandidate{
		Title:          fixtureTitle,
		Content:        strings.Repeat("<", 209715) + strings.Repeat("a", 838861),
		SourceItemID:   fixtureSourceItemID,
		SourceItemType: fixtureSourceItemType,
	}
	if len(c.Content) != oneMiB {
		t.Fatalf("fixture content length = %d, want %d (1 MiB)", len(c.Content), oneMiB)
	}

	plain, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	const wantPlainLen = 2097286
	if len(plain) != wantPlainLen {
		t.Fatalf("len(json.Marshal(c)) = %d, want %d (STRUCT 20%% '<' density measurement, "+
			"known-facts #2 of the sprint dispatch)", len(plain), wantPlainLen)
	}
	if len(plain) <= proposal.MaxPayloadBytes {
		t.Fatalf("plain json.Marshal at 20%% '<' density = %d bytes, want > MaxPayloadBytes (%d) — "+
			"this is the pre-fix failure this ticket closes; if it no longer exceeds the cap, the "+
			"regression this test guards against has changed shape", len(plain), proposal.MaxPayloadBytes)
	}

	got, err := proposal.MarshalConceptCandidate(c)
	if err != nil {
		t.Fatalf("MarshalConceptCandidate: %v", err)
	}
	const wantHelperMaxLen = 1048711
	if len(got) > wantHelperMaxLen {
		t.Fatalf("len(MarshalConceptCandidate(c)) = %d, want <= %d", len(got), wantHelperMaxLen)
	}
	if len(got) > proposal.MaxPayloadBytes {
		t.Fatalf("len(MarshalConceptCandidate(c)) = %d exceeds MaxPayloadBytes (%d) — fix did not close the gap",
			len(got), proposal.MaxPayloadBytes)
	}
}

// TestMarshalConceptCandidate_FullDensityRegression — [F0902-52] "100% '<'
// helper test (red before the fix, green after)". Same structural
// assertions as TestMarshalConceptCandidate_DensityRegression at the most
// extreme end of the density spectrum: content is 1 MiB of nothing but '<'.
// Independently confirmed red against a MarshalConceptCandidate stub that
// omits SetEscapeHTML(false) (matching pre-fix behavior) — see this
// ticket's done-record for the captured failing output — and green against
// the shipped implementation.
func TestMarshalConceptCandidate_FullDensityRegression(t *testing.T) {
	c := proposal.ConceptCandidate{
		Title:          fixtureTitle,
		Content:        strings.Repeat("<", oneMiB),
		SourceItemID:   fixtureSourceItemID,
		SourceItemType: fixtureSourceItemType,
	}

	plain, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	const wantPlainLen = 6291591
	if len(plain) != wantPlainLen {
		t.Fatalf("len(json.Marshal(c)) = %d, want %d (STRUCT 100%% '<' density measurement)", len(plain), wantPlainLen)
	}
	if len(plain) <= proposal.MaxPayloadBytes {
		t.Fatalf("plain json.Marshal at 100%% '<' density = %d bytes, want > MaxPayloadBytes (%d)",
			len(plain), proposal.MaxPayloadBytes)
	}

	got, err := proposal.MarshalConceptCandidate(c)
	if err != nil {
		t.Fatalf("MarshalConceptCandidate: %v", err)
	}
	const wantHelperLen = 1048711
	if len(got) != wantHelperLen {
		t.Fatalf("len(MarshalConceptCandidate(c)) = %d, want %d", len(got), wantHelperLen)
	}
	if len(got) > proposal.MaxPayloadBytes {
		t.Fatalf("len(MarshalConceptCandidate(c)) = %d exceeds MaxPayloadBytes (%d)", len(got), proposal.MaxPayloadBytes)
	}
}

// TestMarshalConceptCandidate_HalfQuoteDensityStaysUnderCap — [F0902-52]
// pins the residual worst case documented in MarshalConceptCandidate's doc
// comment: '"' still costs 2 bytes per character after the fix (mandatory
// JSON string-escaping, independent of SetEscapeHTML), but at 50% density a
// 1 MiB content field still stays under MaxPayloadBytes. Contrast with
// TestMarshalConceptCandidate_FullQuoteDensityExceedsCap (100% density does
// not).
func TestMarshalConceptCandidate_HalfQuoteDensityStaysUnderCap(t *testing.T) {
	k := oneMiB / 2
	c := proposal.ConceptCandidate{
		Title:          fixtureTitle,
		Content:        strings.Repeat(`"`, k) + strings.Repeat("a", oneMiB-k),
		SourceItemID:   fixtureSourceItemID,
		SourceItemType: fixtureSourceItemType,
	}
	got, err := proposal.MarshalConceptCandidate(c)
	if err != nil {
		t.Fatalf("MarshalConceptCandidate: %v", err)
	}
	const wantLen = 1572999
	if len(got) != wantLen {
		t.Fatalf("len(MarshalConceptCandidate(c)) = %d, want %d (STRUCT 50%% '\"' density measurement)", len(got), wantLen)
	}
	if len(got) > proposal.MaxPayloadBytes {
		t.Errorf("50%% '\"' density content = %d bytes, exceeds MaxPayloadBytes (%d) — expected to stay under cap",
			len(got), proposal.MaxPayloadBytes)
	}
}

// TestMarshalConceptCandidate_FullQuoteDensityExceedsCap documents (with a
// pinned, passing assertion rather than only prose) the accepted residual
// gap: a 1 MiB content field that is entirely literal '"' characters still
// exceeds MaxPayloadBytes after this fix, because '"' escaping is mandatory
// JSON string-escaping independent of SetEscapeHTML. This is the "Exception
// path" the spec's User journeys section describes as a known, accepted,
// not-currently-observed shape (no producer generates this today) — this
// test is not required to change AutoProposeConceptFromKnowledge's
// behavior, only to pin the byte count so the claim in
// internal/handler/knowledge_payload_cap_invariant_test.go's comment stays
// verifiable rather than only asserted in prose.
func TestMarshalConceptCandidate_FullQuoteDensityExceedsCap(t *testing.T) {
	c := proposal.ConceptCandidate{
		Title:          fixtureTitle,
		Content:        strings.Repeat(`"`, oneMiB),
		SourceItemID:   fixtureSourceItemID,
		SourceItemType: fixtureSourceItemType,
	}
	got, err := proposal.MarshalConceptCandidate(c)
	if err != nil {
		t.Fatalf("MarshalConceptCandidate: %v", err)
	}
	const wantLen = 2097287 // MaxPayloadBytes (2,097,152) + 135
	if len(got) != wantLen {
		t.Fatalf("len(MarshalConceptCandidate(c)) = %d, want %d", len(got), wantLen)
	}
	if len(got) <= proposal.MaxPayloadBytes {
		t.Fatalf("len(MarshalConceptCandidate(c)) = %d, want > MaxPayloadBytes (%d) — this test documents "+
			"the accepted residual gap; if it no longer exceeds the cap the gap has closed and this test "+
			"(and the invariant-test comment referencing it) should be revisited", len(got), proposal.MaxPayloadBytes)
	}
}

// TestAutoProposeConceptFromKnowledge_PayloadCap_PG — [F0902-52] spec row 3,
// Postgres half. Boundaries for this ticket [F0902-52]
// scope engineer A to internal/proposal/autopropose.go,
// internal/storage/sqlite/proposal.go (one call-site line only), and this
// package's test files — no new file in internal/storage/sqlite is in
// scope. Both backends are still exercised: this package's proposal_test
// external test package can import internal/storage/sqlite (no import
// cycle — sqlite depends on proposal, not on proposal_test) and drive
// sqlite.ProposalStore.AutoProposeConceptFromKnowledge directly, so the
// SQLite half below stays within the file boundaries while still meeting
// spec row 3's "against both proposal.Store ... and sqlite.ProposalStore"
// requirement.
func TestAutoProposeConceptFromKnowledge_PayloadCap_PG(t *testing.T) {
	pool := openBatchTestPgPool(t)
	wsID := uuid.New()
	store := proposal.NewStore(pool, &wsID)
	ctx := context.Background()

	t.Run("20% density succeeds", func(t *testing.T) {
		item := &db.KnowledgeItem{
			ID:      uuid.New(),
			Type:    "til",
			Title:   "t",
			Content: strings.Repeat("<", 209715) + strings.Repeat("a", 838861),
		}
		got, err := store.AutoProposeConceptFromKnowledge(ctx, item, "test")
		if err != nil {
			t.Fatalf("AutoProposeConceptFromKnowledge: unexpected error: %v", err)
		}
		if got == nil {
			t.Fatal("AutoProposeConceptFromKnowledge returned nil proposal, want non-nil")
		}
		t.Cleanup(func() {
			_, cleanErr := pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = $1", got.ID)
			if cleanErr != nil {
				t.Logf("cleanup proposal: %v", cleanErr)
			}
		})
	})

	t.Run("content sized past cap even after fix still rejected", func(t *testing.T) {
		item := &db.KnowledgeItem{
			ID:      uuid.New(),
			Type:    "til",
			Title:   "t",
			Content: strings.Repeat("a", proposal.MaxPayloadBytes+1024),
		}
		got, err := store.AutoProposeConceptFromKnowledge(ctx, item, "test")
		if !errors.Is(err, proposal.ErrPayloadTooLarge) {
			t.Fatalf("AutoProposeConceptFromKnowledge error = %v, want ErrPayloadTooLarge", err)
		}
		if got != nil {
			t.Errorf("AutoProposeConceptFromKnowledge returned non-nil proposal alongside ErrPayloadTooLarge: %+v", got)
		}
	})
}

// TestAutoProposeConceptFromKnowledge_PayloadCap_SQLite — [F0902-52] spec
// row 3, SQLite half. See TestAutoProposeConceptFromKnowledge_PayloadCap_PG's
// doc comment for why this lives here instead of a new file in
// internal/storage/sqlite.
func TestAutoProposeConceptFromKnowledge_PayloadCap_SQLite(t *testing.T) {
	d, err := sqlite.Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	store := sqlite.NewProposalStore(d)
	ctx := context.Background()

	t.Run("20% density succeeds", func(t *testing.T) {
		item := &db.KnowledgeItem{
			ID:      uuid.New(),
			Type:    "til",
			Title:   "t",
			Content: strings.Repeat("<", 209715) + strings.Repeat("a", 838861),
		}
		got, err := store.AutoProposeConceptFromKnowledge(ctx, item, "test")
		if err != nil {
			t.Fatalf("AutoProposeConceptFromKnowledge: unexpected error: %v", err)
		}
		if got == nil {
			t.Fatal("AutoProposeConceptFromKnowledge returned nil proposal, want non-nil")
		}
	})

	t.Run("content sized past cap even after fix still rejected", func(t *testing.T) {
		item := &db.KnowledgeItem{
			ID:      uuid.New(),
			Type:    "til",
			Title:   "t",
			Content: strings.Repeat("a", proposal.MaxPayloadBytes+1024),
		}
		got, err := store.AutoProposeConceptFromKnowledge(ctx, item, "test")
		if !errors.Is(err, proposal.ErrPayloadTooLarge) {
			t.Fatalf("AutoProposeConceptFromKnowledge error = %v, want ErrPayloadTooLarge", err)
		}
		if got != nil {
			t.Errorf("AutoProposeConceptFromKnowledge returned non-nil proposal alongside ErrPayloadTooLarge: %+v", got)
		}
	})
}
