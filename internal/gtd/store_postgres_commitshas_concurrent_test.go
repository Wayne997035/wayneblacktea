package gtd_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

// TestCompleteTask_ConcurrentArtifactAppend_Postgres is U5's PG bad-case red
// test (P7, 2026-08-20-mcp-surface-spec.md). Before this fix, UpdateTask
// merged commit_shas by reading the existing array in Go and writing the
// whole array back — two concurrent callers both read the same pre-update
// array, and whichever write committed last silently discarded the other's
// SHA (TOCTOU). commit_shas is now appended atomically at the SQL layer
// (array_append inside a CASE expression, gtd/store.go's UpdateTask).
//
// A minimal 2-goroutine race (this test's literal spec wording) turned out
// too narrow a window to reliably reproduce against a local Docker
// container — verified empirically by temporarily reverting the fix and
// running the 2-goroutine shape 8x, all 8 passed "by luck" even against the
// old buggy code. The SAME revert reproduces total data loss (3 of 50
// survived) once concurrency is raised to 20+, which is what the loop below
// exercises: it still literally satisfies "2 concurrent calls, both survive"
// as the first two entries of a larger burst, but is an actually reliable
// regression guard rather than one that would pass against either version
// of the code.
func TestCompleteTask_ConcurrentArtifactAppend_Postgres(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "concurrent commit_shas PG"})
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
			if _, err := store.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{AppendCommitSHA: &sha}); err != nil {
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

	got, err := store.GetTaskByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	// bad case: len must be n, not fewer — the pre-fix behaviour silently
	// dropped whichever goroutines' writes lost the race (empirically down to
	// 3 of 50 survived under the reverted code at this concurrency level).
	if len(got.CommitSHAs) != n {
		t.Fatalf("commit_shas has len %d, want %d (every concurrent append must survive, none silently dropped)",
			len(got.CommitSHAs), n)
	}
	present := map[string]bool{}
	for _, s := range got.CommitSHAs {
		present[s] = true
	}
	for _, sha := range shas {
		if !present[sha] {
			t.Errorf("commit_shas is missing %q: %v", sha, got.CommitSHAs)
		}
	}
}
