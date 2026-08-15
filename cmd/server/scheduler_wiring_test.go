package main

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/discord"
	"github.com/Wayne997035/wayneblacktea/internal/scheduler"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
)

// wiringTestStores is a minimal storage.ServerStores test double for
// wirePendingProposalsPruneSQLite. It embeds the (nil) interface so any
// method other than the one overridden below panics if invoked --
// wirePendingProposalsPruneSQLite (main.go:954-963) only ever calls
// SqliteDB(), so that is the only method this stub needs to implement.
// Building a fuller fake ServerStores was explicitly out of scope per the
// dispatch prompt; this embed-nil-interface pattern keeps the double to the
// one method actually exercised.
type wiringTestStores struct {
	storage.ServerStores
	sqliteDB *wbtsqlite.DB
}

func (s wiringTestStores) SqliteDB() *wbtsqlite.DB { return s.sqliteDB }

// newWiringTestScheduler builds a minimal *scheduler.Scheduler suitable for
// exercising wirePendingProposalsPruneSQLite in isolation, without a live DB
// or the rest of wireScheduler's setup. All optional deps are nil; none of
// them are touched by scheduler.New's registration path (they are only read
// inside job-run callback methods, which nothing here invokes) -- matching
// the nil-heavy construction pattern internal/scheduler's own tests already
// use (e.g. internal/scheduler/pending_proposals_prune_sqlite_test.go).
func newWiringTestScheduler(t *testing.T) *scheduler.Scheduler {
	t.Helper()
	sc, err := scheduler.New(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("scheduler.New: %v", err)
	}
	return sc
}

// pendingProposalsPruneSQLiteFieldIsNil reads the unexported
// pendingProposalsPruneSQLite field on *scheduler.Scheduler via reflection
// and reports whether it is a TRUE nil interface.
//
// reflect.Value.IsNil() operates directly on the interface's (type, value)
// pair and does NOT require calling Interface() (which panics on a field
// obtained from a different package via FieldByName) -- so this is a plain
// `field == nil` check, just reached through reflection because the field
// itself is package-private to internal/scheduler. It correctly distinguishes
// a true nil interface (both the type and value words are nil) from a
// non-nil interface boxing a broken/nil-backed concrete pointer (only the
// value word would be a "nil-ish" pointer; the type word is still set, so
// IsNil() on the interface reports false) -- exactly the typed-nil trap
// wirePendingProposalsPruneSQLite's doc comment (main.go:948-953) documents.
func pendingProposalsPruneSQLiteFieldIsNil(t *testing.T, sc *scheduler.Scheduler) bool {
	t.Helper()
	v := reflect.ValueOf(sc).Elem().FieldByName("pendingProposalsPruneSQLite")
	if !v.IsValid() {
		t.Fatal("scheduler.Scheduler has no field named pendingProposalsPruneSQLite -- test is stale, update it")
	}
	return v.IsNil()
}

// TestWirePendingProposalsPruneSQLite_SQLiteBackend_WiresUsableStore covers
// the happy path: when stores.SqliteDB() returns a real, non-nil *DB (SQLite
// backend), the scheduler must receive a non-nil, usable prune store.
func TestWirePendingProposalsPruneSQLite_SQLiteBackend_WiresUsableStore(t *testing.T) {
	db, err := wbtsqlite.Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("wbtsqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	sc := newWiringTestScheduler(t)
	stores := wiringTestStores{sqliteDB: db}

	if err := wirePendingProposalsPruneSQLite(sc, stores); err != nil {
		t.Fatalf("wirePendingProposalsPruneSQLite: %v", err)
	}

	if pendingProposalsPruneSQLiteFieldIsNil(t, sc) {
		t.Fatal("expected a non-nil prune store to be wired when SqliteDB() returns a real *DB")
	}

	// "Usable" proof: wirePendingProposalsPruneSQLite's production body
	// (main.go:957) constructs the wired store as
	// wbtsqlite.NewProposalStore(sqliteDB) from this exact db. Exercise that
	// same construction independently against the same in-memory DB to prove
	// it is a working store backed by real (migrated) schema, not just a
	// non-nil pointer.
	usable := wbtsqlite.NewProposalStore(db)
	if _, _, err := usable.MarkAndDeleteStaleProposals(
		context.Background(), 24*time.Hour, 24*time.Hour, 24*time.Hour, "wiring-test",
	); err != nil {
		t.Fatalf("MarkAndDeleteStaleProposals against wired store's backing DB: %v", err)
	}
}

// TestWirePendingProposalsPruneSQLite_PostgresBackend_StaysTrueNilInterface
// covers the boundary that matters: when stores.SqliteDB() returns nil
// (Postgres deployment), the interface reaching
// scheduler.WithPendingProposalsPruneSQLite -- and therefore the field it
// stores -- must stay a TRUE nil interface, not a non-nil interface boxing a
// broken concrete store. See pendingProposalsPruneSQLiteFieldIsNil's doc
// comment for exactly which failure mode this assertion catches.
func TestWirePendingProposalsPruneSQLite_PostgresBackend_StaysTrueNilInterface(t *testing.T) {
	sc := newWiringTestScheduler(t)
	stores := wiringTestStores{sqliteDB: nil} // Postgres deployment: no SQLite backend wired.

	if err := wirePendingProposalsPruneSQLite(sc, stores); err != nil {
		t.Fatalf("wirePendingProposalsPruneSQLite: %v", err)
	}

	if !pendingProposalsPruneSQLiteFieldIsNil(t, sc) {
		t.Fatal("expected pendingProposalsPruneSQLite to stay a TRUE nil interface on Postgres " +
			"(SqliteDB() == nil); a non-nil interface here means the typed-nil guard regressed and " +
			"a Postgres deployment would wire a broken SQLite prune store")
	}
}

// TestWireDiscordSender_NilClient_StaysTrueNilInterface covers the crash
// scenario (DISCORD_WEBHOOK_URL unset): discord.NewClient() returns a nil
// *discord.Client, and wireDiscordSender must hand scheduler.New a TRUE nil
// DiscordSender -- not a non-nil interface boxing a nil pointer. Unlike
// pendingProposalsPruneSQLiteFieldIsNil above, no reflection is needed here:
// wireDiscordSender's return value is directly inspectable in this package,
// so `dc == nil` on the interface value itself is the whole test.
func TestWireDiscordSender_NilClient_StaysTrueNilInterface(t *testing.T) {
	dc := wireDiscordSender(nil)
	if dc != nil {
		t.Fatal("expected wireDiscordSender(nil) to return a TRUE nil DiscordSender; got a non-nil " +
			"interface -- this means a nil *discord.Client got boxed and " +
			"sendDailyReviewReminder's `s.discord == nil` guard (scheduler.go:842) will not fire, " +
			"reproducing the SIGSEGV this fix exists to prevent")
	}
}

// TestWireDiscordSender_NonNilClient_PassesThrough covers the happy path
// (DISCORD_WEBHOOK_URL set): a real, non-nil *discord.Client must still reach
// the scheduler as a usable, non-nil DiscordSender.
func TestWireDiscordSender_NonNilClient_PassesThrough(t *testing.T) {
	client := &discord.Client{}
	dc := wireDiscordSender(client)
	if dc == nil {
		t.Fatal("expected wireDiscordSender to pass a non-nil *discord.Client through as a non-nil " +
			"DiscordSender")
	}
}
