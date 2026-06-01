//go:build integration

package watchdog_test

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/watchdog"
	migrationfs "github.com/Wayne997035/wayneblacktea/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// skipMigrations are migration files that cannot be run by pgx because they
// contain psql metacommands.
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
	c, err := tcpostgres.Run(ctx,
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

// TestPgDisciplineEventStore_Insert verifies that Insert persists a row and it
// appears in ListUnresolved.
func TestPgDisciplineEventStore_Insert(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := watchdog.NewPgDisciplineEventStore(pool, &wsID)
	ctx := context.Background()

	detail, _ := json.Marshal(map[string]any{"task_id": uuid.New().String()})
	err := store.Insert(ctx, watchdog.InsertParams{
		WorkspaceID: &wsID,
		EventType:   watchdog.EventTypeStuckTask,
		Severity:    watchdog.SeverityWarn,
		Detail:      json.RawMessage(detail),
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	events, err := store.ListUnresolved(ctx, &wsID)
	if err != nil {
		t.Fatalf("ListUnresolved: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected at least one event after Insert, got none")
	}

	found := false
	for _, ev := range events {
		if ev.EventType == watchdog.EventTypeStuckTask && ev.WorkspaceID != nil && *ev.WorkspaceID == wsID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("inserted event not found in ListUnresolved results")
	}
}

// TestPgDisciplineEventStore_ListUnresolved verifies workspace scoping and
// that resolved events are excluded.
func TestPgDisciplineEventStore_ListUnresolved(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	otherWsID := uuid.New()
	store := watchdog.NewPgDisciplineEventStore(pool, &wsID)
	ctx := context.Background()

	// Insert one event in wsID and one in otherWsID.
	if err := store.Insert(ctx, watchdog.InsertParams{
		WorkspaceID: &wsID,
		EventType:   watchdog.EventTypeUnloggedDecision,
		Severity:    watchdog.SeverityWarn,
		Detail:      json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("Insert wsID: %v", err)
	}
	if err := store.Insert(ctx, watchdog.InsertParams{
		WorkspaceID: &otherWsID,
		EventType:   watchdog.EventTypeProposalFail,
		Severity:    watchdog.SeverityInfo,
		Detail:      json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("Insert otherWsID: %v", err)
	}

	// ListUnresolved scoped to wsID must not include the other workspace event.
	events, err := store.ListUnresolved(ctx, &wsID)
	if err != nil {
		t.Fatalf("ListUnresolved: %v", err)
	}
	for _, ev := range events {
		if ev.WorkspaceID != nil && *ev.WorkspaceID == otherWsID {
			t.Errorf("event from other workspace leaked into scoped ListUnresolved")
		}
	}
}

// TestPgDisciplineEventStore_MarkResolved verifies that MarkResolved sets
// resolved_at and the event no longer appears in ListUnresolved.
func TestPgDisciplineEventStore_MarkResolved(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := watchdog.NewPgDisciplineEventStore(pool, &wsID)
	ctx := context.Background()

	if err := store.Insert(ctx, watchdog.InsertParams{
		WorkspaceID: &wsID,
		EventType:   watchdog.EventTypeStaleHandoff,
		Severity:    watchdog.SeverityWarn,
		Detail:      json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	events, err := store.ListUnresolved(ctx, &wsID)
	if err != nil {
		t.Fatalf("ListUnresolved: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected event after Insert")
	}
	eventID := events[0].ID

	// Mark resolved.
	if err := store.MarkResolved(ctx, eventID); err != nil {
		t.Fatalf("MarkResolved: %v", err)
	}

	// Event must not appear in ListUnresolved anymore.
	after, err := store.ListUnresolved(ctx, &wsID)
	if err != nil {
		t.Fatalf("ListUnresolved after resolve: %v", err)
	}
	for _, ev := range after {
		if ev.ID == eventID {
			t.Errorf("resolved event %v still appears in ListUnresolved", eventID)
		}
	}
}

// TestPgDisciplineEventStore_MarkResolved_NotFound verifies that MarkResolved
// returns ErrEventNotFound for a non-existent event ID.
func TestPgDisciplineEventStore_MarkResolved_NotFound(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := watchdog.NewPgDisciplineEventStore(pool, &wsID)
	ctx := context.Background()

	err := store.MarkResolved(ctx, uuid.New())
	if !isErrEventNotFound(err) {
		t.Errorf("expected ErrEventNotFound for non-existent ID, got: %v", err)
	}
}

// TestPgDisciplineEventStore_PruneOlderThan verifies that only rows older
// than the cutoff are deleted.
func TestPgDisciplineEventStore_PruneOlderThan(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := watchdog.NewPgDisciplineEventStore(pool, &wsID)
	ctx := context.Background()

	// Insert two events; both will be older than a future cutoff.
	for i := 0; i < 2; i++ {
		if err := store.Insert(ctx, watchdog.InsertParams{
			WorkspaceID: &wsID,
			EventType:   watchdog.EventTypeRepeatedCorrection,
			Severity:    watchdog.SeverityInfo,
			Detail:      json.RawMessage(`{}`),
		}); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	// Prune with a future cutoff — all just-inserted rows qualify.
	n, err := store.PruneOlderThan(ctx, time.Now().Add(1*time.Minute))
	if err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if n < 2 {
		t.Errorf("expected at least 2 rows deleted, got %d", n)
	}

	// Workspace must now be empty.
	remaining, err := store.ListUnresolved(ctx, &wsID)
	if err != nil {
		t.Fatalf("ListUnresolved after prune: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("expected 0 events after prune, got %d", len(remaining))
	}
}

// isErrEventNotFound reports whether err wraps ErrEventNotFound.
func isErrEventNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not found")
}
