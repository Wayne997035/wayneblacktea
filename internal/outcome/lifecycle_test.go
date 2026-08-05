package outcome

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestIsIdempotentReplay covers the pure comparison helper RecordExecutionResult
// uses to decide ActionReplayedIdempotent vs ActionSuperseded.
func TestIsIdempotentReplay(t *testing.T) {
	entityID := uuid.New()
	base := Outcome{
		EntityType: "task",
		EntityID:   entityID,
		Result:     "success",
		Notes:      "done on time",
		Metrics:    []byte(`{"duration_ms":100}`),
	}

	tests := []struct {
		name   string
		latest Outcome
		params CreateOutcomeParams
		want   bool
	}{
		{
			name:   "identical result, notes, and metrics",
			latest: base,
			params: CreateOutcomeParams{
				Result:  "success",
				Notes:   "done on time",
				Metrics: []byte(`{"duration_ms":100}`),
			},
			want: true,
		},
		{
			name:   "different result",
			latest: base,
			params: CreateOutcomeParams{
				Result:  "failure",
				Notes:   "done on time",
				Metrics: []byte(`{"duration_ms":100}`),
			},
			want: false,
		},
		{
			name:   "different notes",
			latest: base,
			params: CreateOutcomeParams{
				Result:  "success",
				Notes:   "done late",
				Metrics: []byte(`{"duration_ms":100}`),
			},
			want: false,
		},
		{
			name:   "different metrics bytes",
			latest: base,
			params: CreateOutcomeParams{
				Result:  "success",
				Notes:   "done on time",
				Metrics: []byte(`{"duration_ms":200}`),
			},
			want: false,
		},
		{
			name: "both empty metrics and notes match",
			latest: Outcome{
				Result: "unknown",
			},
			params: CreateOutcomeParams{
				Result: "unknown",
			},
			want: true,
		},
		{
			name: "nil metrics vs empty-but-non-nil metrics still match (bytes.Equal)",
			latest: Outcome{
				Result:  "success",
				Metrics: nil,
			},
			params: CreateOutcomeParams{
				Result:  "success",
				Metrics: []byte{},
			},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isIdempotentReplay(tc.latest, tc.params)
			if got != tc.want {
				t.Errorf("isIdempotentReplay() = %v, want %v", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// RecordExecutionResult — M-1 (PR #152 second army): a fast, deterministic
// spy-backed unit test of the ActionDraftPreserved branch, complementing the
// real-DB SQLite (internal/mcp/tools_outcome_lifecycle_test.go) and Postgres
// (store_test.go) coverage of the same fix. RecordExecutionResult is pure
// orchestration against StoreIface with no SQL of its own (see its doc
// comment), so a fake implementing the interface is sufficient to prove the
// branch never reaches FinalizeDraft/CreateOutcome — the two write paths
// that could otherwise erase or duplicate the draft.
// ---------------------------------------------------------------------------

// fakeStore is a minimal StoreIface spy: GetLatestForEntity returns a
// pre-seeded latest outcome (or ErrNotFound), and FinalizeDraft/CreateOutcome
// record whether they were called so tests can assert M-1's guard actually
// short-circuits before either write path. Methods beyond the three
// RecordExecutionResult composes fail the test loudly if ever called — this
// function has no business touching them, and a future edit that
// accidentally does so should be caught immediately rather than silently
// returning a zero value.
type fakeStore struct {
	t *testing.T

	hasLatest bool
	latest    Outcome

	finalizeCalled bool
	createCalled   bool
}

func (f *fakeStore) GetLatestForEntity(context.Context, *uuid.UUID, string, uuid.UUID) (Outcome, error) {
	if !f.hasLatest {
		return Outcome{}, ErrNotFound
	}
	return f.latest, nil
}

// FinalizeDraft mirrors the real stores' APPEND-ONLY merge semantics
// (migration 000075 — see store.go / storage/sqlite/outcome.go's
// FinalizeDraft doc comments for the authoritative per-field rule), not a
// blind overwrite: Result is always written; Notes appends (direct write
// only when existing is empty); Metrics only adds keys absent from the
// existing value; RelatedRuleIDs unions (dedup, existing elements first);
// WorkSessionID writes only when the existing value is nil. Getting this
// right matters beyond the original M-2a tests below — the draft-retry
// idempotency tests (PR #152 round 3 Major, and the append-semantics tests
// added alongside migration 000075) issue a SECOND FinalizeDraft-eligible
// call against the result of the first and assert on what actually got
// merged, so a fake that didn't mirror append semantics precisely could
// silently pass tests that should fail (or vice versa).
func (f *fakeStore) FinalizeDraft(_ context.Context, _ uuid.UUID, params CreateOutcomeParams) (Outcome, error) {
	f.finalizeCalled = true
	f.latest.Result = params.Result
	if params.Notes != "" {
		if f.latest.Notes == "" {
			f.latest.Notes = params.Notes
		} else {
			f.latest.Notes = f.latest.Notes + "\n\n" + params.Notes
		}
	}
	if len(params.Metrics) > 0 {
		var incoming map[string]json.RawMessage
		if err := json.Unmarshal(params.Metrics, &incoming); err == nil {
			merged := make(map[string]json.RawMessage, len(incoming))
			for k, v := range incoming {
				merged[k] = v
			}
			if len(f.latest.Metrics) > 0 {
				var existing map[string]json.RawMessage
				if err := json.Unmarshal(f.latest.Metrics, &existing); err == nil {
					for k, v := range existing {
						merged[k] = v // existing wins on conflicting keys
					}
				}
			}
			if out, err := json.Marshal(merged); err == nil {
				f.latest.Metrics = out
			}
		}
	}
	if len(params.RelatedRuleIDs) > 0 {
		seen := make(map[uuid.UUID]bool, len(f.latest.RelatedRuleIDs)+len(params.RelatedRuleIDs))
		merged := make([]uuid.UUID, 0, len(f.latest.RelatedRuleIDs)+len(params.RelatedRuleIDs))
		for _, id := range f.latest.RelatedRuleIDs {
			if !seen[id] {
				seen[id] = true
				merged = append(merged, id)
			}
		}
		for _, id := range params.RelatedRuleIDs {
			if !seen[id] {
				seen[id] = true
				merged = append(merged, id)
			}
		}
		f.latest.RelatedRuleIDs = merged
	}
	if params.WorkSessionID != nil && f.latest.WorkSessionID == nil {
		f.latest.WorkSessionID = params.WorkSessionID
	}
	return f.latest, nil
}

func (f *fakeStore) CreateOutcome(_ context.Context, params CreateOutcomeParams) (Outcome, error) {
	f.createCalled = true
	return Outcome{
		ID:             uuid.New(),
		EntityType:     params.EntityType,
		EntityID:       params.EntityID,
		Result:         params.Result,
		Notes:          params.Notes,
		Metrics:        params.Metrics,
		RelatedRuleIDs: params.RelatedRuleIDs,
		WorkSessionID:  params.WorkSessionID,
		SupersedesID:   params.SupersedesID,
	}, nil
}

func (f *fakeStore) GetOutcomeByID(context.Context, uuid.UUID, *uuid.UUID) (Outcome, error) {
	f.t.Fatal("fakeStore.GetOutcomeByID: unexpected call — RecordExecutionResult does not use this method")
	return Outcome{}, nil
}

func (f *fakeStore) ListRecentOutcomes(context.Context, *uuid.UUID, string, int) ([]Outcome, error) {
	f.t.Fatal("fakeStore.ListRecentOutcomes: unexpected call — RecordExecutionResult does not use this method")
	return nil, nil
}

func (f *fakeStore) CreateEvaluation(context.Context, CreateEvaluationParams) (Evaluation, error) {
	f.t.Fatal("fakeStore.CreateEvaluation: unexpected call — RecordExecutionResult does not use this method")
	return Evaluation{}, nil
}

func (f *fakeStore) ListEvaluationsByOutcomeID(context.Context, uuid.UUID, *uuid.UUID) ([]Evaluation, error) {
	f.t.Fatal("fakeStore.ListEvaluationsByOutcomeID: unexpected call — RecordExecutionResult does not use this method")
	return nil, nil
}

func (f *fakeStore) ListFailedOutcomes(context.Context, *uuid.UUID, int) ([]Outcome, error) {
	f.t.Fatal("fakeStore.ListFailedOutcomes: unexpected call — RecordExecutionResult does not use this method")
	return nil, nil
}

func (f *fakeStore) PruneOlderThan(context.Context, time.Time) (int64, error) {
	f.t.Fatal("fakeStore.PruneOlderThan: unexpected call — RecordExecutionResult does not use this method")
	return 0, nil
}

func (f *fakeStore) ExistsForEntity(context.Context, *uuid.UUID, string, uuid.UUID) (bool, error) {
	f.t.Fatal("fakeStore.ExistsForEntity: unexpected call — RecordExecutionResult does not use this method")
	return false, nil
}

func (f *fakeStore) SeedDraft(context.Context, *uuid.UUID, string, uuid.UUID) (Outcome, bool, error) {
	f.t.Fatal("fakeStore.SeedDraft: unexpected call — RecordExecutionResult does not use this method")
	return Outcome{}, false, nil
}

var _ StoreIface = (*fakeStore)(nil)

// TestRecordExecutionResult_UnknownAgainstExistingDraft_PreservesContent is
// the M-1 attack reproduction: a call carrying result="unknown" (exactly
// what a prompt-injected record_outcome(result="unknown") call would send)
// against an entity that already has an 'unknown' draft with real content
// (notes, metrics, related_rule_ids, work_session_id) must not erase that
// content — no write should occur at all.
func TestRecordExecutionResult_UnknownAgainstExistingDraft_PreservesContent(t *testing.T) {
	entityID := uuid.New()
	draftID := uuid.New()
	relatedRule := uuid.New()
	sessionID := uuid.New()

	fs := &fakeStore{
		t:         t,
		hasLatest: true,
		latest: Outcome{
			ID:             draftID,
			EntityType:     "task",
			EntityID:       entityID,
			Result:         "unknown",
			Notes:          "real postmortem content the attacker wants gone",
			Metrics:        []byte(`{"duration_ms":4200}`),
			RelatedRuleIDs: []uuid.UUID{relatedRule},
			WorkSessionID:  &sessionID,
		},
	}

	got, action, err := RecordExecutionResult(context.Background(), fs, CreateOutcomeParams{
		EntityType: "task",
		EntityID:   entityID,
		Result:     "unknown",
		// Notes/Metrics/RelatedRuleIDs/WorkSessionID deliberately left empty —
		// this is exactly what a minimal prompt-injected call would send.
	})
	if err != nil {
		t.Fatalf("RecordExecutionResult: %v", err)
	}
	if action != ActionDraftPreserved {
		t.Errorf("action = %q, want %q", action, ActionDraftPreserved)
	}
	if fs.finalizeCalled {
		t.Error("FinalizeDraft must NOT be called for an unknown-against-unknown call (M-1 regression)")
	}
	if fs.createCalled {
		t.Error("CreateOutcome must NOT be called for an unknown-against-unknown call")
	}
	if got.ID != draftID {
		t.Errorf("returned ID = %s, want the untouched draft ID %s", got.ID, draftID)
	}
	if got.Notes != "real postmortem content the attacker wants gone" {
		t.Errorf("Notes = %q, want unchanged (M-1 regression: draft content erased)", got.Notes)
	}
	if string(got.Metrics) != `{"duration_ms":4200}` {
		t.Errorf("Metrics = %q, want unchanged", got.Metrics)
	}
	if len(got.RelatedRuleIDs) != 1 || got.RelatedRuleIDs[0] != relatedRule {
		t.Errorf("RelatedRuleIDs = %v, want unchanged [%s]", got.RelatedRuleIDs, relatedRule)
	}
	if got.WorkSessionID == nil || *got.WorkSessionID != sessionID {
		t.Errorf("WorkSessionID = %v, want unchanged %s", got.WorkSessionID, sessionID)
	}
}

// TestRecordExecutionResult_UnknownWithContentAgainstExistingDraft_Enriches
// is the M-2a regression reproduction (PR #152 second round): a call
// carrying result="unknown" but WITH new content (exactly what the MCP
// instructions in server.go document callers doing to enrich a
// complete_task-seeded draft) against an existing 'unknown' draft must
// still reach FinalizeDraft — not be silently dropped as
// ActionDraftPreserved. FinalizeDraft is now merge-only (COALESCE), so this
// is safe: the call's content is written, anything it didn't supply is left
// alone by the store layer.
func TestRecordExecutionResult_UnknownWithContentAgainstExistingDraft_Enriches(t *testing.T) {
	entityID := uuid.New()
	draftID := uuid.New()

	fs := &fakeStore{
		t:         t,
		hasLatest: true,
		latest: Outcome{
			ID:         draftID,
			EntityType: "task",
			EntityID:   entityID,
			Result:     "unknown",
		},
	}

	got, action, err := RecordExecutionResult(context.Background(), fs, CreateOutcomeParams{
		EntityType: "task",
		EntityID:   entityID,
		Result:     "unknown",
		Notes:      "still unknown, but here's what I know so far",
	})
	if err != nil {
		t.Fatalf("RecordExecutionResult: %v", err)
	}
	if action != ActionDraftEnriched {
		t.Errorf("action = %q, want %q", action, ActionDraftEnriched)
	}
	if !fs.finalizeCalled {
		t.Error("FinalizeDraft must be called when an unknown-against-unknown call carries new content (M-2a regression)")
	}
	if fs.createCalled {
		t.Error("CreateOutcome must NOT be called — this stays in place, never a new row")
	}
	if got.ID != draftID {
		t.Errorf("returned ID = %s, want the same draft ID %s", got.ID, draftID)
	}
	if got.Result != resultUnknown {
		t.Errorf("Result = %q, want unknown (ActionDraftEnriched is not a finalization)", got.Result)
	}
	if got.Notes != "still unknown, but here's what I know so far" {
		t.Errorf("Notes = %q, want the newly-supplied content to have been written", got.Notes)
	}
}

// TestHasNewContent covers the pure helper RecordExecutionResult uses to
// decide ActionDraftPreserved vs ActionDraftEnriched for an
// unknown-against-unknown call.
func TestHasNewContent(t *testing.T) {
	sessionID := uuid.New()
	tests := []struct {
		name   string
		params CreateOutcomeParams
		want   bool
	}{
		{"completely empty", CreateOutcomeParams{Result: "unknown"}, false},
		{"notes only", CreateOutcomeParams{Notes: "x"}, true},
		{"metrics only", CreateOutcomeParams{Metrics: []byte(`{"a":1}`)}, true},
		{"related_rule_ids only", CreateOutcomeParams{RelatedRuleIDs: []uuid.UUID{uuid.New()}}, true},
		{"work_session_id only", CreateOutcomeParams{WorkSessionID: &sessionID}, true},
		{"empty-but-non-nil metrics does not count", CreateOutcomeParams{Metrics: []byte{}}, false},
		{"empty-but-non-nil related_rule_ids does not count", CreateOutcomeParams{RelatedRuleIDs: []uuid.UUID{}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasNewContent(tc.params); got != tc.want {
				t.Errorf("hasNewContent() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRecordExecutionResult_TerminalAgainstDraft_StillFinalizes is a
// regression guard for the M-1 fix's scope: a call carrying a genuinely
// terminal result against an existing draft must still finalize it in
// place via FinalizeDraft — M-1 only blocks unknown-against-unknown, not
// legitimate finalization.
func TestRecordExecutionResult_TerminalAgainstDraft_StillFinalizes(t *testing.T) {
	entityID := uuid.New()
	draftID := uuid.New()

	fs := &fakeStore{
		t:         t,
		hasLatest: true,
		latest: Outcome{
			ID:         draftID,
			EntityType: "task",
			EntityID:   entityID,
			Result:     "unknown",
		},
	}

	got, action, err := RecordExecutionResult(context.Background(), fs, CreateOutcomeParams{
		EntityType: "task",
		EntityID:   entityID,
		Result:     "success",
		Notes:      "shipped fine",
	})
	if err != nil {
		t.Fatalf("RecordExecutionResult: %v", err)
	}
	if action != ActionFinalizedDraft {
		t.Errorf("action = %q, want %q", action, ActionFinalizedDraft)
	}
	if !fs.finalizeCalled {
		t.Error("FinalizeDraft must be called for a terminal result against an existing draft")
	}
	if got.ID != draftID {
		t.Errorf("returned ID = %s, want the same draft ID %s", got.ID, draftID)
	}
	if got.Result != "success" {
		t.Errorf("Result = %q, want success", got.Result)
	}
}

// ---------------------------------------------------------------------------
// PR #152 round 3 Major: draft-enrich retry idempotency. Before this fix,
// RecordExecutionResult's draft branch had no idempotency check reachable
// for an enrich retry — a byte-identical retry of a content-bearing
// result="unknown" call re-invoked FinalizeDraft and re-reported
// ActionDraftEnriched, which tools_outcome.go treats as "a real write
// occurred" and re-fires SetOutcomeLink/atomize.
// ---------------------------------------------------------------------------

// TestIsDraftIdempotentReplay covers the pure comparison helper the draft
// branch uses to distinguish a byte-identical retry (no new information)
// from a call that carries genuinely new content, mirroring what
// FinalizeDraft's APPEND-ONLY UPDATE (migration 000075) would actually do.
// Several cases here flipped `want` relative to the pre-append-semantics
// version of this test (see each case's comment for why) — that's the
// deliberate, expected consequence of the redesign: "new information" now
// means "would FinalizeDraft's append/union/set-once merge actually change
// the stored value", not "differs byte-for-byte from what's already there".
func TestIsDraftIdempotentReplay(t *testing.T) {
	sessionA, sessionB := uuid.New(), uuid.New()
	ruleA, ruleB := uuid.New(), uuid.New()
	base := Outcome{
		Result:         resultUnknown,
		Notes:          "still investigating",
		Metrics:        []byte(`{"duration_ms":100}`),
		RelatedRuleIDs: []uuid.UUID{ruleA},
		WorkSessionID:  &sessionA,
	}

	tests := []struct {
		name   string
		latest Outcome
		params CreateOutcomeParams
		want   bool
	}{
		{
			name:   "byte-identical retry: all fields match",
			latest: base,
			params: CreateOutcomeParams{
				Result:         resultUnknown,
				Notes:          "still investigating",
				Metrics:        []byte(`{"duration_ms":100}`),
				RelatedRuleIDs: []uuid.UUID{ruleA},
				WorkSessionID:  &sessionA,
			},
			want: true,
		},
		{
			name:   "empty params against a content-bearing draft: nothing to write, no diff",
			latest: base,
			params: CreateOutcomeParams{Result: resultUnknown},
			want:   true,
		},
		{
			name:   "different notes is new content",
			latest: base,
			params: CreateOutcomeParams{Result: resultUnknown, Notes: "actually it's this"},
			want:   false,
		},
		{
			name: "notes retry: incoming already the trailing appended block is a replay",
			// After a real append, latest.Notes == "still investigating\n\nverified in prod".
			// A retry sending just the appended tail again must be detected
			// as idempotent (this is exactly the byte-identical-retry shape
			// TestRecordExecutionResult_DraftEnrichRetry_ByteIdenticalIsIdempotent
			// exercises at the RecordExecutionResult level).
			latest: Outcome{Result: resultUnknown, Notes: "still investigating\n\nverified in prod"},
			params: CreateOutcomeParams{Result: resultUnknown, Notes: "verified in prod"},
			want:   true,
		},
		{
			// FLIPPED from the pre-append-semantics version (was `want: false`):
			// under append-only merge, resupplying an ALREADY-PRESENT metrics
			// key with a different value is NOT new information — FinalizeDraft
			// will keep the existing value regardless (only ABSENT keys get
			// added). This call would be a complete no-op write.
			name:   "same metrics key with a different value is NOT new (existing value can never be overwritten)",
			latest: base,
			params: CreateOutcomeParams{Result: resultUnknown, Metrics: []byte(`{"duration_ms":200}`)},
			want:   true,
		},
		{
			name:   "a genuinely NEW metrics key (alongside an already-present one) is new content",
			latest: base,
			params: CreateOutcomeParams{Result: resultUnknown, Metrics: []byte(`{"duration_ms":200,"retries":3}`)},
			want:   false,
		},
		{
			// FLIPPED from the pre-append-semantics version (was `want: false`,
			// framed as "reverse-protection"): once work_session_id is already
			// set (sessionA here), FinalizeDraft's set-once rule means NO later
			// call can re-point it, regardless of what ID it supplies — so this
			// is a no-op write, not new information. The genuine "must still
			// write" case is covered below, where latest.WorkSessionID is nil.
			name:   "work_session_id already set: supplying a DIFFERENT id is still NOT new (set-once)",
			latest: base,
			params: CreateOutcomeParams{Result: resultUnknown, Notes: "still investigating", WorkSessionID: &sessionB},
			want:   true,
		},
		{
			name:   "work_session_id unset (nil) on latest: supplying an id IS new content",
			latest: Outcome{Result: resultUnknown, WorkSessionID: nil},
			params: CreateOutcomeParams{Result: resultUnknown, WorkSessionID: &sessionB},
			want:   false,
		},
		{
			name:   "same notes but a DIFFERENT related_rule_ids set is new content",
			latest: base,
			params: CreateOutcomeParams{Result: resultUnknown, Notes: "still investigating", RelatedRuleIDs: []uuid.UUID{ruleB}},
			want:   false,
		},
		{
			// FLIPPED from the pre-append-semantics version (was `want: false`,
			// "mirrors FinalizeDraft's literal array replace"): FinalizeDraft
			// now UNIONS related_rule_ids instead of replacing the whole array,
			// so membership — not order — is what determines the stored
			// result. A differently-ordered resubmission of the exact same ID
			// set produces an IDENTICAL final array, so it carries no new info.
			name:   "same related_rule_ids set in a different ORDER is NOT new (union is order-insensitive)",
			latest: Outcome{Result: resultUnknown, RelatedRuleIDs: []uuid.UUID{ruleA, ruleB}},
			params: CreateOutcomeParams{Result: resultUnknown, RelatedRuleIDs: []uuid.UUID{ruleB, ruleA}},
			want:   true,
		},
		{
			name:   "nil vs empty-but-non-nil related_rule_ids on latest are equivalent to an empty params slice",
			latest: Outcome{Result: resultUnknown, RelatedRuleIDs: nil},
			params: CreateOutcomeParams{Result: resultUnknown, RelatedRuleIDs: []uuid.UUID{}},
			want:   true,
		},
		{
			name:   "work_session_id already set on latest, params repeats the SAME id",
			latest: base,
			params: CreateOutcomeParams{Result: resultUnknown, WorkSessionID: &sessionA},
			want:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isDraftIdempotentReplay(tc.latest, tc.params)
			if got != tc.want {
				t.Errorf("isDraftIdempotentReplay() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRecordExecutionResult_DraftEnrichRetry_ByteIdenticalIsIdempotent is the
// PR #152 round 3 Major reproduction: a retry of a content-bearing
// result="unknown" enrich call, byte-identical to the first, must be
// detected as a replay (ActionReplayedIdempotent) and must NOT re-invoke
// FinalizeDraft. The first army's isolated repro (quoted in the dispatch)
// showed both calls reporting action=draft_enriched with finalizeCalled=true
// — this test pins the fixed behavior: only the first call writes.
func TestRecordExecutionResult_DraftEnrichRetry_ByteIdenticalIsIdempotent(t *testing.T) {
	entityID := uuid.New()
	draftID := uuid.New()

	fs := &fakeStore{
		t:         t,
		hasLatest: true,
		latest: Outcome{
			ID:         draftID,
			EntityType: "task",
			EntityID:   entityID,
			Result:     resultUnknown,
		},
	}

	params := CreateOutcomeParams{
		EntityType: "task",
		EntityID:   entityID,
		Result:     resultUnknown,
		Notes:      "still investigating",
	}

	first, action1, err := RecordExecutionResult(context.Background(), fs, params)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if action1 != ActionDraftEnriched {
		t.Fatalf("first call action = %q, want %q", action1, ActionDraftEnriched)
	}
	if !fs.finalizeCalled {
		t.Fatal("first call must invoke FinalizeDraft")
	}
	fs.finalizeCalled = false // reset spy to isolate the retry's own behaviour

	second, action2, err := RecordExecutionResult(context.Background(), fs, params)
	if err != nil {
		t.Fatalf("retry call: %v", err)
	}
	if action2 != ActionReplayedIdempotent {
		t.Errorf("retry action = %q, want %q (byte-identical retry — Major regression)", action2, ActionReplayedIdempotent)
	}
	if fs.finalizeCalled {
		t.Error("FinalizeDraft must NOT be called again for a byte-identical retry (Major regression)")
	}
	if second.ID != first.ID {
		t.Errorf("retry must return the SAME row: got %s, want %s", second.ID, first.ID)
	}
	if second.Notes != first.Notes {
		t.Errorf("retry Notes = %q, want unchanged %q", second.Notes, first.Notes)
	}
}

// TestRecordExecutionResult_DraftEnrichRetry_NewWorkSessionIDStillWrites is
// the Major fix's mandatory reverse-protection guard: a retry carrying the
// SAME notes as the first call but a NEW work_session_id is NOT a
// byte-identical replay — WorkSessionID is a field FinalizeDraft actually
// writes (COALESCE), so this call carries genuinely new information and MUST
// still reach FinalizeDraft, reporting ActionDraftEnriched. A comparison
// that only checked Notes/Metrics (like the terminal branch's
// isIdempotentReplay) would wrongly classify this as a replay and silently
// drop the session link — reintroducing M-2a's failure mode for this field.
func TestRecordExecutionResult_DraftEnrichRetry_NewWorkSessionIDStillWrites(t *testing.T) {
	entityID := uuid.New()
	draftID := uuid.New()
	sessionID := uuid.New()

	fs := &fakeStore{
		t:         t,
		hasLatest: true,
		latest: Outcome{
			ID:         draftID,
			EntityType: "task",
			EntityID:   entityID,
			Result:     resultUnknown,
			Notes:      "still investigating",
		},
	}

	got, action, err := RecordExecutionResult(context.Background(), fs, CreateOutcomeParams{
		EntityType:    "task",
		EntityID:      entityID,
		Result:        resultUnknown,
		Notes:         "still investigating", // identical to what's already stored
		WorkSessionID: &sessionID,            // NEW — not present on latest
	})
	if err != nil {
		t.Fatalf("RecordExecutionResult: %v", err)
	}
	if action != ActionDraftEnriched {
		t.Errorf("action = %q, want %q (new work_session_id must still count as new content)", action, ActionDraftEnriched)
	}
	if !fs.finalizeCalled {
		t.Error("FinalizeDraft must be called — a new work_session_id is genuinely new information")
	}
	if got.WorkSessionID == nil || *got.WorkSessionID != sessionID {
		t.Errorf("WorkSessionID = %v, want %s to have been written", got.WorkSessionID, sessionID)
	}
}

// TestRecordExecutionResult_DraftEnrichRetry_NewRelatedRuleIDsStillWrites
// mirrors the WorkSessionID reverse-protection test above for
// RelatedRuleIDs: identical notes, but a new (non-empty, different)
// RelatedRuleIDs set must still be treated as new content, not a replay.
func TestRecordExecutionResult_DraftEnrichRetry_NewRelatedRuleIDsStillWrites(t *testing.T) {
	entityID := uuid.New()
	draftID := uuid.New()
	ruleID := uuid.New()

	fs := &fakeStore{
		t:         t,
		hasLatest: true,
		latest: Outcome{
			ID:         draftID,
			EntityType: "task",
			EntityID:   entityID,
			Result:     resultUnknown,
			Notes:      "still investigating",
		},
	}

	got, action, err := RecordExecutionResult(context.Background(), fs, CreateOutcomeParams{
		EntityType:     "task",
		EntityID:       entityID,
		Result:         resultUnknown,
		Notes:          "still investigating",
		RelatedRuleIDs: []uuid.UUID{ruleID},
	})
	if err != nil {
		t.Fatalf("RecordExecutionResult: %v", err)
	}
	if action != ActionDraftEnriched {
		t.Errorf("action = %q, want %q (new related_rule_ids must still count as new content)", action, ActionDraftEnriched)
	}
	if !fs.finalizeCalled {
		t.Error("FinalizeDraft must be called — new related_rule_ids is genuinely new information")
	}
	if len(got.RelatedRuleIDs) != 1 || got.RelatedRuleIDs[0] != ruleID {
		t.Errorf("RelatedRuleIDs = %v, want [%s] to have been written", got.RelatedRuleIDs, ruleID)
	}
}
