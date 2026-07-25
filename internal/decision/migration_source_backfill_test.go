package decision_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	migrationfs "github.com/Wayne997035/wayneblacktea/migrations"
	"github.com/google/uuid"
)

// preCutoffCreatedAt is a fixed timestamp before migration 000073's
// created_at backfill cutoff (2026-07-25T07:00:00Z, see the up.sql comment)
// — legacy fixture rows use this so the backfill predicates match
// regardless of what wall-clock time `go test` actually runs at (security
// review round 2, M-1).
const preCutoffCreatedAt = "2026-07-20T00:00:00Z"

// postCutoffCreatedAt is a fixed timestamp AFTER the same cutoff, used by
// the boundary test case to prove created_at is actually enforced.
const postCutoffCreatedAt = "2026-07-25T08:00:00Z"

// TestMigration000073_Backfill_Postgres mirrors the SQLite twin coverage
// (internal/storage/sqlite's real-SQLite test) — backend-security-design.md
// §6.5 dual-backend parity requires the same predicate logic to be
// independently verified on both engines. Runs inside a transaction that is
// always rolled back (t.Cleanup), same technique as
// TestMigration000073_UpDownUp_PreservesNonSourceFields below: M-1 changed
// the ALTER from `ADD COLUMN IF NOT EXISTS` to plain `ADD COLUMN`, so
// re-executing up.sql verbatim against the already-migrated shared pool now
// fails loudly (duplicate column) instead of silently no-op'ing — that's the
// point of M-1, but it means this test can no longer re-run up.sql directly
// against the pool. Instead it drops the column via down.sql, seeds legacy
// rows, then re-applies up.sql, all inside one transaction — exercising the
// exact same predicate SQL production ran once, without ever mutating the
// shared table outside this transaction. NULL context/rationale is not
// separately covered — both columns are NOT NULL as of the originating
// migration 000003_decisions, so a NULL near-miss is structurally
// unreachable.
func TestMigration000073_Backfill_Postgres(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()

	upBody, downBody := readMigration000073Bodies(t)

	cases := []struct {
		name      string
		context   string
		rationale string
		createdAt string
		wantAuto  bool
	}{
		{"predicate1 exact match", "any context", "implicitly decided via tool invocation", preCutoffCreatedAt, true},
		{"predicate1 case-variant stays manual", "any context", "Implicitly decided via tool invocation", preCutoffCreatedAt, false},
		{"predicate1 trailing space stays manual", "any context", "implicitly decided via tool invocation ", preCutoffCreatedAt, false},
		{"predicate2 exact match", "auto-extracted from session transcript", "implicitly decided during session", preCutoffCreatedAt, true},
		{"predicate2 wrong rationale stays manual", "auto-extracted from session transcript", "some other rationale", preCutoffCreatedAt, false},
		{"predicate3 exact match", "auto-proposed by trigger_tool=confirm_proposal session=abc123", "whatever", preCutoffCreatedAt, true},
		{
			"predicate3 missing session marker stays manual", "auto-proposed by trigger_tool=confirm_proposal",
			"whatever", preCutoffCreatedAt, false,
		},
		{"predicate3 case-variant stays manual", "Auto-proposed by trigger_tool=x session=y", "whatever", preCutoffCreatedAt, false},
		{"unrelated legacy row stays manual", "some manual context", "some manual rationale", preCutoffCreatedAt, false},
		// M-1 boundary: an adversarially-crafted row whose context matches
		// predicate 3 byte-for-byte, but whose created_at is AFTER the
		// cutoff (i.e. it's a row written after P3.0a shipped, simulating a
		// caller who invoked log_decision with a predicate-shaped context)
		// MUST stay manual — the cutoff, not just the text match, gates
		// reclassification.
		{
			"predicate3 match but created_at after cutoff stays manual",
			"auto-proposed by trigger_tool=confirm_proposal session=abc123",
			"whatever", postCutoffCreatedAt, false,
		},
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() {
		if rbErr := tx.Rollback(ctx); rbErr != nil {
			t.Logf("rollback tx: %v", rbErr)
		}
	})

	if _, err := tx.Exec(ctx, string(downBody)); err != nil {
		t.Fatalf("drop source column (down.sql): %v", err)
	}

	ids := make(map[string]uuid.UUID, len(cases))
	for _, tc := range cases {
		var id uuid.UUID
		err := tx.QueryRow(
			ctx,
			`INSERT INTO decisions (title, context, decision, rationale, created_at) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			"backfill: "+tc.name, tc.context, "some decision text", tc.rationale, tc.createdAt,
		).Scan(&id)
		if err != nil {
			t.Fatalf("insert legacy decision %q: %v", tc.name, err)
		}
		ids[tc.name] = id
	}

	if _, err := tx.Exec(ctx, string(upBody)); err != nil {
		t.Fatalf("re-apply migration 000073 body: %v", err)
	}

	for _, tc := range cases {
		var source string
		if err := tx.QueryRow(ctx, "SELECT source FROM decisions WHERE id = $1", ids[tc.name]).Scan(&source); err != nil {
			t.Fatalf("%s: read source: %v", tc.name, err)
		}
		want := "manual"
		if tc.wantAuto {
			want = "auto"
		}
		if source != want {
			t.Errorf("%s: source = %q, want %q", tc.name, source, want)
		}
	}
}

// TestStore_Log_InvalidSourceWritesZeroRows verifies the application-layer
// guard: Store.Log rejects an invalid Source before ever reaching the DB —
// the CHECK constraint is defence-in-depth, not the primary enforcement
// point. asserts zero rows written for the invalid title used.
func TestStore_Log_InvalidSourceWritesZeroRows(t *testing.T) {
	pool := openTestPgPool(t)
	store := decision.NewStore(pool, nil)
	ctx := context.Background()

	const title = "P3.0a invalid-source probe (should never be written)"
	_, err := store.Log(ctx, decision.LogParams{
		Title:     title,
		Context:   "ctx",
		Decision:  "dec",
		Rationale: "rat",
		Source:    decision.Source("system"), // not manual/auto
	})
	if err == nil {
		t.Fatal("expected error for invalid source, got nil")
	}
	if !errors.Is(err, decision.ErrInvalidSource) {
		t.Errorf("expected ErrInvalidSource, got %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM decisions WHERE title = $1", title).Scan(&count); err != nil {
		t.Fatalf("count check: %v", err)
	}
	if count != 0 {
		t.Errorf("expected zero rows written for invalid source, got %d", count)
	}
}

// TestStore_Log_ValidSourcePersists verifies a valid Source round-trips
// through Store.Log into the returned row and the DB.
func TestStore_Log_ValidSourcePersists(t *testing.T) {
	pool := openTestPgPool(t)
	store := decision.NewStore(pool, nil)
	ctx := context.Background()

	d, err := store.Log(ctx, decision.LogParams{
		Title:     "P3.0a valid-source probe",
		Context:   "ctx",
		Decision:  "dec",
		Rationale: "rat",
		Source:    decision.SourceAuto,
	})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	t.Cleanup(func() {
		if _, cleanErr := pool.Exec(ctx, "DELETE FROM decisions WHERE id = $1", d.ID); cleanErr != nil {
			t.Logf("cleanup decision: %v", cleanErr)
		}
	})
	if d.Source != string(decision.SourceAuto) {
		t.Errorf("Source = %q, want %q", d.Source, decision.SourceAuto)
	}

	var dbSource string
	if err := pool.QueryRow(ctx, "SELECT source FROM decisions WHERE id = $1", d.ID).Scan(&dbSource); err != nil {
		t.Fatalf("read back source: %v", err)
	}
	if dbSource != string(decision.SourceAuto) {
		t.Errorf("DB source = %q, want %q", dbSource, decision.SourceAuto)
	}
}

// TestMigration000073_UpDownUp_PreservesNonSourceFields exercises down then
// up again and verifies non-source fields (title/context/decision/rationale/
// alternatives) round-trip unchanged. down is provenance-lossy by design:
// it DROPs the source column entirely, and re-up only recomputes the 3
// legacy text predicates against context/rationale — it does NOT restore
// whatever the row's source was before down ran. This test seeds a row with
// Source=SourceAuto but context/rationale that DON'T match any of the 3
// predicates (an "auto" classification with no legacy-text fingerprint,
// exactly the case down/up cannot recover) specifically so the source value
// is NOT expected to equal its pre-down value after the round-trip — per
// P3.0a Step 7, this test does not assert post-up source equality; it
// documents and asserts the expected LOSS (source reverts to the column
// DEFAULT 'manual') instead.
//
// Runs inside a transaction that is always rolled back (t.Cleanup), so the
// column DROP/re-ADD never touches the shared testPgPool table used by
// sibling tests in this package.
func TestMigration000073_UpDownUp_PreservesNonSourceFields(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()

	upBody, downBody := readMigration000073Bodies(t)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() {
		if rbErr := tx.Rollback(ctx); rbErr != nil {
			t.Logf("rollback tx: %v", rbErr)
		}
	})

	store := decision.NewStore(pool, nil).WithTx(tx)
	d, err := store.Log(ctx, decision.LogParams{
		Title:        "P3.0a up-down-up probe",
		Context:      "no legacy predicate matches this context",
		Decision:     "some decision",
		Rationale:    "no legacy predicate matches this rationale either",
		Alternatives: "kept as-is",
		Source:       decision.SourceAuto,
	})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if d.Source != string(decision.SourceAuto) {
		t.Fatalf("precondition failed: Source = %q, want %q", d.Source, decision.SourceAuto)
	}

	if _, err := tx.Exec(ctx, string(downBody)); err != nil {
		t.Fatalf("apply down.sql: %v", err)
	}
	if _, err := tx.Exec(ctx, string(upBody)); err != nil {
		t.Fatalf("re-apply up.sql: %v", err)
	}

	var title, context, dec, rationale, alternatives, source string
	err = tx.QueryRow(
		ctx,
		"SELECT title, context, decision, rationale, alternatives, source FROM decisions WHERE id = $1",
		d.ID,
	).Scan(&title, &context, &dec, &rationale, &alternatives, &source)
	if err != nil {
		t.Fatalf("read back row post round-trip: %v", err)
	}

	assertUpDownUpPreservedNonSourceFields(t, d, title, context, dec, rationale, alternatives, source)
}

// readMigration000073Bodies loads both SQL bodies for migration 000073 in one
// call, keeping the caller's error-handling branch count down (gocyclo).
func readMigration000073Bodies(t *testing.T) (up, down []byte) {
	t.Helper()
	up, err := migrationfs.FS.ReadFile("000073_decision_source.up.sql")
	if err != nil {
		t.Fatalf("read migration 000073 up.sql: %v", err)
	}
	down, err = migrationfs.FS.ReadFile("000073_decision_source.down.sql")
	if err != nil {
		t.Fatalf("read migration 000073 down.sql: %v", err)
	}
	return up, down
}

// assertUpDownUpPreservedNonSourceFields checks the round-tripped row against
// the original Log() result: non-source fields must be byte-identical, and
// source must have reverted to the documented provenance-loss DEFAULT.
func assertUpDownUpPreservedNonSourceFields(
	t *testing.T, original *db.Decision, title, context, dec, rationale, alternatives, source string,
) {
	t.Helper()
	if title != original.Title || context != original.Context || dec != original.Decision ||
		rationale != original.Rationale || alternatives != "kept as-is" {
		t.Errorf("non-source fields changed across down/up: title=%q context=%q decision=%q rationale=%q alternatives=%q",
			title, context, dec, rationale, alternatives)
	}
	// Documented provenance loss: this row was 'auto' before down, but its
	// context/rationale match none of the 3 legacy predicates, so re-up
	// cannot recompute 'auto' — it lands on the column DEFAULT.
	if source != string(decision.SourceManual) {
		t.Errorf("source after down/up = %q, want %q (documented provenance loss for non-predicate-matching rows)",
			source, decision.SourceManual)
	}
}
