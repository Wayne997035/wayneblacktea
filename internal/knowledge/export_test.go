package knowledge

import "time"

// ContextPackEmbedMaxPerWindowForTest and ContextPackEmbedWindowForTest
// expose the F0929-74 budget constants so store_test.go (external package
// knowledge_test, reusing store_postgres_test.go's shared PG pool) doesn't
// need to duplicate or hardcode these values. File ends in _test.go so none
// of this ships in the production binary (same export_test.go pattern as
// internal/lifecycle's SetPortProbeTimeoutForTest).
const (
	ContextPackEmbedMaxPerWindowForTest = contextPackEmbedMaxPerWindow
	ContextPackEmbedWindowForTest       = contextPackEmbedWindow
)

// ResetContextPackEmbedBudgetForTest resets the process-global F0929-74
// embedding budget to a known token count and reset time, so tests can start
// each assertion from a deterministic state regardless of execution order
// within the test binary.
func ResetContextPackEmbedBudgetForTest(tokens int, resetAt time.Time) {
	contextPackEmbedBudget.mu.Lock()
	defer contextPackEmbedBudget.mu.Unlock()
	contextPackEmbedBudget.tokens = tokens
	contextPackEmbedBudget.resetAt = resetAt
}

// TryAcquireContextPackEmbedTokenForTest exposes tryAcquireContextPackEmbedToken
// directly so tests can pin its refill-boundary behavior (strict
// now.After(resetAt), matching tryAcquireClassifyToken's documented
// behavior bit-for-bit) without waiting out contextPackEmbedWindow in real
// time.
func TryAcquireContextPackEmbedTokenForTest(now time.Time) bool {
	return tryAcquireContextPackEmbedToken(now)
}

// ContextPackEmbedBudgetStateForTest reads the process-global budget's
// current token count and reset time under its lock, so a failing test can
// report the state it observed instead of only a derived call count.
func ContextPackEmbedBudgetStateForTest() (tokens int, resetAt time.Time) {
	contextPackEmbedBudget.mu.Lock()
	defer contextPackEmbedBudget.mu.Unlock()
	return contextPackEmbedBudget.tokens, contextPackEmbedBudget.resetAt
}
