package sqlite_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/procedural"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/Wayne997035/wayneblacktea/internal/validator"
	"github.com/Wayne997035/wayneblacktea/internal/vision"
	"github.com/Wayne997035/wayneblacktea/internal/worksession"
)

// badRepoName is the canonical rejected value for the store-layer repo_name
// backstops below: a real value an LLM once sent through log_decision, and
// one the workspace repo name rule rejects (segment starts with '.').
const badRepoName = "_project/.claude"

func openRepoNameDB(t *testing.T) *sqlite.DB {
	t.Helper()
	d, err := sqlite.OpenTemplated(t, context.Background(), ":memory:", "") // [F0925-09] semantics-preserving template helper
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// TestRepoNameBackstop_Decision pins the SQLite decisions backstop on both
// write paths (Log and LogTx): a bad repo_name is rejected, empty is allowed.
func TestRepoNameBackstop_Decision(t *testing.T) {
	t.Parallel() // [F0925-29]
	ctx := context.Background()

	d := openRepoNameDB(t)
	s := sqlite.NewDecisionStore(d)
	p := cleanLogParams()
	p.RepoName = badRepoName
	if _, err := s.Log(ctx, p); !errors.Is(err, validator.ErrInvalidRepoName) {
		t.Errorf("Log(%q): want ErrInvalidRepoName, got %v", badRepoName, err)
	}
	p.RepoName = ""
	if _, err := s.Log(ctx, p); err != nil {
		t.Errorf("Log(empty repo_name): %v", err)
	}

	tx, err := d.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	p.RepoName = badRepoName
	if _, err := s.LogTx(ctx, tx, p); !errors.Is(err, validator.ErrInvalidRepoName) {
		t.Errorf("LogTx(%q): want ErrInvalidRepoName, got %v", badRepoName, err)
	}
}

// TestRepoNameBackstop_Handoff pins the SQLite session_handoffs backstop.
func TestRepoNameBackstop_Handoff(t *testing.T) {
	t.Parallel() // [F0925-29]
	s := sqlite.NewSessionStore(openRepoNameDB(t))
	p := cleanHandoffParams()
	p.RepoName = badRepoName
	if _, err := s.SetHandoff(context.Background(), p); !errors.Is(err, validator.ErrInvalidRepoName) {
		t.Errorf("SetHandoff(%q): want ErrInvalidRepoName, got %v", badRepoName, err)
	}
	p.RepoName = ""
	if _, err := s.SetHandoff(context.Background(), p); err != nil {
		t.Errorf("SetHandoff(empty repo_name): %v", err)
	}
}

// TestRepoNameBackstop_WorkSession pins the SQLite work_sessions backstop.
// repo_name stays required there: empty keeps the existing error.
func TestRepoNameBackstop_WorkSession(t *testing.T) {
	t.Parallel() // [F0925-29]
	s, _ := openWorkSessionMem(t, "")
	p := worksession.CreateParams{RepoName: badRepoName, Title: "t", Goal: "g", Source: "manual"}
	if _, err := s.Create(context.Background(), p); !errors.Is(err, validator.ErrInvalidRepoName) {
		t.Errorf("Create(%q): want ErrInvalidRepoName, got %v", badRepoName, err)
	}
	p.RepoName = ""
	if _, err := s.Create(context.Background(), p); err == nil || !strings.Contains(err.Error(), "repo_name is required") {
		t.Errorf("Create(empty repo_name): want \"repo_name is required\", got %v", err)
	}
}

// TestRepoNameBackstop_Vision pins the SQLite vision_items backstop.
func TestRepoNameBackstop_Vision(t *testing.T) {
	t.Parallel() // [F0925-29]
	s := sqlite.NewVisionStore(openRepoNameDB(t))
	p := vision.AddVisionParams{RepoName: badRepoName, Title: "t", WhyBlocked: "w"}
	if _, err := s.Add(context.Background(), p); !errors.Is(err, validator.ErrInvalidRepoName) {
		t.Errorf("Add(%q): want ErrInvalidRepoName, got %v", badRepoName, err)
	}
	p.RepoName = ""
	if _, err := s.Add(context.Background(), p); err != nil {
		t.Errorf("Add(empty repo_name): %v", err)
	}
}

// TestRepoNameBackstop_Procedural pins the SQLite procedural_memories backstop.
func TestRepoNameBackstop_Procedural(t *testing.T) {
	t.Parallel() // [F0925-29]
	s := sqlite.NewProceduralStore(openRepoNameDB(t))
	p := procedural.AddParams{RepoName: badRepoName, Title: "t", WhenToUse: "w", ApproachMD: "a"}
	if _, err := s.Add(context.Background(), p); !errors.Is(err, validator.ErrInvalidRepoName) {
		t.Errorf("Add(%q): want ErrInvalidRepoName, got %v", badRepoName, err)
	}
	p.RepoName = ""
	if _, err := s.Add(context.Background(), p); err != nil {
		t.Errorf("Add(empty repo_name): %v", err)
	}
}

// TestRepoNameBackstop_PGPoolStores covers the Postgres vision, procedural
// and work session stores. Their backstops return before the pool is
// touched, so a nil pool proves the rejection without a database; a nil
// pool reached by a passing value would panic instead.
func TestRepoNameBackstop_PGPoolStores(t *testing.T) {
	t.Parallel() // [F0925-29]
	ctx := context.Background()
	vp := vision.AddVisionParams{RepoName: badRepoName, Title: "t"}
	if _, err := vision.NewStore(nil, nil).Add(ctx, vp); !errors.Is(err, validator.ErrInvalidRepoName) {
		t.Errorf("pg vision Add(%q): want ErrInvalidRepoName, got %v", badRepoName, err)
	}
	pp := procedural.AddParams{RepoName: badRepoName, Title: "t"}
	if _, err := procedural.New(nil, nil).Add(ctx, pp); !errors.Is(err, validator.ErrInvalidRepoName) {
		t.Errorf("pg procedural Add(%q): want ErrInvalidRepoName, got %v", badRepoName, err)
	}
	wp := worksession.CreateParams{RepoName: badRepoName, Title: "t", Goal: "g", Source: "manual"}
	if _, err := worksession.NewStore(nil, nil).Create(ctx, wp); !errors.Is(err, validator.ErrInvalidRepoName) {
		t.Errorf("pg worksession Create(%q): want ErrInvalidRepoName, got %v", badRepoName, err)
	}
}

// TestRepoNameBackstop_PGDecisionAndHandoff exercises the Postgres decision
// and session stores through fakeDBTX: the backstop returns before any query,
// so a bad value fails with ErrInvalidRepoName while an empty value reaches
// the (fake) database and fails with the fake's own error instead.
func TestRepoNameBackstop_PGDecisionAndHandoff(t *testing.T) {
	t.Parallel() // [F0925-29]
	ctx := context.Background()

	ds := decision.NewStore(fakeDBTX{}, nil)
	p := cleanLogParams()
	p.RepoName = badRepoName
	if _, err := ds.Log(ctx, p); !errors.Is(err, validator.ErrInvalidRepoName) {
		t.Errorf("pg decision Log(%q): want ErrInvalidRepoName, got %v", badRepoName, err)
	}
	p.RepoName = ""
	if _, err := ds.Log(ctx, p); errors.Is(err, validator.ErrInvalidRepoName) {
		t.Errorf("pg decision Log(empty repo_name): must pass the backstop, got %v", err)
	}

	ss := session.NewStore(fakeDBTX{}, nil)
	h := cleanHandoffParams()
	h.RepoName = badRepoName
	if _, err := ss.SetHandoff(ctx, h); !errors.Is(err, validator.ErrInvalidRepoName) {
		t.Errorf("pg SetHandoff(%q): want ErrInvalidRepoName, got %v", badRepoName, err)
	}
	h.RepoName = ""
	if _, err := ss.SetHandoff(ctx, h); errors.Is(err, validator.ErrInvalidRepoName) {
		t.Errorf("pg SetHandoff(empty repo_name): must pass the backstop, got %v", err)
	}
}
