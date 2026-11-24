package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrInvalidBudget means the budget given to RunLoop has a non-positive
	// MaxTurns or MaxDuration. RunLoop fails closed instead of running with no
	// limit. [F1124-32]
	ErrInvalidBudget = errors.New("runtime: invalid budget")
	// ErrBudgetExceeded means a run stopped because a budget dimension was
	// exceeded (distinct from ErrInvalidBudget).
	ErrBudgetExceeded = errors.New("runtime: budget exceeded")
)

// ErrorCode values stored in Run.ErrorCode.
const errorCodeModel = "model_error"

// LoopOptions carries optional loop behaviour. Empty until slices 3e/3f.
type LoopOptions struct{}

// GenerateRequest is what the loop sends to a ModelCaller.
type GenerateRequest struct {
	ContextDigest string
	PriorTurns    []Turn
}

// GenerateResponse is what a ModelCaller returns. An empty FinishReason means
// the model has not signalled stop yet.
type GenerateResponse struct {
	Output             string
	FinishReason       FinishReason
	HadCapabilityWrite bool
}

// ModelCaller produces one model turn.
type ModelCaller interface {
	Generate(ctx context.Context, req GenerateRequest) (GenerateResponse, error)
}

// fixedResponseModelCaller always returns the same canned response. It is the
// placeholder model client until slice 5c.
type fixedResponseModelCaller struct {
	Response GenerateResponse
	Err      error
}

func (f fixedResponseModelCaller) Generate(_ context.Context, _ GenerateRequest) (GenerateResponse, error) {
	return f.Response, f.Err
}

func sha256Digest(s string) string {
	h := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(h[:])
}

// compileContext is a minimal stub: a digest of the trigger and principal.
func compileContext(p RunParams) string {
	return sha256Digest(p.Trigger.Type + p.Trigger.Ref + p.Principal.Kind + p.Principal.ID)
}

// RunLoop runs the minimal interactive loop: validate budget, compile context,
// then call the model once per turn until it signals stop or the budget is
// exceeded.
//
// [F1124-32] A budget with MaxTurns <= 0 or MaxDuration <= 0 is rejected
// before compileContext and before any Generate call. MaxCostMicroUSD and
// MaxToolCalls <= 0 are not rejected (no default cost; tool calls unevaluated).
// This is deliberately different from Budget.CheckExceeded, where <= 0
// disables a dimension; do not unify the two.
func RunLoop(ctx context.Context, caller ModelCaller, p RunParams, _ LoopOptions) (Run, error) {
	if p.Budget.MaxTurns <= 0 || p.Budget.MaxDuration <= 0 {
		return Run{}, fmt.Errorf("%w: MaxTurns=%d MaxDuration=%s must both be positive",
			ErrInvalidBudget, p.Budget.MaxTurns, p.Budget.MaxDuration)
	}

	start := time.Now()
	run := Run{
		ID:                uuid.New(),
		WorkspaceID:       p.WorkspaceID,
		Trigger:           p.Trigger,
		Principal:         p.Principal,
		PresenceSessionID: p.PresenceSessionID,
		Status:            StatusRunning,
		Budget:            p.Budget,
		ContextDigest:     compileContext(p),
		StartedAt:         start,
	}

	finish := func(status Status, reason FinishReason) {
		now := time.Now()
		run.Status = status
		run.FinishReason = reason
		run.FinishedAt = &now
	}

	for seq := 1; ; seq++ {
		// [F1124-31] Judge the budget before the turn starts, with the
		// sequence number of the turn about to run (first turn = 1).
		if exceeded, reason := p.Budget.CheckExceeded(Elapsed{Turns: seq, Duration: time.Since(start)}); exceeded {
			finish(StatusFailed, reason)
			return run, fmt.Errorf("%w: %s", ErrBudgetExceeded, reason)
		}

		turnStart := time.Now()
		resp, err := caller.Generate(ctx, GenerateRequest{
			ContextDigest: run.ContextDigest,
			PriorTurns:    run.Turns,
		})
		if err != nil {
			run.ErrorCode = errorCodeModel
			finish(StatusFailed, "")
			return run, fmt.Errorf("runtime: generate turn %d: %w", seq, err)
		}

		turnEnd := time.Now()
		run.Turns = append(run.Turns, Turn{
			ID:           uuid.New(),
			RunID:        run.ID,
			Sequence:     seq,
			InputDigest:  run.ContextDigest,
			OutputDigest: sha256Digest(resp.Output),
			StartedAt:    turnStart,
			FinishedAt:   &turnEnd,
			FinishReason: resp.FinishReason,
		})
		// [F1124-30] Carry the model output forward as the user-visible answer.
		run.FinalOutput = resp.Output
		run.HadCapabilityWrite = run.HadCapabilityWrite || resp.HadCapabilityWrite

		if resp.FinishReason != "" {
			finish(StatusCompleted, resp.FinishReason)
			return run, nil
		}
	}
}
