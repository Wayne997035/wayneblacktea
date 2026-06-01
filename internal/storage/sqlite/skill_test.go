package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/skill"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
)

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

// TestSkillStore_GetByID verifies ErrNotFound for unknown ID and happy path.
func TestSkillStore_GetByID(t *testing.T) {
	db := newSkillTestDB(t)
	store := wbtsqlite.NewSkillStore(db)
	ctx := context.Background()

	t.Run("ErrNotFound for unknown ID", func(t *testing.T) {
		_, err := store.GetByID(ctx, "no-such-id", nil)
		if !errors.Is(err, skill.ErrNotFound) {
			t.Errorf("want ErrNotFound, got %v", err)
		}
	})

	t.Run("returns inserted skill", func(t *testing.T) {
		sk, err := store.Add(ctx, skill.AddParams{Name: "lookup-me"})
		if err != nil {
			t.Fatalf("Add: %v", err)
		}
		got, err := store.GetByID(ctx, sk.ID, nil)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.Name != "lookup-me" {
			t.Errorf("Name: got %q, want %q", got.Name, "lookup-me")
		}
	})
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
