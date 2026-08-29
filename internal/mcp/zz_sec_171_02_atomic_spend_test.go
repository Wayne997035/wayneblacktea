package mcp

import (
	"context"
	"sync"
	"testing"

	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// [SEC171-02] Both confirm paths document their token as single-use —
// server.go:227 says the reconcile record is "consumed exactly once by the
// confirm call". [F170-SEC-R3-03] made refusals non-consuming by splitting the
// atomic LoadAndDelete into Load-then-validate-then-Delete, and that split
// turned the spend into check-then-act: two concurrent confirms could both
// pass validation before either deleted, and both apply. Measured during
// review at 6 of 40 iterations with 8 concurrent callers.
//
// Nothing in the existing suite could have caught it: every token test is
// sequential, and a sequential test cannot express "two callers were inside
// the window at once". That is the gap these tests close, and it is why they
// sit in their own file rather than joining the F170-SEC-R3-03 set — those pin
// the refusal semantics, these pin the spend.
//
// On flakiness: with the fix the assertion is deterministic — exactly one
// winner, every iteration, whatever the scheduler does. Without it the race is
// probabilistic, so the loop runs enough iterations that a reverted fix is
// caught with near-certainty. A test that only fails sometimes is a test the
// next person learns to re-run instead of believe.
//
// The call helpers below deliberately do not take *testing.T: the package's
// existing callDeleteTaskCtx / callReconcileCtx call t.Fatalf on error, and
// Fatalf from a non-test goroutine is invalid (it Goexits the wrong
// goroutine). Every assertion here happens on the test goroutine.

// spendIterations is sized from the review's measured hit rate — roughly 15%
// of iterations lost the race. At 40 iterations a reverted fix escapes with
// probability ~0.85^40 ≈ 0.1%.
const spendIterations = 40

// concurrentCallers matches the review's reproduction. More would not widen
// the window — they all block on the same barrier — only slow the test.
const concurrentCallers = 8

func confirmReconcileRaw(ctx context.Context, s *Server, token string) (*mcpmsg.CallToolResult, error) {
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = map[string]any{"confirm": true, "reconcile_token": token}
	return s.handleReconcileMergedPRs(ctx, req)
}

func callDeleteRaw(ctx context.Context, s *Server, args map[string]any) (*mcpmsg.CallToolResult, error) {
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	return seam("delete_task", s.handleDeleteTask)(ctx, req)
}

// TestSEC171_02_ReconcileConfirmSpendsTokenExactlyOnce fires N confirms at one
// token from a barrier and asserts exactly one applies.
//
// Mutation proof: put the unconditional Delete back in place of the
// LoadAndDelete spend in handleReconcileMergedPRsConfirm — this goes red.
func TestSEC171_02_ReconcileConfirmSpendsTokenExactlyOnce(t *testing.T) {
	for i := range spendIterations {
		s := withReconcileCandidates(t, newTestWorkSessionServer(t))
		branch := "feature/sec171-02-reconcile"
		seedBranchedTask(t, s, "atomic reconcile spend", branch)

		ctx := s.MCPServer().WithContext(context.Background(), fakeClientSession{id: "sec171-02-A"})
		preview := extractReconcileToken(t, callReconcileCtx(t, ctx, s, reconcilePayloadForBranch(branch), nil))
		if len(preview.Matches) != 1 {
			t.Fatalf("iteration %d: preview matches = %d, want 1 — the fixture did not match, so "+
				"the confirms below would be racing over nothing", i, len(preview.Matches))
		}

		var start, done sync.WaitGroup
		results := make([]bool, concurrentCallers)
		errs := make([]error, concurrentCallers)
		start.Add(1)
		for c := range concurrentCallers {
			done.Add(1)
			go func() {
				defer done.Done()
				start.Wait()
				r, err := confirmReconcileRaw(ctx, s, preview.ReconcileToken)
				errs[c] = err
				results[c] = err == nil && r != nil && !r.IsError
			}()
		}
		start.Done()
		done.Wait()

		wins := 0
		for c := range concurrentCallers {
			if errs[c] != nil {
				t.Fatalf("iteration %d caller %d: handler returned a Go error: %v", i, c, errs[c])
			}
			if results[c] {
				wins++
			}
		}
		if wins != 1 {
			t.Fatalf("iteration %d: %d of %d concurrent confirms applied, want exactly 1 — the "+
				"record is documented as consumed exactly once", i, wins, concurrentCallers)
		}
	}
}

// TestSEC171_02_DeleteTaskConfirmSpendsTokenExactlyOnce is the same property on
// delete_task.
//
// Mutation proof: replace the CompareAndDelete spend in handleDeleteTask with
// an unconditional Delete — this goes red.
func TestSEC171_02_DeleteTaskConfirmSpendsTokenExactlyOnce(t *testing.T) {
	for i := range spendIterations {
		s := newTestWorkSessionServer(t)
		id := seedTask(t, s)
		ctx := s.MCPServer().WithContext(context.Background(), fakeClientSession{id: "sec171-02-D"})
		token := extractToken(t, callDeleteTaskCtx(t, ctx, s, map[string]any{"task_id": id.String()}))

		var start, done sync.WaitGroup
		results := make([]bool, concurrentCallers)
		errs := make([]error, concurrentCallers)
		start.Add(1)
		for c := range concurrentCallers {
			done.Add(1)
			go func() {
				defer done.Done()
				start.Wait()
				r, err := callDeleteRaw(ctx, s, map[string]any{
					"task_id":        id.String(),
					"confirm":        true,
					"deletion_token": token,
				})
				errs[c] = err
				results[c] = err == nil && r != nil && !r.IsError
			}()
		}
		start.Done()
		done.Wait()

		wins := 0
		for c := range concurrentCallers {
			if errs[c] != nil {
				t.Fatalf("iteration %d caller %d: handler returned a Go error: %v", i, c, errs[c])
			}
			if results[c] {
				wins++
			}
		}
		if wins != 1 {
			t.Fatalf("iteration %d: %d of %d concurrent confirms deleted, want exactly 1",
				i, wins, concurrentCallers)
		}
	}
}

// UNCOVERED, deliberately and on the record: the second half of SEC171-02.
//
// deleteTokens is keyed by TASK ID, not by the token, so an unconditional
// Delete at the spend removes whatever occupies that key at that instant —
// possibly a record another session created after we loaded ours. That is why
// handleDeleteTask spends with CompareAndDelete while reconcile uses
// LoadAndDelete: reconcile's key IS the token, so no other record can ever
// occupy it.
//
// There is no test below for it, and that is a finding, not an omission. A
// black-box test cannot reach the interleaving: it needs another session's
// issue to land strictly between our Load and our spend, and the handler
// exposes no seam there. The attempt is recorded rather than deleted quietly —
// a barrier race of A-spends-while-B-issues, 40 iterations, reached the window
// 0 times, because the two orderings that ARE reachable both end early (B
// first → A hits token-mismatch; A first → the task is gone before B can be
// issued a token). It was removed rather than left in as a permanent t.Skip,
// because a test that never executes its assertion still reads, in a list of
// test names, exactly like a guard.
//
// So this property currently rests on the code and its comment alone. Closing
// it means either a test-only seam around the spend or an integration-level
// harness that can hold a handler mid-flight — tracked in GTD d847829f. Anyone
// "simplifying" the two spends into one shared helper should start there: the
// double-spend tests above stay green under that change, so they will not stop
// it.
