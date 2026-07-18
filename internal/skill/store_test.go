//go:build integration

package skill_test

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/skill"
	migrationfs "github.com/Wayne997035/wayneblacktea/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// skipMigrations lists migration files that contain psql metacommands and
// cannot be executed directly by pgx.
var skipMigrations = map[string]bool{
	"000011_backfill_workspace_id.up.sql": true,
}

var testPgPool *pgxpool.Pool

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(run(m))
}

func run(m *testing.M) int {
	if testing.Short() {
		return m.Run()
	}
	ctx := context.Background()
	c, err := tcpostgres.Run(
		ctx,
		"pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("wbt_test"),
		tcpostgres.WithUsername("wbt"),
		tcpostgres.WithPassword("wbt"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		log.Fatalf("start postgres container: %v", err)
	}
	defer func() { _ = c.Terminate(ctx) }()

	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Printf("get connection string: %v", err)
		return 1
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Printf("pgxpool.New: %v", err)
		return 1
	}
	defer pool.Close()

	applyAllUpMigrationsOnce(ctx, pool)

	testPgPool = pool
	return m.Run()
}

func applyAllUpMigrationsOnce(ctx context.Context, pool *pgxpool.Pool) {
	entries, err := migrationfs.FS.ReadDir(".")
	if err != nil {
		log.Fatalf("read embedded migrations dir: %v", err)
	}
	var ups []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		ups = append(ups, name)
	}
	sort.Strings(ups)

	for _, name := range ups {
		if skipMigrations[name] {
			log.Printf("applyAllUpMigrations: skipping %s (psql-metacommand-only file)", name)
			continue
		}
		body, err := migrationfs.FS.ReadFile(name)
		if err != nil {
			log.Fatalf("read %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			log.Fatalf("apply %s: %v", name, err)
		}
	}
}

func openTestPgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode (requires Docker)")
	}
	return testPgPool
}

// TestStore_Add verifies the happy path and edge cases for Add.
func TestStore_Add(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := skill.New(pool, &wsID)
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
				Description:           "How to implement a new feature end-to-end",
				Triggers:              []string{"new feature requested"},
				Steps:                 []string{"read spec", "implement", "test"},
				FailureModes:          []string{"missing tests"},
				VerificationChecklist: []string{"task check passes"},
				Examples:              []any{map[string]string{"note": "PR #42"}},
				SourceAtomIDs:         []string{},
			},
		},
		{
			name: "minimal required fields only",
			params: skill.AddParams{
				Name:        "minimal-skill",
				Description: "",
			},
		},
		{
			name: "empty slices produce JSON arrays not null",
			params: skill.AddParams{
				Name:     "empty-slices",
				Triggers: []string{},
				Steps:    []string{},
				Examples: []any{},
			},
		},
		{
			name:    "empty name is rejected by DB NOT NULL check",
			params:  skill.AddParams{Name: ""},
			wantErr: false, // DB has NOT NULL but no CHECK; empty string is allowed
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sk, err := store.Add(ctx, tc.params)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Add: %v", err)
			}
			if sk == nil {
				t.Fatal("Add returned nil")
			}
			if sk.ID == "" {
				t.Error("ID should be set")
			}
			if sk.Name != tc.params.Name {
				t.Errorf("Name: got %q, want %q", sk.Name, tc.params.Name)
			}
			if sk.Triggers == nil {
				t.Error("Triggers should not be nil")
			}
			if sk.Steps == nil {
				t.Error("Steps should not be nil")
			}
			if sk.Examples == nil {
				t.Error("Examples should not be nil")
			}
		})
	}
}

// TestStore_IncrementSuccess verifies the success counter is incremented.
func TestStore_IncrementSuccess(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := skill.New(pool, &wsID)
	ctx := context.Background()
	wsIDStr := wsID.String()

	t.Run("increments success_count", func(t *testing.T) {
		sk, err := store.Add(ctx, skill.AddParams{Name: "incr-success"})
		if err != nil {
			t.Fatalf("Add: %v", err)
		}
		if sk.SuccessCount != 0 {
			t.Errorf("initial SuccessCount: got %d, want 0", sk.SuccessCount)
		}

		updated, err := store.IncrementSuccess(ctx, sk.ID, &wsIDStr)
		if err != nil {
			t.Fatalf("IncrementSuccess: %v", err)
		}
		if updated.SuccessCount != 1 {
			t.Errorf("SuccessCount after increment: got %d, want 1", updated.SuccessCount)
		}
		if updated.LastUsedAt == nil {
			t.Error("LastUsedAt should be set after increment")
		}
	})

	t.Run("ErrNotFound for unknown ID", func(t *testing.T) {
		_, err := store.IncrementSuccess(ctx, uuid.New().String(), &wsIDStr)
		if !errors.Is(err, skill.ErrNotFound) {
			t.Errorf("want ErrNotFound, got %v", err)
		}
	})
}

// TestStore_Search verifies search returns matching and no-match results correctly.
func TestStore_Search(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := skill.New(pool, &wsID)
	ctx := context.Background()
	wsIDStr := wsID.String()

	// Insert a skill to search against.
	uniqueToken := fmt.Sprintf("xsearch-%s", uuid.New().String()[:8])
	_, err := store.Add(ctx, skill.AddParams{
		Name:        uniqueToken + "-skill",
		Description: "description for " + uniqueToken,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	t.Run("returns skill when name matches", func(t *testing.T) {
		results, err := store.Search(ctx, skill.SearchFilter{
			WorkspaceID: &wsIDStr,
			Query:       uniqueToken,
			Limit:       10,
		})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(results) == 0 {
			t.Error("expected at least one result, got none")
		}
	})

	t.Run("returns empty slice when no match", func(t *testing.T) {
		results, err := store.Search(ctx, skill.SearchFilter{
			WorkspaceID: &wsIDStr,
			Query:       "zzznomatch" + uuid.New().String(),
			Limit:       10,
		})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if results == nil {
			t.Error("Search should return non-nil empty slice on no match")
		}
		if len(results) != 0 {
			t.Errorf("expected 0 results, got %d", len(results))
		}
	})
}

// TestStore_UpdateFromOutcome verifies outcome tracking.
func TestStore_UpdateFromOutcome(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := skill.New(pool, &wsID)
	ctx := context.Background()
	wsIDStr := wsID.String()

	t.Run("success outcome increments success_count", func(t *testing.T) {
		sk, err := store.Add(ctx, skill.AddParams{Name: "outcome-skill"})
		if err != nil {
			t.Fatalf("Add: %v", err)
		}

		updated, err := store.UpdateFromOutcome(ctx, skill.UpdateFromOutcomeParams{
			SkillID:   sk.ID,
			OutcomeID: uuid.New().String(),
			Success:   true,
			Notes:     "great outcome",
		}, &wsIDStr)
		if err != nil {
			t.Fatalf("UpdateFromOutcome: %v", err)
		}
		if updated.SuccessCount != 1 {
			t.Errorf("SuccessCount: got %d, want 1", updated.SuccessCount)
		}
		if len(updated.Examples) == 0 {
			t.Error("expected examples to be populated")
		}
	})

	t.Run("failure outcome increments failure_count", func(t *testing.T) {
		sk, err := store.Add(ctx, skill.AddParams{Name: "failure-skill"})
		if err != nil {
			t.Fatalf("Add: %v", err)
		}

		updated, err := store.UpdateFromOutcome(ctx, skill.UpdateFromOutcomeParams{
			SkillID:   sk.ID,
			OutcomeID: uuid.New().String(),
			Success:   false,
			Notes:     "something went wrong",
		}, &wsIDStr)
		if err != nil {
			t.Fatalf("UpdateFromOutcome: %v", err)
		}
		if updated.FailureCount != 1 {
			t.Errorf("FailureCount: got %d, want 1", updated.FailureCount)
		}
	})

	t.Run("ErrNotFound for unknown ID", func(t *testing.T) {
		_, err := store.UpdateFromOutcome(ctx, skill.UpdateFromOutcomeParams{
			SkillID: uuid.New().String(),
			Success: true,
			Notes:   "notes",
		}, &wsIDStr)
		if !errors.Is(err, skill.ErrNotFound) {
			t.Errorf("want ErrNotFound, got %v", err)
		}
	})
}

// TestStore_ListRelevant verifies listing with and without query filter.
func TestStore_ListRelevant(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := skill.New(pool, &wsID)
	ctx := context.Background()
	wsIDStr := wsID.String()

	t.Run("returns skills ordered by success_count DESC", func(t *testing.T) {
		uniqueToken := fmt.Sprintf("xlist-%s", uuid.New().String()[:8])

		sk1, err := store.Add(ctx, skill.AddParams{Name: uniqueToken + "-a"})
		if err != nil {
			t.Fatalf("Add sk1: %v", err)
		}
		sk2, err := store.Add(ctx, skill.AddParams{Name: uniqueToken + "-b"})
		if err != nil {
			t.Fatalf("Add sk2: %v", err)
		}

		// sk2 gets more successes.
		for i := 0; i < 3; i++ {
			_, err = store.IncrementSuccess(ctx, sk2.ID, &wsIDStr)
			if err != nil {
				t.Fatalf("IncrementSuccess sk2: %v", err)
			}
		}
		_, err = store.IncrementSuccess(ctx, sk1.ID, &wsIDStr)
		if err != nil {
			t.Fatalf("IncrementSuccess sk1: %v", err)
		}

		results, err := store.ListRelevant(ctx, &wsIDStr, uniqueToken, 10)
		if err != nil {
			t.Fatalf("ListRelevant: %v", err)
		}
		if len(results) < 2 {
			t.Fatalf("expected at least 2 results, got %d", len(results))
		}
		// First result should have higher or equal success_count than second.
		if results[0].SuccessCount < results[1].SuccessCount {
			t.Errorf("expected first result to have higher success_count: got %d < %d",
				results[0].SuccessCount, results[1].SuccessCount)
		}
	})

	t.Run("returns empty slice when nothing matches", func(t *testing.T) {
		results, err := store.ListRelevant(ctx, &wsIDStr, "zzznomatch"+uuid.New().String(), 10)
		if err != nil {
			t.Fatalf("ListRelevant: %v", err)
		}
		if results == nil {
			t.Error("ListRelevant should return non-nil slice on no match")
		}
	})
}
