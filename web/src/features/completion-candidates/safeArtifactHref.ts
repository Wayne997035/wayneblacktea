// [F0925-22] The URL-scheme allowlist guard now lives in ../../lib/safeHref
// so KnowledgeCard and RepoDetailPage can share the same hardening instead
// of each page re-implementing (or forgetting) the allowlist. Re-exported
// under this name so CandidateRow's import is unchanged.
export { safeHref as safeArtifactHref } from '../../lib/safeHref'
