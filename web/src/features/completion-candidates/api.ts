import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../../lib/api'
import type {
  CompletionCandidate,
  CandidateConfidence,
  CandidateReason,
  CandidateStatus,
} from './types'

/**
 * Backend candidate payload shape (matches
 * internal/handler/dashboard_automation_handler.go::candidateJSON).
 *
 * Note: the canonical backend timestamp field is `detected_at`. The frontend
 * canonical name is `created_at`; mapping happens in `toFrontend` below.
 */
interface CandidateJSON {
  id: string
  task_id: string
  repo_name?: string
  reason: string
  evidence_refs: string[]
  confidence: string
  suggested_artifact?: string
  status: string
  detected_at: string
}

/**
 * Subset of /api/dashboard/automation-health used by the candidates feature.
 * Only candidate-bearing fields are typed here; the rest of the payload is
 * already covered by hooks/useAutomationHealth.ts.
 */
interface AutomationHealthCandidatesResponse {
  stale_in_progress: CandidateJSON[]
  completion_candidates: CandidateJSON[]
}

const REASON_ALLOWLIST: ReadonlySet<CandidateReason> = new Set<CandidateReason>([
  'stale_in_progress',
  'finish_work_gap',
  'artifact_evidence',
  'completion_signal',
  'pr_merged_fuzzy',
])

const CONFIDENCE_ALLOWLIST: ReadonlySet<CandidateConfidence> = new Set<CandidateConfidence>([
  'high',
  'medium',
  'low',
])

const STATUS_ALLOWLIST: ReadonlySet<CandidateStatus> = new Set<CandidateStatus>([
  'pending',
  'accepted',
  'rejected',
  'auto_applied',
])

/**
 * Map an API row onto the frontend-canonical CompletionCandidate. Unknown
 * enum values are surfaced as `null` so the caller can skip the row instead
 * of rendering with an invalid type — defence in depth in case the backend
 * adds new enum variants the UI hasn't been updated for yet.
 */
function toFrontend(row: CandidateJSON): CompletionCandidate | null {
  if (!REASON_ALLOWLIST.has(row.reason as CandidateReason)) return null
  if (!CONFIDENCE_ALLOWLIST.has(row.confidence as CandidateConfidence)) return null
  if (!STATUS_ALLOWLIST.has(row.status as CandidateStatus)) return null

  return {
    id: row.id,
    task_id: row.task_id,
    reason: row.reason as CandidateReason,
    confidence: row.confidence as CandidateConfidence,
    status: row.status as CandidateStatus,
    created_at: row.detected_at,
    suggested_artifact: row.suggested_artifact,
    repo_name: row.repo_name,
    evidence_refs: row.evidence_refs ?? [],
  }
}

/**
 * useCompletionCandidates fetches the pending completion-candidate list from
 * the automation-health endpoint and returns it shaped for the UI.
 *
 * Both `stale_in_progress` and `completion_candidates` are merged because
 * they are the same domain row split server-side only for dashboard layout;
 * the candidates page surfaces all of them.
 */
export function useCompletionCandidates() {
  return useQuery<CompletionCandidate[]>({
    queryKey: ['completion-candidates'],
    queryFn: async () => {
      const data = await apiFetch<AutomationHealthCandidatesResponse>(
        '/api/dashboard/automation-health',
      )
      const merged = [
        ...(data.stale_in_progress ?? []),
        ...(data.completion_candidates ?? []),
      ]
      return merged
        .map(toFrontend)
        .filter((c): c is CompletionCandidate => c !== null)
    },
    staleTime: 0,
    refetchInterval: 60_000,
    refetchOnWindowFocus: true,
  })
}
