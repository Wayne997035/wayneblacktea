//go:build integration

package playbook_test

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

	"github.com/Wayne997035/wayneblacktea/internal/playbook"
	migrationfs "github.com/Wayne997035/wayneblacktea/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

var playbookSkipMigrations = map[string]bool{
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
	c, err := tcpostgres.Run(ctx,
		"pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("wbt_playbook_test"),
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

	applyPlaybookUpMigrationsOnce(ctx, pool)

	testPgPool = pool
	return m.Run()
}

func applyPlaybookUpMigrationsOnce(ctx context.Context, pool *pgxpool.Pool) {
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
		if playbookSkipMigrations[name] {
			log.Printf("applyPlaybookUpMigrations: skipping %s", name)
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

func openPlaybookTestPgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode (requires Docker)")
	}
	return testPgPool
}

func TestStorePostgres_CreatePersistsAllFields(t *testing.T) {
	wsID := uuid.New()
	srcID := uuid.New()
	store := playbook.NewStore(openPlaybookTestPgPool(t), &wsID)

	got, err := store.Create(context.Background(), playbook.CreateParams{
		TriggerPattern:    "before release",
		ActionTemplate:    "run task check",
		SourceDecisionIDs: []uuid.UUID{srcID},
		Confidence:        0.87,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.WorkspaceID == nil || *got.WorkspaceID != wsID {
		t.Fatalf("WorkspaceID = %v, want %s", got.WorkspaceID, wsID)
	}
	if got.TriggerPattern != "before release" || got.ActionTemplate != "run task check" {
		t.Fatalf("persisted playbook = %+v", got)
	}
	if len(got.SourceDecisionIDs) != 1 || got.SourceDecisionIDs[0] != srcID {
		t.Fatalf("SourceDecisionIDs = %v, want [%s]", got.SourceDecisionIDs, srcID)
	}
	if got.Confidence < 0.86 || got.Confidence > 0.88 {
		t.Fatalf("Confidence = %v, want about 0.87", got.Confidence)
	}
}

func TestStorePostgres_ListFiltersByWorkspaceID(t *testing.T) {
	pool := openPlaybookTestPgPool(t)
	ctx := context.Background()
	wsA := uuid.New()
	wsB := uuid.New()
	storeA := playbook.NewStore(pool, &wsA)
	storeB := playbook.NewStore(pool, &wsB)

	if _, err := storeA.Create(ctx, playbook.CreateParams{TriggerPattern: "alpha", ActionTemplate: "do a"}); err != nil {
		t.Fatalf("create A: %v", err)
	}
	if _, err := storeB.Create(ctx, playbook.CreateParams{TriggerPattern: "beta", ActionTemplate: "do b"}); err != nil {
		t.Fatalf("create B: %v", err)
	}

	got, err := storeA.List(ctx, playbook.ListParams{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].TriggerPattern != "alpha" {
		t.Fatalf("workspace A list = %+v, want only alpha", got)
	}
}

func TestStorePostgres_ListKeywordsORCombineAndCaseInsensitive(t *testing.T) {
	store := playbook.NewStore(openPlaybookTestPgPool(t), nil)
	ctx := context.Background()
	if _, err := store.Create(ctx, playbook.CreateParams{TriggerPattern: "Before Complex Task", ActionTemplate: "query playbooks"}); err != nil {
		t.Fatalf("create complex: %v", err)
	}
	if _, err := store.Create(ctx, playbook.CreateParams{TriggerPattern: "release", ActionTemplate: "RUN task check"}); err != nil {
		t.Fatalf("create release: %v", err)
	}
	if _, err := store.Create(ctx, playbook.CreateParams{TriggerPattern: "unrelated", ActionTemplate: "ignore"}); err != nil {
		t.Fatalf("create unrelated: %v", err)
	}

	got, err := store.List(ctx, playbook.ListParams{ContextKeywords: []string{"complex", "run task"}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d playbooks, want 2: %+v", len(got), got)
	}
}

func TestStorePostgres_ListLimitDefaultsTo20AndOrdersByConfidenceDesc(t *testing.T) {
	store := playbook.NewStore(openPlaybookTestPgPool(t), nil)
	ctx := context.Background()
	for i := 0; i < 25; i++ {
		if _, err := store.Create(ctx, playbook.CreateParams{
			TriggerPattern: fmt.Sprintf("pattern-%02d", i),
			ActionTemplate: "act",
			Confidence:     float64(i) / 100,
		}); err != nil {
			t.Fatalf("Create[%d]: %v", i, err)
		}
	}

	got, err := store.List(ctx, playbook.ListParams{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 20 {
		t.Fatalf("len = %d, want default limit 20", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Confidence < got[i].Confidence {
			t.Fatalf("not sorted desc at %d: %v < %v", i, got[i-1].Confidence, got[i].Confidence)
		}
	}
}

func TestStorePostgres_IncrementHitsAtomicCrossWorkspaceBlocked(t *testing.T) {
	pool := openPlaybookTestPgPool(t)
	ctx := context.Background()
	wsA := uuid.New()
	wsB := uuid.New()
	storeA := playbook.NewStore(pool, &wsA)
	storeB := playbook.NewStore(pool, &wsB)

	created, err := storeA.Create(ctx, playbook.CreateParams{TriggerPattern: "ship", ActionTemplate: "verify"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := storeB.IncrementHits(ctx, created.ID); !errors.Is(err, playbook.ErrNotFound) {
		t.Fatalf("IncrementHits cross-workspace err = %v, want ErrNotFound", err)
	}
	if err := storeA.IncrementHits(ctx, created.ID); err != nil {
		t.Fatalf("IncrementHits owner: %v", err)
	}
	if err := storeA.IncrementHits(ctx, created.ID); err != nil {
		t.Fatalf("IncrementHits owner second: %v", err)
	}

	got, err := storeA.List(ctx, playbook.ListParams{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Hits != 2 || got[0].LastUsedAt == nil {
		t.Fatalf("after increments = %+v, want hits=2 and last_used_at", got)
	}
}
