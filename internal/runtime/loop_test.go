package runtime

import (
	"context"
	"errors"
	"testing"
	"time"
)

// countingCaller counts Generate calls. If failAfter > 0 it calls t.Fatalf once
// calls exceeds it, so a broken budget check fails loudly instead of hanging.
type countingCaller struct {
	t         *testing.T
	resp      GenerateResponse
	err       error
	failAfter int
	calls     int
}

func (c *countingCaller) Generate(_ context.Context, _ GenerateRequest) (GenerateResponse, error) {
	c.calls++
	if c.failAfter > 0 && c.calls > c.failAfter {
		c.t.Fatalf("Generate called %d times, over the guard of %d: budget check did not stop the loop", c.calls, c.failAfter)
	}
	return c.resp, c.err
}

// F1124-30
func TestRuntimeLoop_InteractiveRun_HappyPath(t *testing.T) {
	caller := fixedResponseModelCaller{Response: GenerateResponse{Output: "hello", FinishReason: "stop", HadCapabilityWrite: true}}
	p := RunParams{
		Trigger:   Trigger{Type: "user_intent", Ref: "r1"},
		Principal: Principal{Kind: "user", ID: "u1"},
		Budget:    Budget{MaxTurns: 12, MaxDuration: 120 * time.Second},
	}
	run, err := RunLoop(context.Background(), caller, p, LoopOptions{})
	if err != nil {
		t.Fatalf("RunLoop err = %v, want nil", err)
	}
	if run.Status != StatusCompleted {
		t.Errorf("Status = %q, want %q", run.Status, StatusCompleted)
	}
	if run.FinalOutput != "hello" {
		t.Errorf("FinalOutput = %q, want %q", run.FinalOutput, "hello")
	}
	if len(run.Turns) != 1 {
		t.Fatalf("len(Turns) = %d, want 1", len(run.Turns))
	}
	if run.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want stop", run.FinishReason)
	}
	if !run.HadCapabilityWrite {
		t.Error("HadCapabilityWrite = false, want true (OR of turns)")
	}
	if run.ContextDigest == "" || run.Turns[0].RunID != run.ID || run.Turns[0].Sequence != 1 || run.FinishedAt == nil {
		t.Errorf("run/turn bookkeeping not filled: %+v", run)
	}
}

// F1124-30 (error path: model failure)
func TestRuntimeLoop_ModelError_FailsRun(t *testing.T) {
	boom := errors.New("boom")
	caller := &countingCaller{t: t, err: boom}
	run, err := RunLoop(context.Background(), caller, RunParams{Budget: NewInteractiveBudget()}, LoopOptions{})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrapping boom", err)
	}
	if run.Status != StatusFailed || run.ErrorCode != errorCodeModel || len(run.Turns) != 0 {
		t.Errorf("run = %+v, want failed/model_error/0 turns", run)
	}
	if errors.Is(err, ErrBudgetExceeded) || errors.Is(err, ErrInvalidBudget) {
		t.Errorf("model error misclassified as budget error: %v", err)
	}
}

// F1124-31
func TestRuntimeLoop_MaxTurnsExceeded_FinishReasonSet(t *testing.T) {
	const maxTurns = 2
	caller := &countingCaller{t: t, resp: GenerateResponse{FinishReason: ""}, failAfter: maxTurns + 3}
	p := RunParams{Budget: Budget{MaxTurns: maxTurns, MaxDuration: 120 * time.Second}}
	run, err := RunLoop(context.Background(), caller, p, LoopOptions{})
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("err = %v, want ErrBudgetExceeded", err)
	}
	if errors.Is(err, ErrInvalidBudget) {
		t.Errorf("err must not be ErrInvalidBudget: %v", err)
	}
	if run.Status != StatusFailed {
		t.Errorf("Status = %q, want %q", run.Status, StatusFailed)
	}
	if run.FinishReason != FinishReasonMaxTurns {
		t.Errorf("FinishReason = %q, want %q", run.FinishReason, FinishReasonMaxTurns)
	}
	if len(run.Turns) != maxTurns {
		t.Errorf("len(Turns) = %d, want exactly %d", len(run.Turns), maxTurns)
	}
	if caller.calls != maxTurns {
		t.Errorf("Generate calls = %d, want exactly %d", caller.calls, maxTurns)
	}
}

// F1124-32
func TestRuntimeLoop_ZeroBudget_RejectedFailClosed(t *testing.T) {
	cases := []struct {
		name   string
		budget Budget
	}{
		{"zero value", Budget{}},
		{"MaxTurns zero", Budget{MaxTurns: 0, MaxDuration: 120 * time.Second}},
		{"MaxTurns negative", Budget{MaxTurns: -1, MaxDuration: 120 * time.Second}},
		{"MaxDuration zero", Budget{MaxTurns: 12, MaxDuration: 0}},
		{"MaxDuration negative", Budget{MaxTurns: 12, MaxDuration: -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Returns "stop" so that, if the check were removed, the loop
			// would run exactly one turn and calls==1 would turn this red.
			caller := &countingCaller{t: t, resp: GenerateResponse{Output: "x", FinishReason: "stop"}, failAfter: 5}
			run, err := RunLoop(context.Background(), caller, RunParams{Budget: tc.budget}, LoopOptions{})
			if !errors.Is(err, ErrInvalidBudget) {
				t.Errorf("err = %v, want ErrInvalidBudget", err)
			}
			if errors.Is(err, ErrBudgetExceeded) {
				t.Errorf("zero budget misclassified as ErrBudgetExceeded: %v", err)
			}
			if caller.calls != 0 {
				t.Errorf("Generate calls = %d, want 0", caller.calls)
			}
			if len(run.Turns) != 0 || run.ID.String() != "00000000-0000-0000-0000-000000000000" || run.Status != "" {
				t.Errorf("run = %+v, want zero value", run)
			}
		})
	}
}

// F1124-32 (positive control: cost=0 and tool-calls<=0 are not rejected)
func TestRuntimeLoop_InteractiveBudget_Accepted(t *testing.T) {
	budgets := map[string]Budget{
		"interactive defaults":  NewInteractiveBudget(),
		"tool calls unset":      {MaxTurns: 12, MaxDuration: time.Second, MaxToolCalls: 0},
		"cost unset explicitly": {MaxTurns: 1, MaxDuration: time.Second, MaxCostMicroUSD: 0},
	}
	for name, b := range budgets {
		t.Run(name, func(t *testing.T) {
			caller := &countingCaller{t: t, resp: GenerateResponse{Output: "ok", FinishReason: "stop"}, failAfter: 5}
			run, err := RunLoop(context.Background(), caller, RunParams{Budget: b}, LoopOptions{})
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if run.Status != StatusCompleted || caller.calls != 1 {
				t.Errorf("Status=%q calls=%d, want completed/1", run.Status, caller.calls)
			}
		})
	}
	if b := NewInteractiveBudget(); b.MaxCostMicroUSD != 0 || b.MaxToolCalls != 20 {
		t.Errorf("NewInteractiveBudget = %+v, want cost 0 and tool calls 20", b)
	}
}
