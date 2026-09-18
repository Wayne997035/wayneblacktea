package mcp

import (
	"context"
	"log/slog"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// This file holds the two-step delete confirmation shared by delete_task and
// delete_project. It exists as one implementation on purpose: the flow below
// accumulated four separate security fixes (F170-SEC-R3-03, SEC171-02,
// SEC171-11, SEC171-13), and three of them had to be applied to several
// branches at once. A second copy of this logic would receive the fifth fix
// in one place only, and the copy that missed it would keep passing its own
// tests — the failure this file's single-implementation shape forecloses.

// Deletion keys namespace the deleteTokens map by entity kind, so a token
// issued to remove a project can never be spent removing a task, even in the
// impossible case of two entities sharing a UUID. Task keys are bare ids
// because that is the shape already stored by the tokens in flight and by
// the tests that write the map directly.
func taskDeletionKey(id string) string    { return id }
func projectDeletionKey(id string) string { return "project:" + id }

// issuePendingDeletion is step 1 for both delete tools: prune expired
// tokens, refuse if too many are already in flight, then store a fresh token
// under key. A non-nil result is the caller's refusal to return as-is.
//
// The token is always a bare random UUID — U9's session binding lives in the
// separate deletionToken.issuedBySession field, never encoded into the token
// string, so that a token appearing in a log or an error message does not
// also disclose which session issued it (see issueDeletionToken).
func (s *Server) issuePendingDeletion(ctx context.Context, key string) (string, time.Time, *mcp.CallToolResult) {
	var count int
	s.deleteTokens.Range(func(k, v any) bool {
		rec := v.(deletionToken)
		if s.now().After(rec.expiresAt) {
			s.deleteTokens.Delete(k)
		} else {
			count++
		}
		return true
	})
	if count >= maxPendingDeletions {
		return "", time.Time{}, mcp.NewToolResultError("too many pending deletions in flight; retry later")
	}

	token := issueDeletionToken()
	expires := s.now().Add(deleteTokenTTL)
	s.deleteTokens.Store(key, deletionToken{
		token:           token,
		expiresAt:       expires,
		issuedBySession: currentSessionID(ctx),
	})
	return token, expires, nil
}

// spendPendingDeletion is step 2 for both delete tools: validate the supplied
// token against the record stored under key and consume it. It returns nil
// when — and only when — the caller may proceed with the actual delete; any
// non-nil result is a refusal to return as-is.
//
// tool and idArg only shape the messages ("delete_task"/"task_id"), so each
// tool tells its caller how to retry in its own terms.
//
// ⚠ The refusal branches below are deliberately NOT symmetric, and the
// asymmetry is the point. A refusal may decline to consume the token only
// when REACHING that refusal already proves the caller holds the secret.
// This map is keyed by ENTITY ID, not by the token, so a caller who knows
// only an id reaches the token comparison without holding anything — those
// branches must consume. The session branch is reached only after the
// correct token was presented, so it can refuse for free.
//
// (An earlier version justified the consuming branches as anti-brute-force.
// That argument does not survive its own arithmetic — roughly 7,200 guesses
// fit in the 60s TTL, against a 122-bit UUID — and a wrong reason for a
// right rule is what the next reader inherits.)
func (s *Server) spendPendingDeletion(ctx context.Context, tool, idArg, key, supplied string) *mcp.CallToolResult {
	if supplied == "" {
		return mcp.NewToolResultError("deletion_token is required when confirm=true")
	}

	// [F170-SEC-R3-03] Load, not LoadAndDelete — same property as
	// tools_reconcile.go's confirm path, and documented as one property, so
	// the two must not drift. Validating after deleting meant a refusal
	// destroyed the pending deletion, and the rightful caller's retry got
	// "no pending deletion" instead of the real reason.
	stored, ok := s.deleteTokens.Load(key)
	if !ok {
		return mcp.NewToolResultError(
			"no pending deletion for this " + idArg + "; call without confirm first to obtain a token",
		)
	}
	rec, ok := stored.(deletionToken)
	if !ok {
		// [SEC171-13] Unusable either way — drop it rather than answering
		// "corrupted" until the TTL expires. CompareAndDelete, not
		// unconditional Delete: this map is keyed by ENTITY ID, so an
		// unconditional Delete here could destroy a fresh, valid record
		// another session obtained for the same id between our Load above
		// and this line — the exact cross-session DoS SEC171-13 named.
		//
		// No panic risk from using CompareAndDelete on this specific branch,
		// where the type assertion into rec already failed: `stored` is the
		// exact `any` Load returned, and sync.Map.CompareAndDelete only
		// requires `old` (here, `stored`) to be of a comparable type — not
		// that a later assertion into some other type succeeds. The sole
		// non-test write path to this map (issuePendingDeletion above) only
		// ever stores a deletionToken{string, time.Time, string}, and all
		// three of those are comparable, so `stored`'s dynamic type is
		// always deletionToken in practice — this branch is defensive
		// against a shape this codebase never actually produces. Contrast
		// tools_reconcile.go's reconcileConfirmation, which holds
		// []gtd.Match/[]gtd.Ambiguous and DOES panic under CompareAndDelete
		// — that comparability difference is why reconcile keeps
		// LoadAndDelete and this path does not.
		//
		// slog.Debug, not silence and not Warn: a false return means another
		// session's fresh record already replaced this one before we could
		// refuse — an expected outcome of the fix this branch exists for,
		// not an anomaly, but still worth an operator-visible trail on a
		// security-relevant path. Never logs the token itself.
		if !s.deleteTokens.CompareAndDelete(key, stored) {
			slog.Debug(tool+": corrupted-record refusal found nothing to clear (already replaced)", idArg, key)
		}
		return mcp.NewToolResultError("internal: deletion token state corrupted")
	}
	if s.now().After(rec.expiresAt) {
		// [SEC171-13][SEC171-17] CompareAndDelete for the same reason as the
		// corrupted-record branch above — see its comment for the full
		// panic-safety argument, which applies identically here.
		//
		// [GTD b1858809] Both numbers, deliberately. SEC171-13 named the
		// mechanism shared by all four exits (this map is keyed by entity
		// id, so an unconditional Delete can destroy another session's fresh
		// record); SEC171-17 named this branch's instance of it, and is the
		// number its regression test carries —
		// TestSEC171_17_ExpiredRefusalDoesNotDestroyReplacementToken. A
		// reviewer flagged the single-number form as a possible typo: it was
		// not, but a reader tracing the guard to its test had no way to tell
		// that from the comment alone.
		if !s.deleteTokens.CompareAndDelete(key, stored) {
			slog.Debug(tool+": expired-token refusal found nothing to clear (already replaced)", idArg, key)
		}
		return mcp.NewToolResultError("deletion_token expired; call without confirm to obtain a new token")
	}
	// Constant-time string compare on equal-length inputs would be ideal, but
	// these tokens are generated server-side UUIDs and never exposed to
	// untrusted parties in the comparison window — plain equality is fine.
	//
	// [SEC171-11] Consuming on mismatch follows the asymmetry rule stated
	// above (⚠, this map is keyed by ENTITY ID): reaching this comparison
	// only requires knowing the id, not holding the token, so this branch
	// must consume — it is not, and was never, an anti-guessing measure; the
	// anti-brute-force framing that phrase pointed at is the one this
	// function's own comment above already retracts by its own arithmetic.
	// [SEC171-13] CompareAndDelete, not unconditional Delete — see the
	// corrupted-record branch above for the full panic-safety argument; it
	// applies identically here.
	if supplied != rec.token {
		if !s.deleteTokens.CompareAndDelete(key, stored) {
			slog.Debug(tool+": token-mismatch refusal found nothing to clear (already replaced)", idArg, key)
		}
		return mcp.NewToolResultError("deletion_token mismatch")
	}
	// U9 partial mitigation (Category S): the confirming call must present
	// the same session id the issuing call carried, when one was tracked.
	// [F170-20]: that is a knowledge check, not an identity check — see
	// issueDeletionToken/deletionTokenMatchesSession's doc comments.
	//
	// [F170-SEC-R3-03] Non-consuming: the token stays live for its remaining
	// TTL so the session it was issued to can still spend it.
	if !deletionTokenMatchesSession(ctx, rec) {
		return mcp.NewToolResultError(
			"deletion_token was issued to a different session; call " + tool + " without confirm " +
				"from that same session to obtain a new token",
		)
	}
	// [SEC171-02] Spend atomically, and spend THIS record specifically.
	//
	// Load-then-Delete was check-then-act: two concurrent confirms both passed
	// validation before either deleted. It had a second failure mode this map
	// has and reconcile's does not — keyed by entity id, an unconditional
	// Delete removes whatever occupies that key now, which may be a token
	// another session obtained after we loaded ours.
	//
	// [SEC171-13] CompareAndDelete closes both failure modes here, and at
	// every exit from this function, not only here: the three refusal
	// branches above (corrupted record, expired, token mismatch) use the
	// identical primitive for the identical reason; see the first of them
	// for the full panic-safety argument. An earlier version of this comment
	// claimed CompareAndDelete "closes both" while those three branches
	// still called the unconditional Delete they were supposed to replace —
	// true only at this one line, not at the three that mattered for the
	// cross-session case. deletionToken's fields are all comparable (string,
	// time.Time, string), which is what makes CompareAndDelete legal at
	// every one of these four sites — reconcile's record is not, and uses
	// LoadAndDelete for that reason plus its key being the token itself.
	if !s.deleteTokens.CompareAndDelete(key, stored) {
		return mcp.NewToolResultError(
			"no pending deletion for this " + idArg + "; call without confirm first to obtain a token",
		)
	}
	return nil
}
