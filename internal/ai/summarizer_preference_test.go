package ai

import (
	"context"
	"errors"
	"testing"
)

const fallbackModel = "fallback-model"

type fakePref struct {
	model string
	err   error
}

func (f fakePref) GetModelPreference(_ context.Context) (string, error) {
	return f.model, f.err
}

func TestModelForCall_DBWins(t *testing.T) {
	s := &Summarizer{model: "env-or-default", pref: fakePref{model: "claude-opus-4-8"}}
	if got := s.modelForCall(context.Background()); got != "claude-opus-4-8" {
		t.Errorf("got %q, want claude-opus-4-8 (DB preference should win)", got)
	}
}

func TestModelForCall_DBErrorFallsToConstructorModel(t *testing.T) {
	s := &Summarizer{model: fallbackModel, pref: fakePref{err: errors.New("db down")}}
	if got := s.modelForCall(context.Background()); got != fallbackModel {
		t.Errorf("got %q, want fallback-model (DB error must fall through)", got)
	}
}

func TestModelForCall_DBEmptyFallsToConstructorModel(t *testing.T) {
	s := &Summarizer{model: fallbackModel, pref: fakePref{model: ""}}
	if got := s.modelForCall(context.Background()); got != fallbackModel {
		t.Errorf("got %q, want fallback-model (empty DB value → fallback)", got)
	}
}

func TestModelForCall_DBNotAllowlisted_FallsBack(t *testing.T) {
	// Defence-in-depth (M-2): a tampered DB value not in the allowlist must
	// never reach the API call — fall back to the constructor model.
	s := &Summarizer{model: fallbackModel, pref: fakePref{model: "gpt-4-tampered"}}
	if got := s.modelForCall(context.Background()); got != fallbackModel {
		t.Errorf("got %q, want fallback-model (non-allowlisted DB value must fall back)", got)
	}
}

func TestModelForCall_NilPrefUsesConstructorModel(t *testing.T) {
	s := &Summarizer{model: "env-or-default", pref: nil}
	if got := s.modelForCall(context.Background()); got != "env-or-default" {
		t.Errorf("got %q, want env-or-default (nil pref)", got)
	}
}

func TestNewWithPreference_NilPrefBehavesLikeNew(t *testing.T) {
	s := NewWithPreference("test-key-1234567890", nil)
	if s.pref != nil {
		t.Errorf("expected nil pref reader")
	}
	if s.model == "" {
		t.Errorf("model should be resolved from env/default")
	}
}
