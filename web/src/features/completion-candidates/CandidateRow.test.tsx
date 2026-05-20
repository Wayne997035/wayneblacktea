/**
 * Snapshot coverage for the completion-candidate row in two reason variants
 * that prod DB does not currently have rows for, so 6 軍 cua-driver UI
 * verification cannot exercise them end-to-end:
 *
 *   - `pr_merged_fuzzy`   (introduced by PR #122 — fuzzy matcher path)
 *   - `ttl-expired-30d`   (introduced by PR #121 — TTL prune path)
 *
 * The component itself does not exist yet in `web/src/`. To avoid touching
 * production code in this sprint (per Sprint #4 E3 scope), the row markup is
 * defined locally below as a test fixture that captures the design contract:
 *   - reason label (fuzzy vs ttl-expired must render distinct text)
 *   - confidence badge
 *   - clickable suggested-artifact link
 *   - manual-accept button when `auto_applied=false`
 *
 * When the real `<CandidateRow />` component lands (tracked in a separate
 * follow-up task), it can be wired into this file by replacing the local
 * `CandidateRow` definition with the real import — the snapshots remain the
 * behavioural contract.
 */
import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'

// --- Shape mirrors internal/gtd/completion_candidate.go domain entity ---

type CandidateReason = 'pr_merged_fuzzy' | 'ttl-expired-30d' | 'pr_merged_exact'
type CandidateConfidence = 'high' | 'medium' | 'low'
type CandidateStatus = 'pending' | 'accepted' | 'rejected'

interface CompletionCandidate {
  id: string
  task_id: string
  reason: CandidateReason
  confidence: CandidateConfidence
  status: CandidateStatus
  suggested_artifact: string
  auto_applied: boolean
  created_at: string
}

// --- Local test fixture: design-contract for the row, not production code ---

function reasonLabel(reason: CandidateReason): string {
  switch (reason) {
    case 'pr_merged_fuzzy':
      return 'PR merged (fuzzy match)'
    case 'ttl-expired-30d':
      return 'TTL expired (30d)'
    case 'pr_merged_exact':
      return 'PR merged (exact match)'
  }
}

function confidenceLabel(confidence: CandidateConfidence): string {
  switch (confidence) {
    case 'high':
      return 'high'
    case 'medium':
      return 'medium'
    case 'low':
      return 'low'
  }
}

interface CandidateRowProps {
  candidate: CompletionCandidate
}

function CandidateRow({ candidate }: CandidateRowProps) {
  return (
    <article
      data-testid="candidate-row"
      data-candidate-id={candidate.id}
      data-reason={candidate.reason}
      data-confidence={candidate.confidence}
    >
      <header>
        <span className="reason-label">{reasonLabel(candidate.reason)}</span>
        <span className="confidence-badge" data-level={candidate.confidence}>
          {confidenceLabel(candidate.confidence)}
        </span>
      </header>
      <div className="artifact">
        <a
          href={candidate.suggested_artifact}
          target="_blank"
          rel="noopener noreferrer"
        >
          {candidate.suggested_artifact}
        </a>
      </div>
      <div className="meta">
        <time dateTime={candidate.created_at}>{candidate.created_at}</time>
        <span className="status">{candidate.status}</span>
      </div>
      {!candidate.auto_applied && (
        <div className="actions">
          <button type="button" aria-label="Accept candidate">
            Accept
          </button>
          <button type="button" aria-label="Reject candidate">
            Reject
          </button>
        </div>
      )}
    </article>
  )
}

// --- Tests ---

describe('CandidateRow snapshot', () => {
  it('renders pr_merged_fuzzy reason with medium confidence and manual-accept controls', () => {
    const candidate: CompletionCandidate = {
      id: 'abc-123',
      task_id: 'task-456',
      reason: 'pr_merged_fuzzy',
      confidence: 'medium',
      status: 'pending',
      suggested_artifact: 'https://github.com/owner/repo/pull/123',
      auto_applied: false,
      created_at: '2026-05-20T00:00:00Z',
    }

    const { container } = render(<CandidateRow candidate={candidate} />)

    expect(container.firstChild).toMatchSnapshot()
    expect(
      container.querySelector('.reason-label')?.textContent,
    ).toBe('PR merged (fuzzy match)')
    expect(
      container.querySelector('.confidence-badge')?.textContent,
    ).toBe('medium')
    const link = container.querySelector('a')
    expect(link?.getAttribute('href')).toBe(
      'https://github.com/owner/repo/pull/123',
    )
    // auto_applied=false → both action buttons render
    expect(container.querySelectorAll('button')).toHaveLength(2)
  })

  it('renders ttl-expired-30d reason with low confidence and distinct label', () => {
    const candidate: CompletionCandidate = {
      id: 'def-789',
      task_id: 'task-456',
      reason: 'ttl-expired-30d',
      confidence: 'low',
      status: 'pending',
      suggested_artifact: 'wbt://tasks/task-456',
      auto_applied: false,
      created_at: '2026-05-20T00:00:00Z',
    }

    const { container } = render(<CandidateRow candidate={candidate} />)

    expect(container.firstChild).toMatchSnapshot()
    // Distinct reason label vs fuzzy variant — guards against accidental
    // collapsing of branch logic.
    expect(
      container.querySelector('.reason-label')?.textContent,
    ).toBe('TTL expired (30d)')
    expect(
      container.querySelector('.confidence-badge')?.textContent,
    ).toBe('low')
  })

  it('hides accept/reject controls when auto_applied=true', () => {
    const candidate: CompletionCandidate = {
      id: 'ghi-321',
      task_id: 'task-789',
      reason: 'pr_merged_exact',
      confidence: 'high',
      status: 'accepted',
      suggested_artifact: 'https://github.com/owner/repo/pull/9',
      auto_applied: true,
      created_at: '2026-05-20T00:00:00Z',
    }

    const { container } = render(<CandidateRow candidate={candidate} />)
    expect(container.querySelectorAll('button')).toHaveLength(0)
  })
})
