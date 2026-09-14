package httpguard

import (
	"net/http"
	"testing"
	"time"
)

// [GTD a3fcbeb3] SetClientBudget exists because assigning Client.Timeout
// looks like it sets the request budget and sets half of it — the Transport's
// ResponseHeaderTimeout is the smaller, and therefore the real, ceiling.

// TestSetClientBudget_SetsBothCeilings is the whole point: one call, both
// numbers. If only Timeout moved, the caller would still be capped at the
// NewSafeHTTPClient default and would have no way to tell.
func TestSetClientBudget_SetsBothCeilings(t *testing.T) {
	c := NewSafeHTTPClient()
	// tr is the same *http.Transport SetClientBudget will mutate, so holding
	// it across the call both proves the default was non-zero beforehand and
	// avoids a second type assertion.
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("NewSafeHTTPClient Transport is %T, not *http.Transport", c.Transport)
	}
	if tr.ResponseHeaderTimeout <= 0 {
		t.Fatal("fixture is wrong: the default has no ResponseHeaderTimeout, so this test " +
			"would pass even if SetClientBudget did nothing to it")
	}

	SetClientBudget(c, 30*time.Second)

	if c.Timeout != 30*time.Second {
		t.Errorf("Client.Timeout = %v, want 30s", c.Timeout)
	}
	if tr.ResponseHeaderTimeout != 30*time.Second {
		t.Errorf("ResponseHeaderTimeout = %v, want 30s — leaving it at the default is the "+
			"defect this function exists to prevent", tr.ResponseHeaderTimeout)
	}
}

// TestSetClientBudget_LeavesTheDefaultAloneWhenNotCalled is the reverse
// frame. The conservative default is correct for a fetcher pulling arbitrary
// user-supplied URLs, where a slow header IS the attack. SetClientBudget must
// be opt-in, not a change to what NewSafeHTTPClient hands back.
func TestSetClientBudget_LeavesTheDefaultAloneWhenNotCalled(t *testing.T) {
	c := NewSafeHTTPClient()
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport is %T", c.Transport)
	}
	if tr.ResponseHeaderTimeout != 5*time.Second {
		t.Errorf("default ResponseHeaderTimeout = %v, want 5s — callers that deliberately do "+
			"NOT set a budget rely on this staying tight", tr.ResponseHeaderTimeout)
	}
}

// TestSetClientBudget_NoOpOnBadInput pins that a constructor cannot be turned
// into an outage by a zero-value config. These are called during wiring, where
// a panic takes the whole process down.
func TestSetClientBudget_NoOpOnBadInput(t *testing.T) {
	SetClientBudget(nil, time.Second) // must not panic

	c := NewSafeHTTPClient()
	original := c.Timeout
	SetClientBudget(c, 0)
	if c.Timeout != original {
		t.Errorf("a zero budget changed Timeout to %v; want it left at %v", c.Timeout, original)
	}
	SetClientBudget(c, -time.Second)
	if c.Timeout != original {
		t.Errorf("a negative budget changed Timeout to %v; want it left at %v", c.Timeout, original)
	}
}

// TestSetClientBudget_ToleratesAForeignTransport covers the branch where the
// client was not built by NewSafeHTTPClient. It must set what it can rather
// than panic on the type assertion.
func TestSetClientBudget_ToleratesAForeignTransport(t *testing.T) {
	c := &http.Client{Transport: http.DefaultTransport}
	SetClientBudget(c, 7*time.Second)
	if c.Timeout != 7*time.Second {
		t.Errorf("Client.Timeout = %v, want 7s even when the Transport is not ours", c.Timeout)
	}
}
