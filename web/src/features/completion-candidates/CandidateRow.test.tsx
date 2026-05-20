// FIXTURE-ONLY — real <CandidateRow /> component pending GTD follow-up task dbc2fef0-ce3d-43ea-a4e6-7b9b0962eaef
//
// grep this file for the string `FIXTURE-ONLY` when wiring the real component
// in. The snapshots recorded here ARE the behavioural contract — when the real
// component lands, replace the local definitions in this file with imports and
// keep the snapshots passing.
//
/**
 * Snapshot coverage for the completion-candidate row, aligned to the backend
 * domain entity at internal/completioncandidate/candidate.go:34-48.
 *
 * Backend reasons (5 total):
 *   stale_in_progress / finish_work_gap / artifact_evidence /
 *   completion_signal / pr_merged_fuzzy
 *
 * This fixture covers two of the reason variants that production DB does
 * not currently have rows for, so 6 軍 cua-driver UI verification cannot
 * exercise them end-to-end:
 *
 *   - `pr_merged_fuzzy`    (introduced by PR #122 — fuzzy matcher path,
 *                           always status='pending' + confidence='medium')
 *   - `artifact_evidence`  (the auto-applied path written by
 *                           store_postgres.go:193 WriteAutoApplied;
 *                           always status='auto_applied')
 *
 * The `ttl-expired-30d` reason lives in a SEPARATE table (pending_proposals,
 * not completion_candidates) and is intentionally NOT covered here — a
 * follow-up task will introduce a PendingProposalRow fixture for it.
 *
 * The real <CandidateRow /> component does not exist yet in `web/src/`.
 * To avoid touching production code in this sprint (per Sprint #4 E3 scope),
 * the row markup is defined locally below as a test fixture that captures
 * the design contract:
 *   - reason label (each backend reason renders a distinct localized string)
 *   - confidence badge
 *   - clickable suggested-artifact link (with URL-scheme allowlist guard)
 *   - manual-accept / reject controls only when status !== 'auto_applied'
 *
 * When the real `<CandidateRow />` component lands (tracked in a separate
 * follow-up task), it can be wired into this file by replacing the local
 * `CandidateRow` definition with the real import — the snapshots remain the
 * behavioural contract.
 */
import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import i18next from 'i18next'
import { useTranslation } from 'react-i18next'

// --- i18n: register fixture-local translation bundle ---
//
// Long-term these keys will live in web/src/i18n/{en,zh-TW}.json under the
// `candidates` namespace. To keep the round-2 PR scope strictly limited to
// this one file + its snapshot, we register the bundle locally at module
// load time. The real component will reuse the same keys once they are
// promoted to the locale JSON files.
i18next.addResourceBundle(
  'en',
  'translation',
  {
    candidates: {
      reason: {
        stale_in_progress: 'Stale in progress',
        finish_work_gap: 'Finish work gap',
        artifact_evidence: 'Artifact evidence',
        completion_signal: 'Completion signal',
        pr_merged_fuzzy: 'PR merged (fuzzy match)',
      },
    },
  },
  true,
  true,
)

// --- Shape mirrors internal/completioncandidate/candidate.go domain entity ---

// Reason: exactly the 5 string constants from candidate.go:34-48.
// This fixture only exercises the 2 variants prod DB lacks; the union still
// lists all 5 so the type is faithful to the backend.
type CandidateReason =
  | 'stale_in_progress'
  | 'finish_work_gap'
  | 'artifact_evidence'
  | 'completion_signal'
  | 'pr_merged_fuzzy'

type CandidateConfidence = 'high' | 'medium' | 'low'

// Status: exactly the 4 string constants from candidate.go:24-29.
// `auto_applied` is a status, NOT a separate boolean field — the backend
// represents auto-applied candidates via Status='auto_applied'.
type CandidateStatus = 'pending' | 'accepted' | 'rejected' | 'auto_applied'

interface CompletionCandidate {
  id: string
  task_id: string
  reason: CandidateReason
  confidence: CandidateConfidence
  status: CandidateStatus
  suggested_artifact: string
  created_at: string
}

// --- URL-scheme allowlist guard ---
//
// suggested_artifact may be a GitHub PR URL ("https://github.com/...") or a
// wayneblacktea-app deep link ("wbt://tasks/<uuid>"). Without an allowlist,
// a malicious suggested_artifact like `javascript:alert(1)` would survive
// into the rendered href and create an XSS surface. Allowlist enforces
// scheme = https | http | wbt; anything else collapses to "#".
const ARTIFACT_SCHEME_ALLOWLIST = /^(https?|wbt):\/\//
function safeArtifactHref(raw: string): string {
  return ARTIFACT_SCHEME_ALLOWLIST.test(raw) ? raw : '#'
}

// --- Local test fixture: design-contract for the row, not production code ---

interface CandidateRowProps {
  candidate: CompletionCandidate
}

function CandidateRow({ candidate }: CandidateRowProps) {
  const { t } = useTranslation()
  const isAutoApplied = candidate.status === 'auto_applied'
  const safeHref = safeArtifactHref(candidate.suggested_artifact)

  return (
    <article
      data-testid="candidate-row"
      data-candidate-id={candidate.id}
      data-reason={candidate.reason}
      data-confidence={candidate.confidence}
      data-status={candidate.status}
      className="flex flex-col gap-2 rounded-md border border-slate-200 bg-white p-3"
    >
      <header className="flex items-center gap-2">
        <span
          data-testid="reason-label"
          className="text-sm font-medium text-slate-700"
        >
          {t(`candidates.reason.${candidate.reason}`)}
        </span>
        <span
          data-testid="confidence-badge"
          data-level={candidate.confidence}
          className="rounded-full bg-slate-100 px-2 py-0.5 font-mono text-xs uppercase text-slate-600"
        >
          {candidate.confidence}
        </span>
      </header>
      <div data-testid="artifact" className="text-sm">
        <a
          href={safeHref}
          target="_blank"
          rel="noopener noreferrer"
          className="text-blue-600 underline hover:text-blue-700"
        >
          {candidate.suggested_artifact}
        </a>
      </div>
      <div
        data-testid="meta"
        className="flex items-center gap-2 text-xs text-slate-500"
      >
        <time dateTime={candidate.created_at}>{candidate.created_at}</time>
        <span data-testid="status">{candidate.status}</span>
      </div>
      {!isAutoApplied && (
        <div data-testid="actions" className="flex gap-2">
          <button
            type="button"
            aria-label="Accept candidate"
            className="rounded bg-emerald-600 px-2 py-1 text-xs font-medium text-white hover:bg-emerald-700"
          >
            Accept
          </button>
          <button
            type="button"
            aria-label="Reject candidate"
            className="rounded bg-slate-200 px-2 py-1 text-xs font-medium text-slate-700 hover:bg-slate-300"
          >
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
