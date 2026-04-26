import { useState, useRef } from 'react'
import { GitBranch, ChevronRight } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { Repo } from '../../types/api'

interface RepoCardProps {
  repo: Repo;
}

const languageColors: Record<string, { bg: string; color: string }> = {
  Go:         { bg: '#00ADD8', color: '#fff' },
  TypeScript: { bg: '#3178C6', color: '#fff' },
  Java:       { bg: '#B07219', color: '#fff' },
}

function LanguageBadge({ language }: { language: string | null | undefined }) {
  if (!language) return null
  const style = languageColors[language] ?? { bg: 'var(--color-bg-hover)', color: 'var(--color-text-muted)' }
  return (
    <span
      className="text-label px-2 py-0.5 rounded-full"
      style={{ background: style.bg, color: style.color }}
    >
      {language}
    </span>
  )
}

export function RepoCard({ repo }: RepoCardProps) {
  const { t } = useTranslation()
  const [isOpen, setIsOpen] = useState(false)
  const issuesRef = useRef<HTMLUListElement>(null)
  const hasIssues = repo.known_issues.length > 0

  const issuesId = `issues-${repo.id}`

  return (
    <article
      className="rounded-lg p-4 transition-colors"
      style={{
        background: 'var(--color-bg-card)',
        border: '1px solid var(--color-border)',
        minHeight: '112px',
      }}
      onMouseEnter={(e) => { e.currentTarget.style.background = 'var(--color-bg-hover)' }}
      onMouseLeave={(e) => { e.currentTarget.style.background = 'var(--color-bg-card)' }}
    >
      <div className="flex items-start gap-2 mb-1">
        <LanguageBadge language={repo.language} />
        <span className="font-mono text-card-title flex-1 truncate" style={{ color: 'var(--color-text-primary)' }}>
          {repo.name}
        </span>
        {/* Status dot */}
        <span
          className="w-2 h-2 rounded-full shrink-0 mt-1.5"
          style={{ background: repo.status === 'active' ? 'var(--color-success)' : 'var(--color-text-muted)' }}
          aria-label={`Status: ${repo.status}`}
        />
      </div>

      {repo.description && (
        <p className="text-body-sm truncate mb-1" style={{ color: 'var(--color-text-muted)' }}>
          {repo.description}
        </p>
      )}

      {repo.current_branch && (
        <div className="flex items-center gap-1 mb-1">
          <GitBranch size={12} aria-hidden="true" style={{ color: 'var(--color-text-muted)' }} />
          <span className="font-mono text-caption" style={{ color: 'var(--color-text-muted)' }}>
            {repo.current_branch}
          </span>
        </div>
      )}

      {repo.next_planned_step && (
        <p className="text-body-sm truncate mb-2" style={{ color: 'var(--color-text-muted)' }}>
          {repo.next_planned_step}
        </p>
      )}

      {hasIssues && (
        <>
          <button
            type="button"
            onClick={() => setIsOpen((v) => !v)}
            aria-expanded={isOpen}
            aria-controls={issuesId}
            className="flex items-center gap-1 text-caption transition-colors"
            style={{ color: 'var(--color-warning)', background: 'transparent', border: 'none', cursor: 'pointer', padding: 0 }}
          >
            <ChevronRight
              size={12}
              aria-hidden="true"
              style={{ transition: 'transform 200ms', transform: isOpen ? 'rotate(90deg)' : 'rotate(0deg)' }}
            />
            {t('workspace.issues', { count: repo.known_issues.length })}
          </button>

          <ul
            id={issuesId}
            ref={issuesRef}
            aria-label={t('workspace.knownIssues')}
            style={{
              overflow: 'hidden',
              maxHeight: isOpen ? `${(issuesRef.current?.scrollHeight ?? 200)}px` : '0',
              transition: 'max-height 250ms ease',
              marginTop: isOpen ? '8px' : '0',
            }}
          >
            {repo.known_issues.map((issue, idx) => (
              <li key={idx} className="text-body-sm flex gap-2 py-0.5" style={{ color: 'var(--color-warning)' }}>
                <span aria-hidden="true">•</span>
                <span>{issue}</span>
              </li>
            ))}
          </ul>
        </>
      )}
    </article>
  )
}
