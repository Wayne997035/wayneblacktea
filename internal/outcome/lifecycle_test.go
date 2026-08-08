package outcome

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestIsIdempotentReplay covers the pure comparison helper RecordExecutionResult
// uses to decide ActionReplayedIdempotent vs ActionSuperseded.
func TestIsIdempotentReplay(t *testing.T) {
	entityID := uuid.New()
	ruleA, ruleB := uuid.New(), uuid.New()
	sessionA, sessionB := uuid.New(), uuid.New()
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
		// PR #152 round 8 Major M-R8-1: identical result/notes/metrics but a
		// genuinely NEW related_rule_id or work_session_id must no longer be
		// misclassified as idempotent — both directions pinned below.
		{
			name: "same result/notes/metrics but a genuinely new related_rule_id -> NOT idempotent",
			latest: Outcome{
				EntityType: "task", EntityID: entityID, Result: "success",
				Notes: "done on time", Metrics: []byte(`{"duration_ms":100}`),
				RelatedRuleIDs: []uuid.UUID{ruleA},
			},
			params: CreateOutcomeParams{
				Result: "success", Notes: "done on time", Metrics: []byte(`{"duration_ms":100}`),
				RelatedRuleIDs: []uuid.UUID{ruleA, ruleB},
			},
			want: false,
		},
		{
			name: "same related_rule_ids set (no new IDs) -> still idempotent",
			latest: Outcome{
				EntityType: "task", EntityID: entityID, Result: "success",
				Notes: "done on time", Metrics: []byte(`{"duration_ms":100}`),
				RelatedRuleIDs: []uuid.UUID{ruleA},
			},
			params: CreateOutcomeParams{
				Result: "success", Notes: "done on time", Metrics: []byte(`{"duration_ms":100}`),
				RelatedRuleIDs: []uuid.UUID{ruleA},
			},
			want: true,
		},
		{
			name:   "same result/notes/metrics but a genuinely new work_session_id (was unset) -> NOT idempotent",
			latest: base,
			params: CreateOutcomeParams{
				Result: "success", Notes: "done on time", Metrics: []byte(`{"duration_ms":100}`),
				WorkSessionID: &sessionA,
			},
			want: false,
		},
		{
			name: "work_session_id already set on latest, params supplies a DIFFERENT id -> NOT idempotent (terminal branch has no set-once)",
			latest: Outcome{
				EntityType: "task", EntityID: entityID, Result: "success",
				Notes: "done on time", Metrics: []byte(`{"duration_ms":100}`),
				WorkSessionID: &sessionA,
			},
			params: CreateOutcomeParams{
				Result: "success", Notes: "done on time", Metrics: []byte(`{"duration_ms":100}`),
				WorkSessionID: &sessionB,
			},
			want: false,
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
	f.latest.Metrics = mergeMetricsExistingWins(f.latest.Metrics, params.Metrics)
	f.latest.RelatedRuleIDs = unionRuleIDs(f.latest.RelatedRuleIDs, params.RelatedRuleIDs)
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

	got, action, _, err := RecordExecutionResult(context.Background(), fs, CreateOutcomeParams{
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

	got, action, _, err := RecordExecutionResult(context.Background(), fs, CreateOutcomeParams{
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

	got, action, _, err := RecordExecutionResult(context.Background(), fs, CreateOutcomeParams{
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
// PR #152 round 8 Major M-R8-1: a retry against a TERMINAL outcome that
// repeats the same result/notes/metrics but supplies a genuinely new
// related_rule_id or work_session_id must fall through to the supersede
// branch instead of being misclassified as ActionReplayedIdempotent — see
// isIdempotentReplay's doc comment for the full rationale.
// ---------------------------------------------------------------------------

// TestRecordExecutionResult_TerminalRetry_NewRelatedRuleIDs_Supersedes is
// M-R8-1's direct reproduction: latest is a TERMINAL outcome with
// related_rule_ids=[A]; a retry carrying the SAME result/notes/metrics but
// related_rule_ids=[A,B] must create a NEW row (SupersedesID set) carrying
// this call's own related_rule_ids verbatim — CreateOutcome for a supersede
// copies params as-is, it does not merge with the row it supersedes (that
// merge-on-retry semantics belongs only to the draft branch's
// FinalizeDraft). Before this fix, isIdempotentReplay ignored
// RelatedRuleIDs entirely, so this call was wrongly classified as a no-op
// replay and the new rule ID B was silently dropped — behavior_governance.go
// reads RelatedRuleIDs to compute rule confidence, so a lost link means a
// bad rule's confidence is never decremented.
func TestRecordExecutionResult_TerminalRetry_NewRelatedRuleIDs_Supersedes(t *testing.T) {
	entityID := uuid.New()
	priorID := uuid.New()
	ruleA, ruleB := uuid.New(), uuid.New()

	fs := &fakeStore{
		t:         t,
		hasLatest: true,
		latest: Outcome{
			ID:             priorID,
			EntityType:     "task",
			EntityID:       entityID,
			Result:         "success",
			Notes:          "shipped fine",
			RelatedRuleIDs: []uuid.UUID{ruleA},
		},
	}

	got, action, _, err := RecordExecutionResult(context.Background(), fs, CreateOutcomeParams{
		EntityType:     "task",
		EntityID:       entityID,
		Result:         "success",
		Notes:          "shipped fine",
		RelatedRuleIDs: []uuid.UUID{ruleA, ruleB},
	})
	if err != nil {
		t.Fatalf("RecordExecutionResult: %v", err)
	}
	if action != ActionSuperseded {
		t.Fatalf("action = %q, want %q (M-R8-1 regression: a genuinely new related_rule_id must not be "+
			"silently dropped as an idempotent replay)", action, ActionSuperseded)
	}
	if got.ID == priorID {
		t.Fatal("a call carrying a genuinely new related_rule_id must create a NEW row, not reuse the prior terminal row")
	}
	if !fs.createCalled || fs.finalizeCalled {
		t.Errorf("expected CreateOutcome (not FinalizeDraft) to be called for a terminal supersede")
	}
	if got.SupersedesID == nil || *got.SupersedesID != priorID {
		t.Errorf("SupersedesID = %v, want %s", got.SupersedesID, priorID)
	}
	if len(got.RelatedRuleIDs) != 2 || got.RelatedRuleIDs[0] != ruleA || got.RelatedRuleIDs[1] != ruleB {
		t.Errorf("RelatedRuleIDs = %v, want [%s %s]", got.RelatedRuleIDs, ruleA, ruleB)
	}
	// The prior row must remain untouched with its original single ID —
	// fakeStore.CreateOutcome never mutates f.latest, mirroring both real
	// backends' supersede path (the prior row is never written to).
	if len(fs.latest.RelatedRuleIDs) != 1 || fs.latest.RelatedRuleIDs[0] != ruleA {
		t.Errorf("prior row's RelatedRuleIDs was mutated: %v", fs.latest.RelatedRuleIDs)
	}
}

// ---------------------------------------------------------------------------
// GTD 35d42906: terminal supersede must UNION latest.RelatedRuleIDs into the
// new row, not carry this call's delta-only IDs verbatim — otherwise a rule
// ID that was only ever linked on the row being superseded loses its
// governance vote FOREVER, because behavior_governance.go's supersession
// filter (internal/scheduler/behavior_governance.go) skips every row that
// something else supersedes.
// ---------------------------------------------------------------------------

// TestRecordExecutionResult_TerminalRetry_DeltaOnlyRelatedRuleIDs_UnionsIntoSupersedeRow
// is the direct reproduction: a terminal retry with the SAME
// result/notes/metrics but a genuinely new (per relatedRuleIDsHasNew)
// related_rule_ids set falls into ActionSuperseded (isIdempotentReplay
// returns false), but before this fix the new row's RelatedRuleIDs was set
// to supersedeParams.RelatedRuleIDs (this call's own IDs) verbatim, dropping
// any ID that only latest carried. The fix unions latest.RelatedRuleIDs
// (first) with supersedeParams.RelatedRuleIDs (second), deduped — mirroring
// FinalizeDraft's own existing-then-new union order (store.go:614-617) —
// and leaves latest.RelatedRuleIDs itself untouched (no in-place append
// aliasing).
func TestRecordExecutionResult_TerminalRetry_DeltaOnlyRelatedRuleIDs_UnionsIntoSupersedeRow(t *testing.T) {
	ruleA, ruleB, ruleC := uuid.New(), uuid.New(), uuid.New()

	tests := []struct {
		name          string
		latestRuleIDs []uuid.UUID
		paramsRuleIDs []uuid.UUID
		wantRuleIDs   []uuid.UUID
	}{
		{
			name:          "delta-only retry: new row inherits latest's IDs plus this call's own",
			latestRuleIDs: []uuid.UUID{ruleA, ruleB},
			paramsRuleIDs: []uuid.UUID{ruleC},
			wantRuleIDs:   []uuid.UUID{ruleA, ruleB, ruleC},
		},
		{
			name:          "overlapping retry: an ID present on both sides is not duplicated",
			latestRuleIDs: []uuid.UUID{ruleA, ruleB},
			paramsRuleIDs: []uuid.UUID{ruleB, ruleC},
			wantRuleIDs:   []uuid.UUID{ruleA, ruleB, ruleC},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entityID := uuid.New()
			priorID := uuid.New()
			// Independent copy so a mutation of fs.latest.RelatedRuleIDs (the
			// aliasing bug this fix must avoid) is distinguishable from tc's
			// own slice below.
			latestRuleIDs := append([]uuid.UUID{}, tc.latestRuleIDs...)

			fs := &fakeStore{
				t:         t,
				hasLatest: true,
				latest: Outcome{
					ID:             priorID,
					EntityType:     "task",
					EntityID:       entityID,
					Result:         "success",
					Notes:          "shipped fine",
					RelatedRuleIDs: latestRuleIDs,
				},
			}

			got, action, _, err := RecordExecutionResult(context.Background(), fs, CreateOutcomeParams{
				EntityType:     "task",
				EntityID:       entityID,
				Result:         "success",
				Notes:          "shipped fine",
				RelatedRuleIDs: tc.paramsRuleIDs,
			})
			if err != nil {
				t.Fatalf("RecordExecutionResult: %v", err)
			}
			if action != ActionSuperseded {
				t.Fatalf("action = %q, want %q", action, ActionSuperseded)
			}
			if !slices.Equal(got.RelatedRuleIDs, tc.wantRuleIDs) {
				t.Errorf("RelatedRuleIDs = %v, want %v", got.RelatedRuleIDs, tc.wantRuleIDs)
			}
			// The row being superseded must never be mutated by the union.
			if !slices.Equal(fs.latest.RelatedRuleIDs, tc.latestRuleIDs) {
				t.Errorf("prior row's RelatedRuleIDs was mutated: got %v, want %v", fs.latest.RelatedRuleIDs, tc.latestRuleIDs)
			}
		})
	}
}

// TestRecordExecutionResult_TerminalRetry_DeltaOnlyRelatedRuleIDs_CapsUnionAtMax
// is the cap sub-scenario: 60 existing + 60 newly-supplied IDs union to 120,
// which exceeds MaxRelatedRuleIDsTotal (100) — the new row's RelatedRuleIDs
// must be capped at 100 via CapRelatedRuleIDs, and per that function's
// contract (store.go:475: "an already-linked rule ID can never be silently
// removed"), every one of the 60 pre-existing IDs (well under the cap on
// their own) must survive the cap.
func TestRecordExecutionResult_TerminalRetry_DeltaOnlyRelatedRuleIDs_CapsUnionAtMax(t *testing.T) {
	entityID := uuid.New()
	priorID := uuid.New()

	existing := make([]uuid.UUID, 60)
	for i := range existing {
		existing[i] = uuid.New()
	}
	incoming := make([]uuid.UUID, 60)
	for i := range incoming {
		incoming[i] = uuid.New()
	}

	fs := &fakeStore{
		t:         t,
		hasLatest: true,
		latest: Outcome{
			ID:             priorID,
			EntityType:     "task",
			EntityID:       entityID,
			Result:         "success",
			Notes:          "shipped fine",
			RelatedRuleIDs: existing,
		},
	}

	got, action, _, err := RecordExecutionResult(context.Background(), fs, CreateOutcomeParams{
		EntityType:     "task",
		EntityID:       entityID,
		Result:         "success",
		Notes:          "shipped fine",
		RelatedRuleIDs: incoming,
	})
	if err != nil {
		t.Fatalf("RecordExecutionResult: %v", err)
	}
	if action != ActionSuperseded {
		t.Fatalf("action = %q, want %q", action, ActionSuperseded)
	}
	if len(got.RelatedRuleIDs) != MaxRelatedRuleIDsTotal {
		t.Fatalf("len(RelatedRuleIDs) = %d, want capped at %d (60 existing + 60 new = 120 uncapped)",
			len(got.RelatedRuleIDs), MaxRelatedRuleIDsTotal)
	}
	gotSet := make(map[uuid.UUID]bool, len(got.RelatedRuleIDs))
	for _, id := range got.RelatedRuleIDs {
		gotSet[id] = true
	}
	for _, id := range existing {
		if !gotSet[id] {
			t.Errorf("existing rule ID %s was dropped by the cap", id)
		}
	}
}

// TestRecordExecutionResult_TerminalRetry_DifferentWorkSessionID_Supersedes
// is round 9 Critical C-1's direct reproduction: latest is a TERMINAL outcome
// already linked to sessionA; a retry carrying the SAME result/notes/metrics
// but a DIFFERENT work_session_id (sessionB — e.g. a second agent or a
// Claude Code restart re-recording the same verdict) must create a NEW row
// (SupersedesID set) carrying THIS call's session ID verbatim — not be
// misclassified as ActionReplayedIdempotent. Before the fix,
// isIdempotentReplay reused workSessionIDHasNew's nil-only "new" rule (only
// correct for the draft branch's COALESCE semantics — see
// isIdempotentReplay's doc comment), so a differing already-set session ID
// was wrongly treated as "no new info": no row was written, SetOutcomeLink
// was skipped by the caller (internal/mcp/tools_outcome.go:368, gated on
// exactly this action), and work_sessions.outcome_id for sessionB stayed
// NULL while the response echoed sessionA's row with no error. Asserting
// action == ActionSuperseded here is what makes that downstream skip NOT
// fire — tools_outcome.go's skip-list only covers ActionReplayedIdempotent
// and ActionDraftPreserved.
func TestRecordExecutionResult_TerminalRetry_DifferentWorkSessionID_Supersedes(t *testing.T) {
	entityID := uuid.New()
	priorID := uuid.New()
	sessionA, sessionB := uuid.New(), uuid.New()

	fs := &fakeStore{
		t:         t,
		hasLatest: true,
		latest: Outcome{
			ID:            priorID,
			EntityType:    "task",
			EntityID:      entityID,
			Result:        "success",
			Notes:         "shipped fine",
			WorkSessionID: &sessionA,
		},
	}

	got, action, _, err := RecordExecutionResult(context.Background(), fs, CreateOutcomeParams{
		EntityType:    "task",
		EntityID:      entityID,
		Result:        "success",
		Notes:         "shipped fine",
		WorkSessionID: &sessionB,
	})
	if err != nil {
		t.Fatalf("RecordExecutionResult: %v", err)
	}
	if action != ActionSuperseded {
		t.Fatalf("action = %q, want %q (round 9 C-1 regression: a differing already-set work_session_id "+
			"must not be silently dropped as an idempotent replay)", action, ActionSuperseded)
	}
	if got.ID == priorID {
		t.Fatal("a call carrying a genuinely different work_session_id must create a NEW row, not reuse the prior terminal row")
	}
	if !fs.createCalled || fs.finalizeCalled {
		t.Errorf("expected CreateOutcome (not FinalizeDraft) to be called for a terminal supersede")
	}
	if got.WorkSessionID == nil || *got.WorkSessionID != sessionB {
		t.Errorf("new row's WorkSessionID = %v, want %s (this call's own session, "+
			"written verbatim by CreateOutcome's plain INSERT)", got.WorkSessionID, sessionB)
	}
	// The prior row must remain untouched, still linked to sessionA —
	// fakeStore.CreateOutcome never mutates f.latest, mirroring both real
	// backends' supersede path (the prior row is never written to, so
	// sessionA's link is never silently repointed).
	if fs.latest.WorkSessionID == nil || *fs.latest.WorkSessionID != sessionA {
		t.Errorf("prior row's WorkSessionID was mutated: %v", fs.latest.WorkSessionID)
	}
}

// TestRecordExecutionResult_TerminalRetry_SameWorkSessionID_StillIdempotent
// is the non-regression companion to the test above: a retry carrying the
// IDENTICAL work_session_id (byte-for-byte, same UUID as latest's) alongside
// identical result/notes/metrics genuinely carries zero new information —
// workSessionIDDiffers must return false for equal UUIDs (not just for nil),
// so this must still take the no-write replay path.
func TestRecordExecutionResult_TerminalRetry_SameWorkSessionID_StillIdempotent(t *testing.T) {
	entityID := uuid.New()
	priorID := uuid.New()
	session := uuid.New()

	fs := &fakeStore{
		t:         t,
		hasLatest: true,
		latest: Outcome{
			ID:            priorID,
			EntityType:    "task",
			EntityID:      entityID,
			Result:        "success",
			Notes:         "shipped fine",
			WorkSessionID: &session,
		},
	}

	got, action, _, err := RecordExecutionResult(context.Background(), fs, CreateOutcomeParams{
		EntityType:    "task",
		EntityID:      entityID,
		Result:        "success",
		Notes:         "shipped fine",
		WorkSessionID: &session,
	})
	if err != nil {
		t.Fatalf("RecordExecutionResult: %v", err)
	}
	if action != ActionReplayedIdempotent {
		t.Fatalf("action = %q, want %q (identical session ID carries no new info)", action, ActionReplayedIdempotent)
	}
	if got.ID != priorID {
		t.Errorf("got.ID = %s, want the same prior row %s (no-op replay must not create a new row)", got.ID, priorID)
	}
	if fs.createCalled || fs.finalizeCalled {
		t.Error("neither CreateOutcome nor FinalizeDraft must be called for an idempotent replay")
	}
}

// TestRecordExecutionResult_TerminalRetry_NilToSetWorkSessionID_Supersedes
// pins the pre-existing (unchanged by round 9 C-1) nil-to-non-nil case: a
// retry that supplies a work_session_id where latest had none must still
// supersede exactly as it did before this fix — workSessionIDDiffers's
// `existing == nil` arm preserves this behaviour, it did not only add the
// differing-non-nil case.
func TestRecordExecutionResult_TerminalRetry_NilToSetWorkSessionID_Supersedes(t *testing.T) {
	entityID := uuid.New()
	priorID := uuid.New()
	session := uuid.New()

	fs := &fakeStore{
		t:         t,
		hasLatest: true,
		latest: Outcome{
			ID:         priorID,
			EntityType: "task",
			EntityID:   entityID,
			Result:     "success",
			Notes:      "shipped fine",
			// WorkSessionID deliberately nil — latest was recorded with no
			// session link at all.
		},
	}

	got, action, _, err := RecordExecutionResult(context.Background(), fs, CreateOutcomeParams{
		EntityType:    "task",
		EntityID:      entityID,
		Result:        "success",
		Notes:         "shipped fine",
		WorkSessionID: &session,
	})
	if err != nil {
		t.Fatalf("RecordExecutionResult: %v", err)
	}
	if action != ActionSuperseded {
		t.Fatalf("action = %q, want %q (nil-to-set must still be treated as new info, preserved behaviour)", action, ActionSuperseded)
	}
	if got.ID == priorID {
		t.Fatal("a call attaching a session ID where latest had none must create a NEW row")
	}
	if got.WorkSessionID == nil || *got.WorkSessionID != session {
		t.Errorf("new row's WorkSessionID = %v, want %s", got.WorkSessionID, session)
	}
}

// ---------------------------------------------------------------------------
// PR #152 round 8 Major (80cf80b6 finding 2): RecordExecutionResult's
// ErrDraftAlreadyFinalized retry loop must be bounded as a WHOLE function,
// not merely "a draft can only be finalized once" — each re-resolve may
// land on a DIFFERENT new draft row under adversarial concurrency.
// retryCapStore embeds *fakeStore (inheriting its "unexpected call fails
// loudly" behaviour for every method RecordExecutionResult should never
// reach) and overrides only GetLatestForEntity/FinalizeDraft to control the
// race-loss count.
// ---------------------------------------------------------------------------

// retryCapStore is a StoreIface spy for the retry-cap tests: GetLatestForEntity
// always reports the SAME 'unknown' draft; FinalizeDraft fails with
// ErrDraftAlreadyFinalized exactly failTimes times before succeeding.
type retryCapStore struct {
	*fakeStore

	draftID   uuid.UUID
	failTimes int

	getCalls      int
	finalizeCalls int
}

func newRetryCapStore(t *testing.T, draftID uuid.UUID, failTimes int) *retryCapStore {
	return &retryCapStore{fakeStore: &fakeStore{t: t}, draftID: draftID, failTimes: failTimes}
}

func (r *retryCapStore) GetLatestForEntity(context.Context, *uuid.UUID, string, uuid.UUID) (Outcome, error) {
	r.getCalls++
	return Outcome{ID: r.draftID, Result: resultUnknown}, nil
}

func (r *retryCapStore) FinalizeDraft(_ context.Context, _ uuid.UUID, params CreateOutcomeParams) (Outcome, error) {
	r.finalizeCalls++
	if r.finalizeCalls <= r.failTimes {
		return Outcome{}, ErrDraftAlreadyFinalized
	}
	return Outcome{ID: r.draftID, Result: params.Result, Notes: params.Notes}, nil
}

var _ StoreIface = (*retryCapStore)(nil)

// TestRecordExecutionResult_FinalizeDraftRetryCap_ExceededReturnsWrappedError
// is the direct reproduction: a store whose FinalizeDraft NEVER stops
// returning ErrDraftAlreadyFinalized must not recurse/loop forever — after
// exactly maxFinalizeDraftRetries (3) attempts, RecordExecutionResult must
// return a wrapped error instead of continuing.
func TestRecordExecutionResult_FinalizeDraftRetryCap_ExceededReturnsWrappedError(t *testing.T) {
	entityID := uuid.New()
	draftID := uuid.New()
	store := newRetryCapStore(t, draftID, 99) // always fails, far more than the cap

	_, _, _, err := RecordExecutionResult(context.Background(), store, CreateOutcomeParams{
		EntityType: "task",
		EntityID:   entityID,
		Result:     "success",
	})
	if err == nil {
		t.Fatal("expected an error after exceeding the retry cap, got nil")
	}
	if !errors.Is(err, ErrDraftAlreadyFinalized) {
		t.Errorf("err = %v, want it to wrap ErrDraftAlreadyFinalized (the actual cause of every retry)", err)
	}
	if store.finalizeCalls != maxFinalizeDraftRetries {
		t.Errorf("finalizeCalls = %d, want exactly %d (the retry cap)", store.finalizeCalls, maxFinalizeDraftRetries)
	}
	if store.getCalls != maxFinalizeDraftRetries {
		t.Errorf("getCalls = %d, want exactly %d (one re-resolve per attempt)", store.getCalls, maxFinalizeDraftRetries)
	}
}

// TestRecordExecutionResult_FinalizeDraftRetryCap_SucceedsAfterOneRetry is
// the well-behaved case: a store that loses the race exactly once, then
// succeeds, must behave identically to today — a normal ActionFinalizedDraft
// result, well within the retry cap.
func TestRecordExecutionResult_FinalizeDraftRetryCap_SucceedsAfterOneRetry(t *testing.T) {
	entityID := uuid.New()
	draftID := uuid.New()
	store := newRetryCapStore(t, draftID, 1) // fails once, then succeeds

	got, action, _, err := RecordExecutionResult(context.Background(), store, CreateOutcomeParams{
		EntityType: "task",
		EntityID:   entityID,
		Result:     "success",
		Notes:      "shipped after one retry",
	})
	if err != nil {
		t.Fatalf("RecordExecutionResult: %v", err)
	}
	if action != ActionFinalizedDraft {
		t.Errorf("action = %q, want %q", action, ActionFinalizedDraft)
	}
	if got.ID != draftID {
		t.Errorf("ID = %s, want %s", got.ID, draftID)
	}
	if got.Notes != "shipped after one retry" {
		t.Errorf("Notes = %q, want the successful call's own notes", got.Notes)
	}
	if store.finalizeCalls != 2 {
		t.Errorf("finalizeCalls = %d, want exactly 2 (one failed race, one success)", store.finalizeCalls)
	}
	if store.getCalls != 2 {
		t.Errorf("getCalls = %d, want exactly 2 (one re-resolve per attempt)", store.getCalls)
	}
}

// ---------------------------------------------------------------------------
// PR #152 round 6 Major M-R6-1: a supersede call's previousNotes must be ""
// (treated like a fresh row), never the PRIOR row's Notes — see
// ActionSuperseded's doc comment for the full rationale. fakeStore's
// CreateOutcome (above) returns a NEW row (fresh uuid.New() ID) carrying
// params.Notes verbatim, mirroring both real backends' supersede path
// exactly enough to exercise RecordExecutionResult's own branch selection
// and return value in isolation from any DB.
// ---------------------------------------------------------------------------

// TestRecordExecutionResult_Supersede_ReturnsEmptyPreviousNotes is the
// direct reproduction: latest is a TERMINAL outcome ("success") with real
// Notes; this call supplies a DIFFERENT terminal result ("regressed") but
// BYTE-IDENTICAL Notes — the "same summary, different verdict" shape a
// re-evaluation naturally produces. Before the fix, previousNotes was
// latest.Notes, so it compared equal to the new row's (identical) Notes at
// tools_outcome.go's atomize gate and silently skipped atomize for content
// that had never been atomized under the NEW row's outcome_id.
func TestRecordExecutionResult_Supersede_ReturnsEmptyPreviousNotes(t *testing.T) {
	entityID := uuid.New()
	priorID := uuid.New()
	sameNotes := "looked fine at review time"

	fs := &fakeStore{
		t:         t,
		hasLatest: true,
		latest: Outcome{
			ID:         priorID,
			EntityType: "task",
			EntityID:   entityID,
			Result:     "success",
			Notes:      sameNotes,
		},
	}

	got, action, previousNotes, err := RecordExecutionResult(context.Background(), fs, CreateOutcomeParams{
		EntityType: "task",
		EntityID:   entityID,
		Result:     "regressed",
		Notes:      sameNotes,
	})
	if err != nil {
		t.Fatalf("RecordExecutionResult: %v", err)
	}
	if action != ActionSuperseded {
		t.Fatalf("action = %q, want %q", action, ActionSuperseded)
	}
	if got.ID == priorID {
		t.Fatalf("supersede must create a NEW row, got the same ID as the prior row")
	}
	if !fs.createCalled || fs.finalizeCalled {
		t.Errorf("expected CreateOutcome (not FinalizeDraft) to be called for a supersede")
	}
	if got.Notes != sameNotes {
		t.Fatalf("new row's Notes = %q, want %q (the call's own supplied notes)", got.Notes, sameNotes)
	}
	if previousNotes != "" {
		t.Errorf("previousNotes = %q, want \"\" (M-R6-1 regression: a supersede row must be treated "+
			"like a fresh row for the atomize gate, never compared against the PRIOR row's Notes)", previousNotes)
	}
}

// TestRecordExecutionResult_Supersede_EmptyNotes_PreviousNotesStillEmpty is
// M-R6-1's reverse case: a supersede call that supplies NO notes at all must
// still report previousNotes == "" (consistent with ActionCreated), and the
// new row's own Notes is empty — so tools_outcome.go's
// `o.Notes != "" && o.Notes != previousNotes` gate correctly skips atomize
// on the FIRST condition (nothing to atomize), not because previousNotes
// happened to match.
func TestRecordExecutionResult_Supersede_EmptyNotes_PreviousNotesStillEmpty(t *testing.T) {
	entityID := uuid.New()
	priorID := uuid.New()

	fs := &fakeStore{
		t:         t,
		hasLatest: true,
		latest: Outcome{
			ID:         priorID,
			EntityType: "task",
			EntityID:   entityID,
			Result:     "success",
			Notes:      "the original postmortem, must not be touched",
		},
	}

	got, action, previousNotes, err := RecordExecutionResult(context.Background(), fs, CreateOutcomeParams{
		EntityType: "task",
		EntityID:   entityID,
		Result:     "regressed",
		// Notes deliberately empty.
	})
	if err != nil {
		t.Fatalf("RecordExecutionResult: %v", err)
	}
	if action != ActionSuperseded {
		t.Fatalf("action = %q, want %q", action, ActionSuperseded)
	}
	if got.Notes != "" {
		t.Fatalf("new row's Notes = %q, want empty (this call supplied none)", got.Notes)
	}
	if previousNotes != "" {
		t.Errorf("previousNotes = %q, want \"\"", previousNotes)
	}
	// The prior row's real content must remain completely untouched — a
	// supersede never mutates the row it supersedes.
	if fs.latest.Notes != "the original postmortem, must not be touched" {
		t.Errorf("prior row's Notes was mutated: %q", fs.latest.Notes)
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

// ---------------------------------------------------------------------------
// PR #152 round 4 Major (finding F4): notesHasNewContent used to compare
// incoming only against the TRAILING "\n\n"-delimited segment of existing
// (strings.HasSuffix), so an earlier segment resubmitted out of order —
// e.g. an interleaved AAA, BBB, AAA resend sequence — was never recognized
// as a replay, letting the caller re-append duplicate content and re-fire
// FinalizeDraft's side effects indefinitely. slices.Contains against every
// segment fixes this regardless of resend order.
// ---------------------------------------------------------------------------

// TestNotesHasNewContent_InterleavedSegments is the direct unit-level
// reproduction: existing already contains both "AAA" and "BBB" as separate
// "\n\n"-delimited segments (not just the trailing one), and every
// already-present segment — not only the last — must be recognized as
// carrying no new content.
func TestNotesHasNewContent_InterleavedSegments(t *testing.T) {
	const existing = "AAA\n\nBBB"

	tests := []struct {
		name     string
		incoming string
		want     bool
	}{
		{"trailing segment resent is a replay", "BBB", false},
		{"NON-trailing (first) segment resent is ALSO a replay", "AAA", false},
		{"genuinely new content is not a replay", "CCC", true},
		{"empty incoming carries no content", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := notesHasNewContent(existing, tc.incoming)
			if got != tc.want {
				t.Errorf("notesHasNewContent(%q, %q) = %v, want %v", existing, tc.incoming, got, tc.want)
			}
		})
	}
}

// TestRecordExecutionResult_DraftEnrichRetry_InterleavedResendDetectedAsReplay
// is the RecordExecutionResult-level reproduction pinned by the dispatch
// acceptance criteria: AAA -> BBB -> AAA must classify the THIRD call as
// ActionReplayedIdempotent (not a third ActionDraftEnriched re-write). Before
// the fix, this sequence reported draft_enriched on every call because the
// second AAA is not the trailing segment of "AAA\n\nBBB".
func TestRecordExecutionResult_DraftEnrichRetry_InterleavedResendDetectedAsReplay(t *testing.T) {
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
	paramsFor := func(notes string) CreateOutcomeParams {
		return CreateOutcomeParams{
			EntityType: "task",
			EntityID:   entityID,
			Result:     resultUnknown,
			Notes:      notes,
		}
	}

	// Call 1: AAA against an empty draft — genuinely new, must write.
	_, action1, _, err := RecordExecutionResult(context.Background(), fs, paramsFor("AAA"))
	if err != nil {
		t.Fatalf("call 1 (AAA): %v", err)
	}
	if action1 != ActionDraftEnriched {
		t.Fatalf("call 1 action = %q, want %q", action1, ActionDraftEnriched)
	}
	if fs.latest.Notes != "AAA" {
		t.Fatalf("after call 1, latest.Notes = %q, want %q", fs.latest.Notes, "AAA")
	}

	// Call 2: BBB — a genuinely new second segment, must write.
	fs.finalizeCalled = false
	_, action2, _, err := RecordExecutionResult(context.Background(), fs, paramsFor("BBB"))
	if err != nil {
		t.Fatalf("call 2 (BBB): %v", err)
	}
	if action2 != ActionDraftEnriched {
		t.Fatalf("call 2 action = %q, want %q", action2, ActionDraftEnriched)
	}
	if fs.latest.Notes != "AAA\n\nBBB" {
		t.Fatalf("after call 2, latest.Notes = %q, want %q", fs.latest.Notes, "AAA\n\nBBB")
	}

	// Call 3: AAA again — already present as the FIRST (non-trailing)
	// segment. Must be detected as a replay: no write, no side effect fire.
	fs.finalizeCalled = false
	third, action3, _, err := RecordExecutionResult(context.Background(), fs, paramsFor("AAA"))
	if err != nil {
		t.Fatalf("call 3 (AAA again): %v", err)
	}
	if action3 != ActionReplayedIdempotent {
		t.Errorf("call 3 action = %q, want %q (interleaved resend of a non-trailing segment must be idempotent)",
			action3, ActionReplayedIdempotent)
	}
	if fs.finalizeCalled {
		t.Error("FinalizeDraft must NOT be called a third time — the resent segment carries no new information")
	}
	if third.Notes != "AAA\n\nBBB" {
		t.Errorf("call 3 must not alter stored Notes: got %q, want unchanged %q", third.Notes, "AAA\n\nBBB")
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

	first, action1, _, err := RecordExecutionResult(context.Background(), fs, params)
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

	second, action2, _, err := RecordExecutionResult(context.Background(), fs, params)
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

	got, action, _, err := RecordExecutionResult(context.Background(), fs, CreateOutcomeParams{
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

	got, action, _, err := RecordExecutionResult(context.Background(), fs, CreateOutcomeParams{
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

// mergeMetricsExistingWins mirrors FinalizeDraft's metrics rule for the fake
// store: incoming keys are merged in, but a key already present in existing
// keeps its existing value (append-only — a call can add facts, never rewrite
// them). Extracted from fakeStore.FinalizeDraft to keep that method under the
// cyclomatic-complexity gate.
func mergeMetricsExistingWins(existing, incoming []byte) []byte {
	if len(incoming) == 0 {
		return existing
	}
	var in map[string]json.RawMessage
	if err := json.Unmarshal(incoming, &in); err != nil {
		return existing
	}
	merged := make(map[string]json.RawMessage, len(in))
	maps.Copy(merged, in)
	if len(existing) > 0 {
		var ex map[string]json.RawMessage
		if err := json.Unmarshal(existing, &ex); err == nil {
			maps.Copy(merged, ex) // existing wins on conflicting keys
		}
	}
	out, err := json.Marshal(merged)
	if err != nil {
		return existing
	}
	return out
}

// unionRuleIDs mirrors FinalizeDraft's related_rule_ids rule for the fake
// store: the union of existing and incoming, de-duplicated, existing order
// preserved first. An empty incoming list leaves existing untouched.
func unionRuleIDs(existing, incoming []uuid.UUID) []uuid.UUID {
	if len(incoming) == 0 {
		return existing
	}
	seen := make(map[uuid.UUID]bool, len(existing)+len(incoming))
	merged := make([]uuid.UUID, 0, len(existing)+len(incoming))
	for _, id := range append(append([]uuid.UUID{}, existing...), incoming...) {
		if !seen[id] {
			seen[id] = true
			merged = append(merged, id)
		}
	}
	return merged
}
