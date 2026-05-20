/**
 * Snapshot coverage for the production <CandidateRow /> component, aligned to
 * the backend domain entity at internal/completioncandidate/candidate.go:34-48.
 *
 * Backend reasons (5 total):
 *   stale_in_progress / finish_work_gap / artifact_evidence /
 *   completion_signal / pr_merged_fuzzy
 *
 * This file covers two of the reason variants that production DB does not
 * currently have rows for, so the 6th-army integration-qa UI verification
 * cannot exercise them end-to-end:
 *
 *   - `pr_merged_fuzzy`    (introduced by PR #122 — fuzzy matcher path,
 *                           always status='pending' + confidence='medium')
 *   - `artifact_evidence`  (the auto-applied path written by
 *                           store_postgres.go:193 WriteAutoApplied;
 *                           always status='auto_applied')
 *
 * The `ttl-expired-30d` reason lives in a SEPARATE table (pending_proposals,
 * not completion_candidates) and is intentionally NOT covered here — a
 * follow-up task (dbc2fef0-ce3d-43ea-a4e6-7b9b0962eaef) will introduce a
 * PendingProposalRow fixture for it.
 *
 * The behavioural contract enforced by these tests:
 *   - reason label (each backend reason renders a distinct localized string)
 *   - confidence badge (raw value, lowercase)
 *   - clickable suggested-artifact link with URL-scheme allowlist guard
 *   - manual accept / reject controls only when status !== 'auto_applied'
 */
import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import { CandidateRow } from './CandidateRow'
import type { CompletionCandidate } from './types'

describe('CandidateRow snapshot', () => {
  it('renders pr_merged_fuzzy reason with medium confidence and manual-accept controls', () => {
    const candidate: CompletionCandidate = {
      id: 'abc-123',
      task_id: 'task-456',
      reason: 'pr_merged_fuzzy',
      confidence: 'medium',
      status: 'pending',
      suggested_artifact: 'https://github.com/owner/repo/pull/123',
      created_at: '2026-05-20T00:00:00Z',
    }

    const { container } = render(<CandidateRow candidate={candidate} />)

    expect(container.firstChild).toMatchSnapshot()
    expect(
      container.querySelector('[data-testid="reason-label"]')?.textContent,
    ).toBe('PR merged (fuzzy match)')
    expect(
      container.querySelector('[data-testid="confidence-badge"]')?.textContent,
    ).toBe('medium')
    const link = container.querySelector('a')
    expect(link?.getAttribute('href')).toBe(
      'https://github.com/owner/repo/pull/123',
    )
    // status !== 'auto_applied' → both action buttons render
    expect(container.querySelectorAll('button')).toHaveLength(2)
  })

  it('renders artifact_evidence auto-applied row without manual controls', () => {
    const candidate: CompletionCandidate = {
      id: 'ghi-321',
      task_id: 'task-789',
      reason: 'artifact_evidence',
      confidence: 'high',
      status: 'auto_applied',
      suggested_artifact: 'https://github.com/owner/repo/pull/9',
      created_at: '2026-05-20T00:00:00Z',
    }

    const { container } = render(<CandidateRow candidate={candidate} />)

    expect(container.firstChild).toMatchSnapshot()
    expect(
      container.querySelector('[data-testid="reason-label"]')?.textContent,
    ).toBe('Artifact evidence')
    // status='auto_applied' → no accept/reject controls
    expect(container.querySelectorAll('button')).toHaveLength(0)
    expect(container.querySelector('[data-testid="actions"]')).toBeNull()
    // data-status attribute exposes the auto-applied state for downstream
    // CSS / e2e selectors.
    expect(
      container
        .querySelector('[data-testid="candidate-row"]')
        ?.getAttribute('data-status'),
    ).toBe('auto_applied')
  })

  it('collapses an unsafe suggested_artifact URL to "#" via scheme allowlist', () => {
    // Adversarial input: a malicious suggested_artifact must NOT survive
    // into the rendered href. The allowlist permits only https/http/wbt.
    const candidate: CompletionCandidate = {
      id: 'xss-001',
      task_id: 'task-xss',
      reason: 'pr_merged_fuzzy',
      confidence: 'low',
      status: 'pending',
      // adversarial fixture — the URL allowlist must collapse this to "#"
      suggested_artifact: 'javascript:alert(1)',
      created_at: '2026-05-20T00:00:00Z',
    }

    const { container } = render(<CandidateRow candidate={candidate} />)
    expect(container.querySelector('a')?.getAttribute('href')).toBe('#')
    // The raw payload is still shown as link text so the user can SEE the
    // suspicious value — but the href is neutralized.
    expect(container.querySelector('a')?.textContent).toBe(
      'javascript:alert(1)',
    )
  })
})
