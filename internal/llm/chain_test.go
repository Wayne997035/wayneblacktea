package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// stubClient is a deterministic JSONClient used by chain tests.
type stubClient struct {
	name  string
	out   string
	err   error
	calls int
}

func (s *stubClient) Name() string { return s.name }

func (s *stubClient) CompleteJSON(_ context.Context, _ JSONRequest) (string, error) {
	s.calls++
	return s.out, s.err
}

// TestChain_EmptyReturnsErrNoProviders verifies that a chain with zero
// providers returns ErrNoProviders so callers can degrade to memory-only.
func TestChain_EmptyReturnsErrNoProviders(t *testing.T) {
	c := NewChain()
	if c.Len() != 0 {
		t.Errorf("Len = %d, want 0", c.Len())
	}
	out, err := c.CompleteJSON(context.Background(), JSONRequest{})
	if !errors.Is(err, ErrNoProviders) {
		t.Errorf("err = %v, want ErrNoProviders", err)
	}
	if out != "" {
		t.Errorf("out = %q, want empty", out)
	}
}

// TestChain_FirstSuccessShortCircuits verifies that the first successful
// provider stops the chain — secondary providers are not called.
func TestChain_FirstSuccessShortCircuits(t *testing.T) {
	a := &stubClient{name: "a", out: "ok"}
	b := &stubClient{name: "b", out: "should-not-be-called"}
	c := NewChain(a, b)
	out, err := c.CompleteJSON(context.Background(), JSONRequest{Task: "test"})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if out != "ok" {
		t.Errorf("out = %q, want %q", out, "ok")
	}
	if a.calls != 1 {
		t.Errorf("a.calls = %d, want 1", a.calls)
	}
	if b.calls != 0 {
		t.Errorf("b.calls = %d, want 0 (chain should short-circuit)", b.calls)
	}
}

// TestChain_RetryableFailoverWalksList verifies that Retryable errors fall
// through and the next provider is tried.
func TestChain_RetryableFailoverWalksList(t *testing.T) {
	a := &stubClient{name: "a", err: &Retryable{Provider: "a", Reason: "http_429"}}
	b := &stubClient{name: "b", err: &Retryable{Provider: "b", Reason: "http_5xx"}}
	d := &stubClient{name: "d", out: "third-time-lucky"}
	c := NewChain(a, b, d)
	out, err := c.CompleteJSON(context.Background(), JSONRequest{Task: "test"})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if out != "third-time-lucky" {
		t.Errorf("out = %q", out)
	}
	if a.calls != 1 || b.calls != 1 || d.calls != 1 {
		t.Errorf("calls a=%d b=%d d=%d, all want 1", a.calls, b.calls, d.calls)
	}
}

// TestChain_AllFailedReturnsAggregate verifies the aggregate error names
// every attempted provider so operators can debug the chain.
func TestChain_AllFailedReturnsAggregate(t *testing.T) {
	a := &stubClient{name: "a", err: &Retryable{Provider: "a", Reason: "timeout"}}
	b := &stubClient{name: "b", err: &Retryable{Provider: "b", Reason: "invalid_json"}}
	c := NewChain(a, b)
	_, err := c.CompleteJSON(context.Background(), JSONRequest{Task: "test"})
	var agg *ErrAllProvidersFailed
	if !errors.As(err, &agg) {
		t.Fatalf("err type = %T, want *ErrAllProvidersFailed", err)
	}
	if len(agg.Attempts) != 2 {
		t.Fatalf("attempts = %d, want 2", len(agg.Attempts))
	}
	if agg.Attempts[0].Provider != "a" || agg.Attempts[0].Reason != "timeout" {
		t.Errorf("attempt[0] = %+v", agg.Attempts[0])
	}
	if agg.Attempts[1].Provider != "b" || agg.Attempts[1].Reason != "invalid_json" {
		t.Errorf("attempt[1] = %+v", agg.Attempts[1])
	}
	// Error message includes both provider names.
	if !strings.Contains(err.Error(), "a=timeout") || !strings.Contains(err.Error(), "b=invalid_json") {
		t.Errorf("err.Error = %q", err.Error())
	}
}

// TestChain_NilProvidersDropped verifies that NewChain silently drops nil
// entries — the constructor's "missing key, skip" contract.
func TestChain_NilProvidersDropped(t *testing.T) {
	a := &stubClient{name: "a", out: "ok"}
	c := NewChain(nil, a, nil)
	if c.Len() != 1 {
		t.Errorf("Len = %d, want 1", c.Len())
	}
	if got := c.Names(); len(got) != 1 || got[0] != "a" {
		t.Errorf("Names = %v, want [a]", got)
	}
}

// TestChain_ContextCancelStopsRetries verifies that a cancelled context
// stops the chain mid-walk — we do not keep punching cancelled contexts at
// downstream providers.
func TestChain_ContextCancelStopsRetries(t *testing.T) {
	a := &stubClient{name: "a", err: &Retryable{Provider: "a", Reason: "timeout"}}
	b := &stubClient{name: "b", out: "should-not-reach"}
	c := NewChain(a, b)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before call
	_, err := c.CompleteJSON(ctx, JSONRequest{Task: "test"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// First provider is called once; chain aborts after seeing ctx.Err().
	if a.calls != 1 {
		t.Errorf("a.calls = %d, want 1", a.calls)
	}
	if b.calls != 0 {
		t.Errorf("b.calls = %d, want 0 (cancelled before fallback)", b.calls)
	}
}

// TestRetryableUnwrap verifies the embedded error is reachable via
// errors.Unwrap / errors.As — required for the chain's classification logic.
func TestRetryableUnwrap(t *testing.T) {
	inner := errors.New("inner")
	r := &Retryable{Provider: "x", Reason: "timeout", Err: inner}
	if !errors.Is(r, inner) {
		t.Errorf("errors.Is failed: %v", r)
	}
	if r.Error() != "x: timeout: inner" {
		t.Errorf("Error() = %q", r.Error())
	}
	r2 := &Retryable{Provider: "y", Reason: "empty"}
	if r2.Error() != "y: empty" {
		t.Errorf("Error() (no inner) = %q", r2.Error())
	}
}
