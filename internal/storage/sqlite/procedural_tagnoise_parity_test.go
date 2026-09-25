package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/procedural"
	"github.com/Wayne997035/wayneblacktea/internal/sanitize"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
)

// [GTD d76ebc56] repo_name reaches three write paths — decision, session and
// procedural. The first two were screened; procedural was not, which is what
// made the "validator-gated at every write path" exemption recorded against
// ProceduralMemory.RepoName untrue. These tests are the procedural row of the
// table TestTagNoiseParity already keeps for the other two.
//
// tagNoise, and the rest of the shared fixtures, come from
// tagnoise_parity_test.go in this package.

func openParityProceduralStore(t *testing.T) *sqlite.ProceduralStore {
	t.Helper()
	d, err := sqlite.OpenTemplated(t, context.Background(), ":memory:", "") // [F0925-09] semantics-preserving template helper
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return sqlite.NewProceduralStore(d)
}

// cleanAddParams mirrors cleanHandoffParams / cleanLogParams: every field
// tag-noise-free, so a single field can be made noisy without an earlier
// field tripping the check first and testing the wrong thing.
func cleanAddParams() procedural.AddParams {
	return procedural.AddParams{
		RepoName:     "wayneblacktea",
		Title:        "clean title",
		WhenToUse:    "clean when-to-use",
		ApproachMD:   "clean approach",
		ToolsUsed:    []string{"Bash"},
		FilesTouched: []string{"internal/procedural/store.go"},
	}
}

// pgxAddRejection calls the pgx Add with a nil pool and returns the error.
//
// A nil pool is what makes this assertable at all: the screen runs before the
// pool is touched, so a rejected write returns cleanly, while a write that got
// past the screen reaches Query and dereferences nil. That second case is the
// regression this test exists to catch, and left alone it surfaces as a
// SIGSEGV that aborts the whole test binary — every other test in the package
// then reports nothing, which is a worse outcome than the regression itself.
// Recovering turns it into an ordinary named failure.
//
// Discovered by mutating the screen away and watching the "failure" arrive as
// a panic trace instead of a message.
func pgxAddRejection(t *testing.T, p procedural.AddParams) (err error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("pgx Add reached the connection pool with a noisy "+
				"repo_name — the screen did not run (panic: %v)", r)
		}
	}()
	_, err = procedural.New(nil, nil).Add(context.Background(), p)
	return err
}

// TestProceduralTagNoiseParity_RepoName is the negative frame on both
// backends, plus AC-9's message-parity property: the wrap text must be
// byte-identical, not merely "an error on both sides".
//
// The pgx store takes a concrete *pgxpool.Pool rather than an interface, so it
// cannot be driven with a fake the way decision.Store can. The screen runs
// before the pool is touched, so the rejection path IS reachable with a nil
// pool; the clean path is not, which is why the positive control below runs on
// SQLite only.
func TestProceduralTagNoiseParity_RepoName(t *testing.T) {
	t.Parallel() // [F0925-10]
	p := cleanAddParams()
	p.RepoName = tagNoise

	pgErr := pgxAddRejection(t, p)
	if !errors.Is(pgErr, sanitize.ErrTagNoise) {
		t.Fatalf("pgx Add did not reject a noisy repo_name: %v", pgErr)
	}

	s := openParityProceduralStore(t)
	_, sqliteErr := s.Add(context.Background(), p)
	if !errors.Is(sqliteErr, sanitize.ErrTagNoise) {
		t.Fatalf("sqlite Add did not reject a noisy repo_name: %v", sqliteErr)
	}

	if sqliteErr.Error() != pgErr.Error() {
		t.Errorf("sqlite message = %q, pgx message = %q — must be byte-identical",
			sqliteErr.Error(), pgErr.Error())
	}
}

// TestProceduralTagNoiseParity_CleanReachesInsert is the positive control. A
// screen that rejects everything passes the test above; this is what rejects
// that implementation.
func TestProceduralTagNoiseParity_CleanReachesInsert(t *testing.T) {
	t.Parallel() // [F0925-10]
	s := openParityProceduralStore(t)
	if _, err := s.Add(context.Background(), cleanAddParams()); err != nil {
		t.Fatalf("clean AddParams must reach the INSERT, got: %v", err)
	}
}

// TestProceduralTagNoise_ProseFieldsNotValidated pins the scope as deliberate,
// the way TestTagNoiseParity_ActorSessionIDNotValidated does for decision.
//
// title / when_to_use / approach_md are prose an agent is SUPPOSED to author
// freely — they are handled on the way out by wrapUntrustedProceduralMemory,
// not by refusing the write. Without this test, "screen repo_name" would drift
// into "screen everything" and start refusing legitimate procedures, and
// nothing would object.
func TestProceduralTagNoise_ProseFieldsNotValidated(t *testing.T) {
	t.Parallel() // [F0925-10]
	fields := []struct {
		name string
		set  func(p *procedural.AddParams, v string)
	}{
		{"Title", func(p *procedural.AddParams, v string) { p.Title = v }},
		{"WhenToUse", func(p *procedural.AddParams, v string) { p.WhenToUse = v }},
		{"ApproachMD", func(p *procedural.AddParams, v string) { p.ApproachMD = v }},
	}

	for _, f := range fields {
		t.Run(f.name, func(t *testing.T) {
			p := cleanAddParams()
			f.set(&p, tagNoise)

			s := openParityProceduralStore(t)
			if _, err := s.Add(context.Background(), p); err != nil {
				t.Fatalf("noisy %s must still be accepted (read-side neutralisation "+
					"handles it), got: %v", f.name, err)
			}
		})
	}
}
