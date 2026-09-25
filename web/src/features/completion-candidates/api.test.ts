/**
 * White-list probe for `toFrontend` (F0925-32).
 *
 * The four acceptance checks in the dispatch record for this ticket never
 * called `toFrontend` / `REASON_ALLOWLIST` directly — a missing allowlist
 * entry for a new backend reason would still pass all of them (the row is
 * silently filtered out of the merged list, not rendered as "wrong"). This
 * file exercises the allowlist directly so a missing entry fails here.
 */
import { describe, it, expect } from 'vitest'
import { toFrontend } from './api'

/**
 * Mirrors the backend candidate payload shape (see `CandidateJSON` in
 * `api.ts`, matching `internal/handler/dashboard_automation_handler.go::candidateJSON`).
 * Local to this test file since `CandidateJSON` itself is not exported.
 */
interface CandidateJSONRow {
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

function baseRow(overrides: Partial<CandidateJSONRow>): Parameters<typeof toFrontend>[0] {
  return {
    id: 'row-1',
    task_id: 'task-1',
    reason: 'pr_merged_repo_unverified',
    evidence_refs: [],
    confidence: 'medium',
    status: 'pending',
    detected_at: '2026-05-20T00:00:00Z',
    ...overrides,
  }
}

describe('toFrontend reason allowlist (F0925-32)', () => {
  it('accepts pr_merged_repo_unverified and preserves the reason field', () => {
    const row = baseRow({})
    const result = toFrontend(row)
    expect(result).not.toBeNull()
    expect(result?.reason).toBe('pr_merged_repo_unverified')
  })

  it('rejects an unknown reason not in REASON_ALLOWLIST', () => {
    const row = baseRow({ reason: 'not_a_reason' })
    const result = toFrontend(row)
    expect(result).toBeNull()
  })
})
