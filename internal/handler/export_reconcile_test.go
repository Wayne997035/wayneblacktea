package handler

import "github.com/Wayne997035/wayneblacktea/internal/gtd"

// WithRepoResolverForTest pins the reconcile repo resolver for tests whose
// subject is branch / pr_url / TOCTOU behaviour rather than repo
// verification ([F0925-31]). Test-only: this file is not part of the build.
func (h *ReconcileHandler) WithRepoResolverForTest(r gtd.RepoResolver) *ReconcileHandler {
	h.resolverOverride = r
	return h
}
