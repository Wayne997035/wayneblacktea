// Domain types for the completion-candidate feature.
//
// These are kept in lockstep with the backend domain entity at
// internal/completioncandidate/candidate.go and the API response shape
// emitted by internal/handler/dashboard_automation_handler.go::candidateJSON.
//
// Backend canonical field name for the row timestamp is `detected_at`. The
// frontend canonical name is `created_at` — the API hook maps detected_at
// onto created_at when shaping the response so that:
//   - the component contract stays stable (matches the design snapshot)
//   - other UI surfaces that consume this type don't have to know about the
//     backend naming choice

/** Detection rule that produced the candidate. Mirrors candidate.go:34-48. */
export type CandidateReason =
  | 'stale_in_progress'
  | 'finish_work_gap'
  | 'artifact_evidence'
  | 'completion_signal'
  | 'pr_merged_fuzzy'

/** Confidence band. Mirrors candidate.go:13-19. */
export type CandidateConfidence = 'high' | 'medium' | 'low'

/**
 * Lifecycle status. Mirrors candidate.go:22-29.
 *
 * `auto_applied` is a status, NOT a separate boolean — the backend records
 * candidates auto-applied by the reconcile flow with status='auto_applied'
 * (see internal/completioncandidate/store_postgres.go::WriteAutoApplied).
 */
export type CandidateStatus = 'pending' | 'accepted' | 'rejected' | 'auto_applied'

/**
 * Frontend-canonical completion candidate. The API hook is responsible for
 * mapping the backend `detected_at` field to `created_at` here.
 */
export interface CompletionCandidate {
  id: string
  task_id: string
  reason: CandidateReason
  confidence: CandidateConfidence
  status: CandidateStatus
  /** ISO-8601 timestamp (RFC 3339 from backend). */
  created_at: string
  suggested_artifact?: string
  repo_name?: string
  evidence_refs?: string[]
}
