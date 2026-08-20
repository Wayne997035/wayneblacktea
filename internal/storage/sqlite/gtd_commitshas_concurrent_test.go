package sqlite_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
)

// TestCompleteTask_ConcurrentArtifactAppend_SQLite is U5's SQLite bad-case
// red test (P7, 2026-08-20-mcp-surface-spec.md), mirroring
// TestCompleteTask_ConcurrentArtifactAppend_Postgres (internal/gtd package).
// Before this fix, UpdateTask merged commit_shas by reading the existing
// array in Go and writing the whole array back — two concurrent callers both
// read the same pre-update array, and whichever write committed last
// silently discarded the other's SHA (TOCTOU). This race is real even though
// sqlite.Open sets SetMaxOpenConns(1) (internal/storage/sqlite/db.go): the
// SELECT (taskByID) and UPDATE inside UpdateTask are two separate
// acquire/release round trips on that one connection, not a single held
// transaction, so database/sql can still interleave two different goroutines'
// calls between them. commit_shas is now appended atomically at the SQL
// layer (json_insert inside a CASE expression, sqlite/gtd.go's UpdateTask).
//
// Same concurrency-level note as the PG test: a literal 2-goroutine race was
// too narrow a window to reliably reproduce locally (verified empirically by
// temporarily reverting the fix); the loop below still trivially covers "2
// concurrent calls, both survive" as the first two entries of a larger burst,
// while actually being a reliable regression guard.
func TestCompleteTask_ConcurrentArtifactAppend_SQLite(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	task, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "concurrent commit_shas SQLite"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	const n = 20
	shas := make([]string, n)
	for i := range shas {
		shas[i] = fmt.Sprintf("%040d", i) // 40-char, distinct, valid-shaped SHA
	}

	var wg sync.WaitGroup
	var ready sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, n)
	wg.Add(n)
	ready.Add(n)
	for i := range shas {
		sha := shas[i]
		go func() {
			ready.Done()
			<-start
			defer wg.Done()
			if _, err := s.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{AppendCommitSHA: &sha}); err != nil {
				errs <- err
			}
		}()
	}
	ready.Wait() // every goroutine parked at <-start before any can begin its call
	close(start) // release all as close to simultaneously as the scheduler allows
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent UpdateTask failed: %v", err)
	}

	got, err := s.GetTaskByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	// bad case: len must be n, not fewer — the pre-fix behaviour silently
	// dropped whichever goroutines' writes lost the race.
	if len(got.CommitSHAs) != n {
		t.Fatalf("commit_shas has len %d, want %d (every concurrent append must survive, none silently dropped)",
			len(got.CommitSHAs), n)
	}
	present := map[string]bool{}
	for _, sha := range got.CommitSHAs {
		present[sha] = true
	}
	for _, sha := range shas {
		if !present[sha] {
			t.Errorf("commit_shas is missing %q: %v", sha, got.CommitSHAs)
		}
	}
}
