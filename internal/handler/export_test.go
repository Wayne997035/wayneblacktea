package handler

// Exported wrappers for internal functions — only compiled in test builds.

// BuildAuthTokenForTest exposes buildAuthToken for unit testing.
func BuildAuthTokenForTest(apiKey, ts string) string {
	return buildAuthToken(apiKey, ts)
}

// ValidateAuthTokenForTest exposes validateAuthToken for unit testing.
func ValidateAuthTokenForTest(apiKey, token string) bool {
	return validateAuthToken(apiKey, token)
}

// NewAutologHandlerForTest creates an AutologHandler with a test-injectable summarizer stub.
// The stub must satisfy the same transcriptSummarizer interface used internally.
func NewAutologHandlerForTest(g autologGTDStore, s autologSessionStore, d autologDecisionStore, sum transcriptSummarizer) *AutologHandler {
	return &AutologHandler{gtd: g, sess: s, decision: d, summarizer: sum}
}

// NewAutologHandlerWithClassifierForTest creates an AutologHandler with both a summarizer
// and an activity classifier stub — for testing the auto-decision capture path.
func NewAutologHandlerWithClassifierForTest(
	g autologGTDStore,
	s autologSessionStore,
	d autologDecisionStore,
	sum transcriptSummarizer,
	clf activityClassifier,
) *AutologHandler {
	return &AutologHandler{gtd: g, sess: s, decision: d, summarizer: sum, classifier: clf}
}
