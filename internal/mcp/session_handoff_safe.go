package mcp

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Wayne997035/wayneblacktea/internal/db"
)

// safeSessionHandoff is the ONLY representation of a *db.SessionHandoff that
// may cross a JSON serialization boundary anywhere in this package.
//
// *db.SessionHandoff is a plain struct: nothing in the type system stops a
// caller from writing `return []any{h}` and shipping the raw row — including
// Embedding (a vector blob) and unfenced Intent/ContextSummary/RepoName text
// with no defence against forged boundary markers. That was exactly the
// recall bug PR #158 round-2 security review found (tools_procedural.go,
// recallEpisodic): the type system offers zero resistance at that layer.
//
// Wrapping the row in safeSessionHandoff moves the "did this get hardened"
// decision out of every individual read site and into the type itself: its
// MarshalJSON always renders the SAME clip+neutralise+fence+byte-budget view
// buildHardenedHandoffView already produces for mark_next_action_done and the
// wayneblacktea://session/handoff/latest resource, regardless of where the
// value ends up — a map[string]any, a []any, a struct field, doesn't matter.
// Forgetting to harden is no longer possible once a value is this type; it is
// only possible by reaching for one of the raw/lightly-hardened accessors
// below, which is a visible, grep-able act instead of a silent omission.
//
// internal/mcp/session_handoff_scope_test.go enforces the other half of this
// guarantee: *db.SessionHandoff (and the session store methods that return
// it) may only appear in a short, reviewed whitelist of files — this one plus
// the handful of existing call sites that already construct a
// safeSessionHandoff or an equivalently-hardened view immediately after
// obtaining the raw row.
type safeSessionHandoff struct {
	h *db.SessionHandoff
}

// json.Marshaler is implemented on the value receiver (not pointer), so a
// safeSessionHandoff embedded by value in a map[string]any or []any still
// goes through MarshalJSON — encoding/json only special-cases pointer
// receivers when the value itself is addressable, which a map/slice element
// is not guaranteed to be. Pinning this via a value-receiver method (not a
// pointer one) is what makes the "even stuffed into []any{...}, the JSON is
// still safe" guarantee hold regardless of how the caller stores the value.
var _ json.Marshaler = safeSessionHandoff{}

// newSafeSessionHandoff wraps h. h may be nil — MarshalJSON on a nil-wrapping
// value returns an error rather than silently emitting an empty or
// misleading payload (design requirement: serialization failure MUST be an
// error, never a silent empty/default value substituted in its place). Every
// call site in this package that has a nil handoff (session.ErrNotFound,
// typically) returns its own "nothing here" shape instead of constructing
// this type at all, so reaching MarshalJSON with a nil h would be a
// programming error worth surfacing immediately, not a normal empty state to
// paper over.
func newSafeSessionHandoff(h *db.SessionHandoff) safeSessionHandoff {
	return safeSessionHandoff{h: h}
}

// MarshalJSON renders the hardened view (buildHardenedHandoffView,
// tools_session.go) — the same clip+neutralise+fence+byte-budget treatment
// mark_next_action_done and the read-only handoff resource already apply.
func (v safeSessionHandoff) MarshalJSON() ([]byte, error) {
	if v.h == nil {
		return nil, errors.New("safeSessionHandoff: MarshalJSON called on a nil-wrapped handoff")
	}
	out, err := json.Marshal(buildHardenedHandoffView(v.h))
	if err != nil {
		return nil, fmt.Errorf("safeSessionHandoff: marshaling hardened view: %w", err)
	}
	return out, nil
}

// rawIntent and rawContextSummary are the ONLY way to reach the unhardened
// text inside this type. They exist purely for internal, non-serializing
// comparisons — recallEpisodic's substring match against the search query is
// the sole caller today (tools_procedural.go). Any new call site that reaches
// for these MUST NOT let the returned string cross a JSON serialization
// boundary; if it needs to render the value, it must go back through the
// safeSessionHandoff itself (MarshalJSON) or hardenedIntent below, not these.
func (v safeSessionHandoff) rawIntent() string {
	if v.h == nil {
		return ""
	}
	return v.h.Intent
}

func (v safeSessionHandoff) rawContextSummary() string {
	if v.h == nil {
		return ""
	}
	return textValue(v.h.ContextSummary)
}

// hardenedIntent returns just the fenced+clipped+neutralised Intent text —
// the same treatment buildHardenedHandoffView gives the Intent field of the
// full hardened view, extracted for callers (closeout_session_check,
// analyze_agent_behavior's stale_handoff detection) that need only a single
// summary string, not the entire handoff object. It shares
// clipAndFenceStoredContext and handoffResourceIntentMaxRunes with
// buildHardenedHandoffView (tools_session.go) so every caller applies
// IDENTICAL treatment — this is not a second, independently-tuned sanitiser.
func (v safeSessionHandoff) hardenedIntent() string {
	if v.h == nil {
		return ""
	}
	return clipAndFenceStoredContext(v.h.Intent, handoffResourceIntentMaxRunes)
}
