package mcp

import (
	"context"
	"testing"
	"time"

	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// [SEC171-02] Both confirm paths document their token as single-use —
// server.go:227 says the reconcile record is "consumed exactly once by the
// confirm call". [F170-SEC-R3-03] made refusals non-consuming by splitting the
// atomic LoadAndDelete into Load-then-validate-then-Delete, and that split
// turned the spend into check-then-act: two concurrent confirms could both
// pass validation before either deleted, and both apply.
//
// [F171-03] These tests used to fire N goroutines at a shared barrier and
// count wins over spendIterations=40 runs, on the theory that enough
// iterations makes a reverted fix escape with near-zero probability. Measured
// by an independent reviewer on this exact suite: the reconcile test's
// documented ~0.1% escape rate held, but the sibling delete_task test's real
// escape rate was 14/50 = 28% — almost 300x worse than the comment claimed,
// for a test asserting the same property with the same iteration budget. A
// probabilistic regression test's escape rate is a property of the CODE
// PATH's timing, not of the iteration count alone, and nothing in a
// goroutine-barrier design tells you which regime you are in until an
// independent party measures it.
//
// The fix does not raise the iteration count — a bigger number is still a
// probability, just a smaller one, and the two paths already disagreed by
// 280x under an identical budget with no visible reason why. Both handlers
// call s.now() exactly once, at the TTL check, strictly between Load and the
// atomic spend (handleDeleteTask: Load then s.now() then CompareAndDelete;
// handleReconcileMergedPRsConfirm: Load then s.now() then LoadAndDelete) —
// see server.go's nowFn field. Overriding s.nowFn to run a second, complete
// confirm call INSIDE the first call's TTL check puts a competing call
// exactly in the race window on every run, in a single goroutine, with the
// scheduler never consulted. This does not "widen" the window the way more
// barrier iterations narrow the miss probability — it makes the interleaving
// a fact about the test's control flow rather than a fact about luck, so
// there is no escape rate to measure or to get wrong a second time.
//
// The call helpers below deliberately do not take *testing.T inside a
// goroutine: there are none left in this file. The nested confirm call runs
// on the same goroutine as the outer one, synchronously, via the s.nowFn
// hook — Fatalf from inside that hook is valid because it is still the test
// goroutine.

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

// wins counts how many of the supplied results represent a successful
// (non-error) confirm — the shared assertion shape for both tests below.
func wins(results ...*mcpmsg.CallToolResult) int {
	n := 0
	for _, r := range results {
		if r != nil && !r.IsError {
			n++
		}
	}
	return n
}

// TestSEC171_02_ReconcileConfirmSpendsTokenExactlyOnce deterministically
// interleaves two confirms of the SAME reconcile_token via the s.nowFn seam
// (tools_reconcile.go's handleReconcileMergedPRsConfirm calls s.now() once,
// strictly between Load and the LoadAndDelete spend) and asserts exactly one
// applies.
//
// Mutation proof: put the unconditional Delete back in place of the
// LoadAndDelete spend in handleReconcileMergedPRsConfirm — this goes red.
func TestSEC171_02_ReconcileConfirmSpendsTokenExactlyOnce(t *testing.T) {
	s := withReconcileCandidates(t, newTestWorkSessionServer(t))
	branch := "feature/sec171-02-reconcile"
	seedBranchedTask(t, s, "atomic reconcile spend", branch)

	ctx := s.MCPServer().WithContext(context.Background(), fakeClientSession{id: "sec171-02-A"})
	preview := extractReconcileToken(t, callReconcileCtx(t, ctx, s, reconcilePayloadForBranch(branch), nil))
	if len(preview.Matches) != 1 {
		t.Fatalf("preview matches = %d, want 1 — the fixture did not match, so the two confirms "+
			"below would be racing over nothing", len(preview.Matches))
	}

	real := s.nowFn
	var fired bool
	var second *mcpmsg.CallToolResult
	s.nowFn = func() time.Time {
		if !fired {
			fired = true
			r, err := confirmReconcileRaw(ctx, s, preview.ReconcileToken)
			if err != nil {
				t.Fatalf("second (nested) confirm returned a Go error: %v", err)
			}
			second = r
		}
		return real()
	}
	first, err := confirmReconcileRaw(ctx, s, preview.ReconcileToken)
	s.nowFn = real
	if err != nil {
		t.Fatalf("first confirm returned a Go error: %v", err)
	}

	if w := wins(first, second); w != 1 {
		t.Fatalf("%d of 2 interleaved confirms applied, want exactly 1 — the record is documented "+
			"as consumed exactly once (first.IsError=%v second.IsError=%v)", w, first.IsError, second.IsError)
	}
}

// TestSEC171_02_DeleteTaskConfirmSpendsTokenExactlyOnce is the same property
// on delete_task, via the same s.nowFn seam (handleDeleteTask also calls
// s.now() exactly once, strictly between Load and the CompareAndDelete
// spend).
//
// Mutation proof: replace the CompareAndDelete spend in handleDeleteTask with
// an unconditional Delete — this goes red.
func TestSEC171_02_DeleteTaskConfirmSpendsTokenExactlyOnce(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)
	ctx := s.MCPServer().WithContext(context.Background(), fakeClientSession{id: "sec171-02-D"})
	token := extractToken(t, callDeleteTaskCtx(t, ctx, s, map[string]any{"task_id": id.String()}))

	real := s.nowFn
	var fired bool
	var second *mcpmsg.CallToolResult
	s.nowFn = func() time.Time {
		if !fired {
			fired = true
			r, err := callDeleteRaw(ctx, s, map[string]any{
				"task_id":        id.String(),
				"confirm":        true,
				"deletion_token": token,
			})
			if err != nil {
				t.Fatalf("second (nested) confirm returned a Go error: %v", err)
			}
			second = r
		}
		return real()
	}
	first, err := callDeleteRaw(ctx, s, map[string]any{
		"task_id":        id.String(),
		"confirm":        true,
		"deletion_token": token,
	})
	s.nowFn = real
	if err != nil {
		t.Fatalf("first confirm returned a Go error: %v", err)
	}

	if w := wins(first, second); w != 1 {
		t.Fatalf("%d of 2 interleaved confirms deleted, want exactly 1 (first.IsError=%v second.IsError=%v)",
			w, first.IsError, second.IsError)
	}
}

// TestSEC171_02_SpendDoesNotConsumeReplacementToken is [TRC171-01]'s second
// half: it is what actually distinguishes CompareAndDelete from
// LoadAndDelete at the spend, which
// TestSEC171_02_DeleteTaskConfirmSpendsTokenExactlyOnce above does not — that
// test double-spends the SAME token from the SAME session, and LoadAndDelete
// is already atomic enough to make that impossible on its own (whichever of
// the two interleaved calls reaches the atomic op first empties the key, so
// the second necessarily sees it gone). The gap only appears when a
// DIFFERENT session's record occupies the key by the time of the spend.
//
// Session A obtains a token, then confirms with that SAME (correct) token —
// but in the window between A's Load and A's TTL check, session B
// independently replaces the record for the SAME task id. A's local `rec`
// was captured before B's overwrite, so A's token still matches `rec.token`
// and A passes every validation check using its own now-stale copy. At the
// spend, the map holds B's record, not A's. CompareAndDelete only removes
// the entry if it is STILL the one A validated, so A's spend is correctly
// refused and B's replacement survives. LoadAndDelete would delete WHATEVER
// occupies the key regardless of identity, so A's spend would both succeed
// — using authorization that was already superseded — AND destroy B's
// separate, still-valid pending deletion: a strictly worse outcome than the
// mismatch-refusal case SEC171-13 covers, because here A's own validation
// never even detects the replacement.
//
// Mutation proof: put LoadAndDelete (or unconditional Delete) back on the
// spend (tools_gtd.go, `if !s.deleteTokens.CompareAndDelete(id.String(),
// stored)`) — this goes red either way.
func TestSEC171_02_SpendDoesNotConsumeReplacementToken(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)

	ctxA := s.MCPServer().WithContext(context.Background(), fakeClientSession{id: "sec171-02-spend-A"})
	tokenA := extractToken(t, callDeleteTaskCtx(t, ctxA, s, map[string]any{"task_id": id.String()}))

	real := s.nowFn
	var fired bool
	var tokenB string
	s.nowFn = func() time.Time {
		if !fired {
			fired = true
			ctxB := s.MCPServer().WithContext(context.Background(), fakeClientSession{id: "sec171-02-spend-B"})
			tokenB = extractToken(t, callDeleteTaskCtx(t, ctxB, s, map[string]any{"task_id": id.String()}))
		}
		return real()
	}
	confirmA := callDeleteTaskCtx(t, ctxA, s, map[string]any{
		"task_id":        id.String(),
		"confirm":        true,
		"deletion_token": tokenA,
	})
	s.nowFn = real
	if !confirmA.IsError {
		t.Fatalf("A's spend used authorization that was superseded by B's replacement and should "+
			"have been refused, got success: %s", resultText(confirmA))
	}

	ctxB := s.MCPServer().WithContext(context.Background(), fakeClientSession{id: "sec171-02-spend-B"})
	confirmB := callDeleteTaskCtx(t, ctxB, s, map[string]any{
		"task_id":        id.String(),
		"confirm":        true,
		"deletion_token": tokenB,
	})
	if confirmB.IsError {
		t.Fatalf("B's replacement token was destroyed by A's stale spend: %s", resultText(confirmB))
	}
}

// TestSEC171_13_MismatchRefusalDoesNotDestroyReplacementToken is
// [F171-05]/[SEC171-13]'s regression test, sharing the s.nowFn seam above.
//
// Session A obtains a token for a task, then confirms with a WRONG token —
// the mismatch refusal branch. In the window between A's Load and A's TTL
// check, session B independently obtains a fresh token FOR THE SAME TASK ID
// (deleteTokens is keyed by task id, not by token, so B's step-1 issuance
// silently overwrites A's record). Before this fix, the mismatch branch did
// an unconditional Delete, which removed whatever occupied the key at that
// instant — B's brand-new record, not A's stale one — so a caller who merely
// guessed wrong could destroy a different session's pending deletion. After
// the fix, the mismatch branch's CompareAndDelete only removes the record it
// actually validated; since that record (A's) no longer occupies the key, it
// is a no-op, and B's token survives.
//
// Mutation proof: put the unconditional Delete back on the mismatch branch
// (tools_gtd.go, the `if suppliedToken != rec.token` block) — this goes red.
func TestSEC171_13_MismatchRefusalDoesNotDestroyReplacementToken(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)

	ctxA := s.MCPServer().WithContext(context.Background(), fakeClientSession{id: "sec171-13-A"})
	tokenA := extractToken(t, callDeleteTaskCtx(t, ctxA, s, map[string]any{"task_id": id.String()}))

	real := s.nowFn
	var fired bool
	var tokenB string
	s.nowFn = func() time.Time {
		if !fired {
			fired = true
			ctxB := s.MCPServer().WithContext(context.Background(), fakeClientSession{id: "sec171-13-B"})
			tokenB = extractToken(t, callDeleteTaskCtx(t, ctxB, s, map[string]any{"task_id": id.String()}))
		}
		return real()
	}
	confirmA := callDeleteTaskCtx(t, ctxA, s, map[string]any{
		"task_id":        id.String(),
		"confirm":        true,
		"deletion_token": tokenA + "-wrong",
	})
	s.nowFn = real
	if !confirmA.IsError {
		t.Fatalf("A's wrong-token confirm should have been refused, got success: %s", resultText(confirmA))
	}

	ctxB := s.MCPServer().WithContext(context.Background(), fakeClientSession{id: "sec171-13-B"})
	confirmB := callDeleteTaskCtx(t, ctxB, s, map[string]any{
		"task_id":        id.String(),
		"confirm":        true,
		"deletion_token": tokenB,
	})
	if confirmB.IsError {
		t.Fatalf("B's replacement token was destroyed by A's mismatch refusal: %s", resultText(confirmB))
	}
}
