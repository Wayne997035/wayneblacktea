package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/skill"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
)

// exampleOutcomeIDs extracts the "outcome_id" field from each examples entry,
// preserving order. Entries round-trip through JSON as map[string]interface{}.
func exampleOutcomeIDs(examples []any) []string {
	ids := make([]string, 0, len(examples))
	for _, e := range examples {
		m, ok := e.(map[string]interface{})
		if !ok {
			continue
		}
		if id, ok := m["outcome_id"].(string); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

func newSkillTestDB(t *testing.T) *wbtsqlite.DB {
	t.Helper()
	db, err := wbtsqlite.Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("open :memory: DB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestSkillStore_Add verifies Add inserts a skill and returns it with defaults.
func TestSkillStore_Add(t *testing.T) {
	db := newSkillTestDB(t)
	store := wbtsqlite.NewSkillStore(db)
	ctx := context.Background()

	tests := []struct {
		name    string
		params  skill.AddParams
		wantErr bool
	}{
		{
			name: "happy path with all fields",
			params: skill.AddParams{
				Name:                  "feature-skill",
				Description:           "End-to-end feature delivery",
				Triggers:              []string{"new feature request"},
				Steps:                 []string{"plan", "implement", "test"},
				FailureModes:          []string{"missing tests"},
				VerificationChecklist: []string{"task check passes"},
				Examples:              []any{map[string]string{"note": "PR #1"}},
				SourceAtomIDs:         []string{"atom-1"},
			},
		},
		{
			name: "minimal fields",
			params: skill.AddParams{
				Name: "minimal",
			},
		},
		{
			name: "nil slices become empty slices",
			params: skill.AddParams{
				Name:     "nil-slices",
				Triggers: nil,
				Steps:    nil,
				Examples: nil,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sk, err := store.Add(ctx, tc.params)
			if tc.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Add: %v", err)
			}
			if sk.ID == "" {
				t.Error("ID should not be empty")
			}
			if sk.Name != tc.params.Name {
				t.Errorf("Name: got %q, want %q", sk.Name, tc.params.Name)
			}
			if sk.Triggers == nil {
				t.Error("Triggers should not be nil after Add")
			}
			if sk.Steps == nil {
				t.Error("Steps should not be nil after Add")
			}
			if sk.Examples == nil {
				t.Error("Examples should not be nil after Add")
			}
			if sk.SuccessCount != 0 {
				t.Errorf("initial SuccessCount: got %d, want 0", sk.SuccessCount)
			}
		})
	}
}

// TestSkillStore_Search verifies LIKE-based search and empty-result behaviour.
func TestSkillStore_Search(t *testing.T) {
	db := newSkillTestDB(t)
	store := wbtsqlite.NewSkillStore(db)
	ctx := context.Background()

	uniqueToken := fmt.Sprintf("xsql-%d", len("test"))
	_, err := store.Add(ctx, skill.AddParams{
		Name:        uniqueToken + "-skill",
		Description: "desc " + uniqueToken,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	t.Run("returns skill on name match", func(t *testing.T) {
		results, err := store.Search(ctx, skill.SearchFilter{Query: uniqueToken, Limit: 10})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(results) == 0 {
			t.Error("expected at least one result")
		}
	})

	t.Run("returns empty slice on no match", func(t *testing.T) {
		results, err := store.Search(ctx, skill.SearchFilter{Query: "zzznomatch9999", Limit: 10})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if results == nil {
			t.Error("Search should return non-nil slice on no match")
		}
		if len(results) != 0 {
			t.Errorf("expected 0 results, got %d", len(results))
		}
	})
}

// TestSkillStore_IncrementSuccess verifies counter increment and ErrNotFound.
func TestSkillStore_IncrementSuccess(t *testing.T) {
	db := newSkillTestDB(t)
	store := wbtsqlite.NewSkillStore(db)
	ctx := context.Background()

	t.Run("increments success_count and sets last_used_at", func(t *testing.T) {
		sk, err := store.Add(ctx, skill.AddParams{Name: "inc-skill"})
		if err != nil {
			t.Fatalf("Add: %v", err)
		}
		updated, err := store.IncrementSuccess(ctx, sk.ID, nil)
		if err != nil {
			t.Fatalf("IncrementSuccess: %v", err)
		}
		if updated.SuccessCount != 1 {
			t.Errorf("SuccessCount: got %d, want 1", updated.SuccessCount)
		}
		if updated.LastUsedAt == nil {
			t.Error("LastUsedAt should be set")
		}
	})

	t.Run("ErrNotFound for unknown ID", func(t *testing.T) {
		_, err := store.IncrementSuccess(ctx, "unknown-id", nil)
		if !errors.Is(err, skill.ErrNotFound) {
			t.Errorf("want ErrNotFound, got %v", err)
		}
	})
}

// TestSkillStore_UpdateFromOutcome verifies outcome tracking and counter increments.
func TestSkillStore_UpdateFromOutcome(t *testing.T) {
	db := newSkillTestDB(t)
	store := wbtsqlite.NewSkillStore(db)
	ctx := context.Background()

	t.Run("success outcome increments success_count", func(t *testing.T) {
		sk, err := store.Add(ctx, skill.AddParams{Name: "outcome-skill"})
		if err != nil {
			t.Fatalf("Add: %v", err)
		}
		updated, err := store.UpdateFromOutcome(ctx, skill.UpdateFromOutcomeParams{
			SkillID:   sk.ID,
			OutcomeID: "out-123",
			Success:   true,
			Notes:     "worked great",
		}, nil)
		if err != nil {
			t.Fatalf("UpdateFromOutcome: %v", err)
		}
		if updated.SuccessCount != 1 {
			t.Errorf("SuccessCount: got %d, want 1", updated.SuccessCount)
		}
		if len(updated.Examples) == 0 {
			t.Error("examples should contain the new entry")
		}
	})

	t.Run("failure outcome increments failure_count", func(t *testing.T) {
		sk, err := store.Add(ctx, skill.AddParams{Name: "fail-skill"})
		if err != nil {
			t.Fatalf("Add: %v", err)
		}
		updated, err := store.UpdateFromOutcome(ctx, skill.UpdateFromOutcomeParams{
			SkillID: sk.ID,
			Success: false,
			Notes:   "something broke",
		}, nil)
		if err != nil {
			t.Fatalf("UpdateFromOutcome: %v", err)
		}
		if updated.FailureCount != 1 {
			t.Errorf("FailureCount: got %d, want 1", updated.FailureCount)
		}
	})

	t.Run("ErrNotFound for unknown ID", func(t *testing.T) {
		_, err := store.UpdateFromOutcome(ctx, skill.UpdateFromOutcomeParams{
			SkillID: "no-such-id",
			Success: true,
		}, nil)
		if !errors.Is(err, skill.ErrNotFound) {
			t.Errorf("want ErrNotFound, got %v", err)
		}
	})
}

// TestF0906_SQLite_UpdateFromOutcome_CapsAt20_SuccessPath is the [F0906-13]
// path-independent regression test for the SQLite [F0906-11] FIFO cap on the
// success path: 25 consecutive successful outcomes leave exactly the most
// recent 20 examples, oldest to newest, with the first 5 outcome_ids dropped.
func TestF0906_SQLite_UpdateFromOutcome_CapsAt20_SuccessPath(t *testing.T) {
	db := newSkillTestDB(t)
	store := wbtsqlite.NewSkillStore(db)
	ctx := context.Background()

	sk, err := store.Add(ctx, skill.AddParams{Name: "f0906-success-cap"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	var updated *skill.Skill
	for i := 1; i <= 25; i++ {
		updated, err = store.UpdateFromOutcome(ctx, skill.UpdateFromOutcomeParams{
			SkillID:   sk.ID,
			OutcomeID: fmt.Sprintf("e%02d", i),
			Success:   true,
			Notes:     "note",
		}, nil)
		if err != nil {
			t.Fatalf("UpdateFromOutcome #%d: %v", i, err)
		}
	}

	if updated.SuccessCount != 25 {
		t.Errorf("SuccessCount: got %d, want 25", updated.SuccessCount)
	}
	got := exampleOutcomeIDs(updated.Examples)
	want := make([]string, 20)
	for i := range want {
		want[i] = fmt.Sprintf("e%02d", i+6) // e06..e25, oldest to newest
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Examples outcome_ids: got %v, want %v", got, want)
	}
}

// TestF0906_SQLite_UpdateFromOutcome_CapsAt20_FailurePath is the [F0906-13]
// path-independent regression test for the SQLite [F0906-11] FIFO cap on the
// failure path. Kept as its own group even though it is mechanically
// guaranteed to pass/fail together with the success-path test above — both
// go through the single skill.go:250 append point — because dropping it
// would require re-deriving the mutation table in acceptance ②; see the
// dispatch doc for the full rationale.
func TestF0906_SQLite_UpdateFromOutcome_CapsAt20_FailurePath(t *testing.T) {
	db := newSkillTestDB(t)
	store := wbtsqlite.NewSkillStore(db)
	ctx := context.Background()

	sk, err := store.Add(ctx, skill.AddParams{Name: "f0906-failure-cap"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	var updated *skill.Skill
	for i := 1; i <= 25; i++ {
		updated, err = store.UpdateFromOutcome(ctx, skill.UpdateFromOutcomeParams{
			SkillID:   sk.ID,
			OutcomeID: fmt.Sprintf("e%02d", i),
			Success:   false,
			Notes:     "note",
		}, nil)
		if err != nil {
			t.Fatalf("UpdateFromOutcome #%d: %v", i, err)
		}
	}

	if updated.FailureCount != 25 {
		t.Errorf("FailureCount: got %d, want 25", updated.FailureCount)
	}
	got := exampleOutcomeIDs(updated.Examples)
	want := make([]string, 20)
	for i := range want {
		want[i] = fmt.Sprintf("e%02d", i+6) // e06..e25, oldest to newest
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Examples outcome_ids: got %v, want %v", got, want)
	}
}
