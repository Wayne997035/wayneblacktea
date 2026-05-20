/**
 * Snapshot + behavioural coverage for the production <CandidateRow />
 * component, aligned to the backend domain entity at
 * internal/completioncandidate/candidate.go:34-48.
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
 *   - confidence badge — LOCALIZED (en: "Medium", zh-TW: "中") rather than
 *     the raw enum value (round-1 review flagged that raw-enum rendering
 *     leaked English-by-default into zh-TW users' UI)
 *   - status text — LOCALIZED for the same reason
 *   - clickable suggested-artifact link with URL-scheme allowlist guard
 *   - manual accept / reject controls only when status !== 'auto_applied'
 *   - accept/reject buttons MUST be disabled when no handler is passed
 *     (round-1 reviewer "Functional False Success" — buttons looked
 *     clickable but had no backend endpoint)
 */
import { describe, it, expect, afterEach } from 'vitest'
import { render, act } from '@testing-library/react'
import i18next from 'i18next'
import { CandidateRow } from './CandidateRow'
import type { CompletionCandidate } from './types'

describe('CandidateRow snapshot', () => {
  afterEach(async () => {
    // Ensure test isolation — any test that switches to zh-TW must reset
    // back to the default `en` locale used by test-setup.ts.
    if (i18next.language !== 'en') {
      await act(async () => {
        await i18next.changeLanguage('en')
      })
    }
  })

  it('renders pr_merged_fuzzy reason with medium confidence and disabled accept/reject when no handlers passed', () => {
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
    // Confidence and status are now LOCALIZED strings (round-2 fix).
    expect(
      container.querySelector('[data-testid="confidence-badge"]')?.textContent,
    ).toBe('Medium')
    expect(
      container.querySelector('[data-testid="status"]')?.textContent,
    ).toBe('Pending')
    const link = container.querySelector('a')
    expect(link?.getAttribute('href')).toBe(
      'https://github.com/owner/repo/pull/123',
    )
    // status !== 'auto_applied' → both action buttons render
    const buttons = container.querySelectorAll('button')
    expect(buttons).toHaveLength(2)
    // Round-2 fix: with no onAccept/onReject handlers passed, both buttons
    // MUST be disabled (Functional False Success — preview-only state).
    buttons.forEach((btn) => {
      expect(btn.hasAttribute('disabled')).toBe(true)
      expect(btn.getAttribute('aria-disabled')).toBe('true')
      expect(btn.getAttribute('title')).toBe('Not yet available')
    })
  })

  it('renders enabled accept/reject buttons when handlers ARE passed', () => {
    const candidate: CompletionCandidate = {
      id: 'enabled-001',
      task_id: 'task-enabled',
      reason: 'pr_merged_fuzzy',
      confidence: 'medium',
      status: 'pending',
      suggested_artifact: 'https://github.com/owner/repo/pull/124',
      created_at: '2026-05-20T00:00:00Z',
    }
    const noop = () => {}
    const { container } = render(
      <CandidateRow candidate={candidate} onAccept={noop} onReject={noop} />,
    )
    const buttons = container.querySelectorAll('button')
    expect(buttons).toHaveLength(2)
    buttons.forEach((btn) => {
      expect(btn.hasAttribute('disabled')).toBe(false)
      // aria-disabled should not be set when handler IS provided
      expect(btn.getAttribute('aria-disabled')).toBeNull()
      expect(btn.getAttribute('title')).toBeNull()
    })
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
    // Localized status (round-2 fix).
    expect(
      container.querySelector('[data-testid="status"]')?.textContent,
    ).toBe('Auto-applied')
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

  it('renders zh-TW confidence/status labels when locale is zh-TW', async () => {
    await act(async () => {
      await i18next.changeLanguage('zh-TW')
    })
    const candidate: CompletionCandidate = {
      id: 'zh-001',
      task_id: 'task-zh',
      reason: 'pr_merged_fuzzy',
      confidence: 'medium',
      status: 'pending',
      suggested_artifact: 'https://github.com/owner/repo/pull/200',
      created_at: '2026-05-20T00:00:00Z',
    }
    const { container } = render(<CandidateRow candidate={candidate} />)
    expect(
      container.querySelector('[data-testid="reason-label"]')?.textContent,
    ).toBe('PR 已合併（模糊比對）')
    expect(
      container.querySelector('[data-testid="confidence-badge"]')?.textContent,
    ).toBe('中')
    expect(
      container.querySelector('[data-testid="status"]')?.textContent,
    ).toBe('待確認')
  })
})

describe('CandidateRow safeArtifactHref hardening', () => {
  // Each entry asserts that an adversarial suggested_artifact value collapses
  // to "#" while the raw text is still surfaced to the user.
  const adversarial: Array<{ label: string; value: string }> = [
    { label: 'lowercase javascript:', value: 'javascript:alert(1)' },
    { label: 'mixed-case JaVaScRiPt:', value: 'JaVaScRiPt:alert(1)' },
    { label: 'uppercase DATA:', value: 'DATA:text/html,<script>alert(1)</script>' },
    { label: 'vbscript:', value: 'vbscript:msgbox("xss")' },
    { label: 'leading TAB before https', value: '\thttps://github.com/owner/repo/pull/1' },
    { label: 'leading CR before https', value: '\rhttps://github.com/owner/repo/pull/1' },
    { label: 'null byte mid-string', value: 'https://github.com/foo\x00javascript:alert(1)' },
    { label: 'CR injection mid-string', value: 'https://github.com/foo\rjavascript:alert(1)' },
    { label: 'LF injection mid-string', value: 'https://github.com/foo\njavascript:alert(1)' },
  ]
  for (const { label, value } of adversarial) {
    it(`neutralises ${label}`, () => {
      const candidate: CompletionCandidate = {
        id: `xss-${label}`,
        task_id: 'task-xss',
        reason: 'pr_merged_fuzzy',
        confidence: 'low',
        status: 'pending',
        suggested_artifact: value,
        created_at: '2026-05-20T00:00:00Z',
      }
      const { container } = render(<CandidateRow candidate={candidate} />)
      expect(container.querySelector('a')?.getAttribute('href')).toBe('#')
      // Raw payload visible so user can see the suspicious value.
      expect(container.querySelector('a')?.textContent).toBe(value)
    })
  }

  it('preserves uppercase HTTPS scheme (case-insensitive allowlist)', () => {
    // Round-2 fix: regex now uses /i flag so HTTPS://, HTTP://, WBT:// all
    // resolve through the allowlist instead of being collapsed to "#".
    const candidate: CompletionCandidate = {
      id: 'case-001',
      task_id: 'task-case',
      reason: 'pr_merged_fuzzy',
      confidence: 'low',
      status: 'pending',
      suggested_artifact: 'HTTPS://github.com/owner/repo/pull/42',
      created_at: '2026-05-20T00:00:00Z',
    }
    const { container } = render(<CandidateRow candidate={candidate} />)
    expect(container.querySelector('a')?.getAttribute('href')).toBe(
      'HTTPS://github.com/owner/repo/pull/42',
    )
  })

  it('preserves wbt:// deep links', () => {
    const candidate: CompletionCandidate = {
      id: 'wbt-001',
      task_id: 'task-wbt',
      reason: 'pr_merged_fuzzy',
      confidence: 'low',
      status: 'pending',
      suggested_artifact: 'wbt://task/abc-uuid',
      created_at: '2026-05-20T00:00:00Z',
    }
    const { container } = render(<CandidateRow candidate={candidate} />)
    expect(container.querySelector('a')?.getAttribute('href')).toBe(
      'wbt://task/abc-uuid',
    )
  })
})
