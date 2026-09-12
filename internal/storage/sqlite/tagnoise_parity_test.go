package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/sanitize"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// tagNoise is a field-name-agnostic tool-call-serialization fragment: the
// invoke-tag branch of sanitize's regex set (internal/sanitize/tagnoise.go)
// matches regardless of which field carries it, unlike the field-named
// closing-tag branch (there is no "</title>" entry — title has no
// field-specific tag). Using one fixture for every field keeps this table
// uniform instead of hand-picking a matching closing tag per field.
const tagNoise = "clean text </invoke> more text"

// fakeDBTX / fakeErrRow are copied from internal/decision/store_test.go:15-43
// — enough of a pgx.DBTX stand-in for decision.Store.Log to reach
// sanitize.ValidateNoTagNoise (and, on clean input, the DB call itself)
// without a real Postgres connection.
type fakeDBTX struct{}

func (fakeDBTX) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errFakeDBTXUnused
}

func (fakeDBTX) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errFakeDBTXUnused
}

func (fakeDBTX) QueryRow(context.Context, string, ...any) pgx.Row {
	return fakeErrRow{}
}

var errFakeDBTXUnused = errors.New("fakeDBTX: Exec/Query not expected to be called by decision.Store.Log")

// fakeErrRow is a pgx.Row whose Scan always fails with a sentinel error
// distinguishable from sanitize.ErrTagNoise.
type fakeErrRow struct{}

func (fakeErrRow) Scan(...any) error { return errFakeRowScan }

var errFakeRowScan = errors.New("fakeErrRow: no real row")

// openParitySessionStore / openParityDecisionStore each open a fresh
// in-memory SQLite DB per call — tests run in parallel-unsafe sequence
// (table-driven, not t.Parallel) but must not share state across subtests.
func openParitySessionStore(t *testing.T) *sqlite.SessionStore {
	t.Helper()
	d, err := sqlite.Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return sqlite.NewSessionStore(d)
}

func openParityDecisionStore(t *testing.T) (*sqlite.DB, *sqlite.DecisionStore) {
	t.Helper()
	d, err := sqlite.Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d, sqlite.NewDecisionStore(d)
}

// cleanHandoffParams / cleanLogParams return a request with every field
// tag-noise-free, so a single field can be swapped to the noisy fixture
// without any OTHER field accidentally tripping ValidateNoTagNoise first —
// AC-5/6/7 are explicitly per-field, and both backends check fields in a
// fixed order (earlier field first), so a dirty neighbour would silently
// test the wrong thing.
func cleanHandoffParams() session.HandoffParams {
	return session.HandoffParams{
		Intent:         "continue tomorrow",
		ContextSummary: "clean summary",
		RepoName:       "wayneblacktea",
	}
}

func cleanLogParams() decision.LogParams {
	return decision.LogParams{
		Title:        "clean title",
		RepoName:     "wayneblacktea",
		Rationale:    "clean rationale",
		Alternatives: "clean alternatives",
		Context:      "clean context",
		Decision:     "clean decision",
		Source:       decision.SourceManual,
	}
}

// sessionFields / decisionFields drive the per-field subtests below — order
// matches the checks added by F0911-04 (session/store.go:68-76,
// decision/store.go:43-60).
var sessionFields = []struct {
	name string
	set  func(p *session.HandoffParams, v string)
}{
	{"Intent", func(p *session.HandoffParams, v string) { p.Intent = v }},
	{"ContextSummary", func(p *session.HandoffParams, v string) { p.ContextSummary = v }},
	{"RepoName", func(p *session.HandoffParams, v string) { p.RepoName = v }},
}

var decisionFields = []struct {
	name string
	set  func(p *decision.LogParams, v string)
}{
	{"Title", func(p *decision.LogParams, v string) { p.Title = v }},
	{"RepoName", func(p *decision.LogParams, v string) { p.RepoName = v }},
	{"Rationale", func(p *decision.LogParams, v string) { p.Rationale = v }},
	{"Alternatives", func(p *decision.LogParams, v string) { p.Alternatives = v }},
	{"Context", func(p *decision.LogParams, v string) { p.Context = v }},
	{"Decision", func(p *decision.LogParams, v string) { p.Decision = v }},
}

// TestTagNoiseParity is AC-5/6/7: sqlite.SessionStore.SetHandoff,
// sqlite.DecisionStore.Log and sqlite.DecisionStore.LogTx must each reject
// the same fields, in the same order, that their pgx counterparts already
// do (internal/session/store.go:68-76, internal/decision/store.go:43-60).
func TestTagNoiseParity(t *testing.T) {
	t.Run("session", func(t *testing.T) {
		for _, f := range sessionFields {
			t.Run(f.name, func(t *testing.T) {
				s := openParitySessionStore(t)
				p := cleanHandoffParams()
				f.set(&p, tagNoise)
				_, err := s.SetHandoff(context.Background(), p)
				if !errors.Is(err, sanitize.ErrTagNoise) {
					t.Fatalf("SetHandoff with noisy %s: errors.Is(err, sanitize.ErrTagNoise) = false (err: %v)", f.name, err)
				}
			})
		}
	})

	// Positive control (AC-5's negative case): all three fields clean must
	// reach the INSERT, not stop early on some unrelated check.
	t.Run("session/AllClean", func(t *testing.T) {
		s := openParitySessionStore(t)
		if _, err := s.SetHandoff(context.Background(), cleanHandoffParams()); err != nil {
			t.Fatalf("clean HandoffParams must reach the INSERT, got: %v", err)
		}
	})

	t.Run("decision-log", func(t *testing.T) {
		for _, f := range decisionFields {
			t.Run(f.name, func(t *testing.T) {
				_, s := openParityDecisionStore(t)
				p := cleanLogParams()
				f.set(&p, tagNoise)
				_, err := s.Log(context.Background(), p)
				if !errors.Is(err, sanitize.ErrTagNoise) {
					t.Fatalf("Log with noisy %s: errors.Is(err, sanitize.ErrTagNoise) = false (err: %v)", f.name, err)
				}
			})
		}
	})

	t.Run("decision-log/AllClean", func(t *testing.T) {
		_, s := openParityDecisionStore(t)
		if _, err := s.Log(context.Background(), cleanLogParams()); err != nil {
			t.Fatalf("clean LogParams must reach the INSERT, got: %v", err)
		}
	})

	t.Run("decision-logtx", func(t *testing.T) {
		for _, f := range decisionFields {
			t.Run(f.name, func(t *testing.T) {
				d, s := openParityDecisionStore(t)
				tx, err := d.BeginTx(context.Background())
				if err != nil {
					t.Fatalf("BeginTx: %v", err)
				}
				defer func() { _ = tx.Rollback() }()
				p := cleanLogParams()
				f.set(&p, tagNoise)
				if _, err := s.LogTx(context.Background(), tx, p); !errors.Is(err, sanitize.ErrTagNoise) {
					t.Fatalf("LogTx with noisy %s: errors.Is(err, sanitize.ErrTagNoise) = false (err: %v)", f.name, err)
				}
			})
		}
	})

	// Positive control (AC-7's negative case): clean params inside a real
	// *sql.Tx commit, and the row is readable after Commit.
	t.Run("decision-logtx/AllClean", func(t *testing.T) {
		d, s := openParityDecisionStore(t)
		tx, err := d.BeginTx(context.Background())
		if err != nil {
			t.Fatalf("BeginTx: %v", err)
		}
		if _, err := s.LogTx(context.Background(), tx, cleanLogParams()); err != nil {
			t.Fatalf("clean LogParams inside a real tx must reach the INSERT, got: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("tx.Commit: %v", err)
		}
		rows, err := s.All(context.Background(), 10)
		if err != nil {
			t.Fatalf("All: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("expected 1 committed row, got %d", len(rows))
		}
	})
}

// TestTagNoiseParity_ActorSessionIDNotValidated pins AC-8: ActorSessionID is
// a server-generated session identifier (internal/decision/store.go:61-66's
// doc comment), not caller-supplied free text, so NEITHER backend runs it
// through ValidateNoTagNoise. "Aligned field-for-field" must not silently
// become "validate every field" — F0911-04 guards exactly the 6 named
// fields, on purpose.
func TestTagNoiseParity_ActorSessionIDNotValidated(t *testing.T) {
	p := cleanLogParams()
	p.ActorSessionID = "sess</invoke>"

	t.Run("sqlite/Log", func(t *testing.T) {
		_, s := openParityDecisionStore(t)
		if _, err := s.Log(context.Background(), p); errors.Is(err, sanitize.ErrTagNoise) {
			t.Fatalf("Log must not reject a noisy ActorSessionID, got: %v", err)
		}
	})

	t.Run("sqlite/LogTx", func(t *testing.T) {
		d, s := openParityDecisionStore(t)
		tx, err := d.BeginTx(context.Background())
		if err != nil {
			t.Fatalf("BeginTx: %v", err)
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := s.LogTx(context.Background(), tx, p); errors.Is(err, sanitize.ErrTagNoise) {
			t.Fatalf("LogTx must not reject a noisy ActorSessionID, got: %v", err)
		}
	})

	t.Run("pgx/Log", func(t *testing.T) {
		// fakeDBTX's QueryRow always fails at Scan (see its doc comment), so
		// "succeeds" is not assertable on this backend — the only assertable
		// property is WHICH error comes back: errFakeRowScan (the call
		// reached the DB) rather than sanitize.ErrTagNoise (rejected first).
		store := decision.NewStore(fakeDBTX{}, nil)
		_, err := store.Log(context.Background(), p)
		if errors.Is(err, sanitize.ErrTagNoise) {
			t.Fatalf("pgx Log must not reject a noisy ActorSessionID, got: %v", err)
		}
		if !errors.Is(err, errFakeRowScan) {
			t.Errorf("expected the call to reach the DB layer (errFakeRowScan), got: %v", err)
		}
	})
}

// TestTagNoiseParity_MessagesMatchAcrossBackends pins AC-9: the wrap text
// itself — not just whether an error occurs — must be byte-identical across
// backends. pgx has no LogTx, so decision.Store.Log (the pgx path) is the
// shared baseline that both sqlite.DecisionStore.Log and .LogTx are
// compared against.
func TestTagNoiseParity_MessagesMatchAcrossBackends(t *testing.T) {
	for _, f := range decisionFields {
		t.Run(f.name, func(t *testing.T) {
			p := cleanLogParams()
			f.set(&p, tagNoise)

			pgStore := decision.NewStore(fakeDBTX{}, nil)
			_, pgErr := pgStore.Log(context.Background(), p)
			if !errors.Is(pgErr, sanitize.ErrTagNoise) {
				t.Fatalf("pgx baseline did not reject noisy %s: %v", f.name, pgErr)
			}

			_, sqliteLogStore := openParityDecisionStore(t)
			_, logErr := sqliteLogStore.Log(context.Background(), p)
			if !errors.Is(logErr, sanitize.ErrTagNoise) {
				t.Fatalf("sqlite Log did not reject noisy %s: %v", f.name, logErr)
			}
			if logErr.Error() != pgErr.Error() {
				t.Errorf("sqlite Log message = %q, pgx Log message = %q — must be byte-identical", logErr.Error(), pgErr.Error())
			}

			d, sqliteLogTxStore := openParityDecisionStore(t)
			tx, err := d.BeginTx(context.Background())
			if err != nil {
				t.Fatalf("BeginTx: %v", err)
			}
			defer func() { _ = tx.Rollback() }()
			_, logTxErr := sqliteLogTxStore.LogTx(context.Background(), tx, p)
			if !errors.Is(logTxErr, sanitize.ErrTagNoise) {
				t.Fatalf("sqlite LogTx did not reject noisy %s: %v", f.name, logTxErr)
			}
			if logTxErr.Error() != pgErr.Error() {
				t.Errorf("sqlite LogTx message = %q, pgx Log message = %q — must be byte-identical", logTxErr.Error(), pgErr.Error())
			}
		})
	}
}
