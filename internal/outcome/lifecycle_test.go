package outcome

import (
	"context"
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

func (f *fakeStore) FinalizeDraft(_ context.Context, _ uuid.UUID, params CreateOutcomeParams) (Outcome, error) {
	f.finalizeCalled = true
	f.latest.Result = params.Result
	f.latest.Notes = params.Notes
	f.latest.Metrics = params.Metrics
	f.latest.RelatedRuleIDs = params.RelatedRuleIDs
	f.latest.WorkSessionID = params.WorkSessionID
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
