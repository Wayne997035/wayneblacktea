package handler

// Exported wrappers for internal functions — only compiled in test builds.

// BuildAuthTokenForTest exposes buildAuthToken for unit testing.
func BuildAuthTokenForTest(apiKey, ts string) string {
	return buildAuthToken(apiKey, ts)
}

// ValidateBranchNameForTest exposes validateBranchName for unit testing.
// Empty return string == valid; non-empty == error message.
func ValidateBranchNameForTest(s string) string {
	return validateBranchName(s)
}

// StrictVaguenessForTest exposes the package-level strictVagueness helper for
// unit testing. The MCP variant has its own copy on *Server.
func StrictVaguenessForTest() bool {
	return strictVagueness()
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

// NewAutologHandlerWithClassifierAndProposalForTest is the test-only ctor
// variant that injects all four collaborators including the proposalStore —
// required to exercise the TASK-2 proposal-queue path.
func NewAutologHandlerWithClassifierAndProposalForTest(
	g autologGTDStore,
	s autologSessionStore,
	d autologDecisionStore,
	sum transcriptSummarizer,
	clf activityClassifier,
	proposalStore autologProposalStore,
) *AutologHandler {
	return &AutologHandler{
		gtd: g, sess: s, decision: d, summarizer: sum,
		classifier: clf, proposalStore: proposalStore,
	}
}
