import { GitBranch } from 'lucide-react'
import { PriorityDot } from '../ui/PriorityDot'
import { StatusBadge } from '../ui/StatusBadge'
import type { Project } from '../../types/api'

interface ProjectCardProps {
  project: Project & {
    taskCount?: number;
    nextPlannedStep?: string | null;
  };
  variant?: 'compact' | 'expanded';
  onClick?: () => void;
}

function formatRelativeDate(dateStr: string): string {
  const date = new Date(dateStr)
  const now = new Date()
  const diffMs = now.getTime() - date.getTime()
  const diffDays = Math.floor(diffMs / (1000 * 60 * 60 * 24))
  if (diffDays === 0) return 'today'
  if (diffDays === 1) return 'yesterday'
  if (diffDays < 30) return `${diffDays}d ago`
  return date.toLocaleDateString()
}

export function ProjectCard({ project, variant = 'compact', onClick }: ProjectCardProps) {
  const isInteractive = Boolean(onClick)

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (onClick && (e.key === 'Enter' || e.key === ' ')) {
      e.preventDefault()
      onClick()
    }
  }

  const cardStyle: React.CSSProperties = {
    background: 'var(--color-bg-card)',
    border: '1px solid var(--color-border)',
    borderRadius: 'var(--radius-lg)',
    padding: '16px',
    width: '100%',
    minHeight: '88px',
    cursor: isInteractive ? 'pointer' : 'default',
    transition: 'background var(--duration-fast) ease, border-color var(--duration-fast) ease',
  }

  const articleProps = isInteractive
    ? {
        tabIndex: 0,
        role: 'button' as const,
        'aria-label': project.title,
        onClick,
        onKeyDown: handleKeyDown,
        onMouseEnter: (e: React.MouseEvent<HTMLElement>) => {
          e.currentTarget.style.background = 'var(--color-bg-hover)'
        },
        onMouseLeave: (e: React.MouseEvent<HTMLElement>) => {
          e.currentTarget.style.background = 'var(--color-bg-card)'
        },
      }
    : {}

  return (
    <article style={cardStyle} {...articleProps}>
      <div className="flex items-start justify-between gap-2 mb-1">
        <div className="flex items-center gap-2 min-w-0">
          <PriorityDot level={project.priority} />
          <span className="text-card-title truncate" style={{ color: 'var(--color-text-primary)' }}>
            {project.title}
          </span>
        </div>
        <StatusBadge status={project.status} size="sm" />
      </div>

      <div className="text-caption mb-1" style={{ color: 'var(--color-text-muted)' }}>
        {project.area}
      </div>

      <div className="flex items-center gap-1 mb-1">
        <GitBranch size={12} aria-hidden="true" style={{ color: 'var(--color-accent-blue)' }} />
        <span className="font-mono text-caption" style={{ color: 'var(--color-accent-blue)' }}>
          {project.name}
        </span>
      </div>

      {project.nextPlannedStep && (
        <div
          className="text-body-sm truncate"
          style={{ color: 'var(--color-text-muted)' }}
        >
          {project.nextPlannedStep}
        </div>
      )}

      {variant === 'expanded' && (
        <>
          {project.description && (
            <p
              className="text-body-sm mt-2"
              style={{
                color: 'var(--color-text-muted)',
                display: '-webkit-box',
                WebkitLineClamp: 2,
                WebkitBoxOrient: 'vertical',
                overflow: 'hidden',
              }}
            >
              {project.description}
            </p>
          )}
          <div className="flex items-center gap-3 mt-2">
            {project.taskCount !== undefined && (
              <span
                className="text-caption px-2 py-0.5 rounded-full"
                style={{
                  background: 'var(--color-bg-hover)',
                  color: 'var(--color-text-muted)',
                }}
              >
                {project.taskCount} tasks
              </span>
            )}
            <span className="text-caption" style={{ color: 'var(--color-text-muted)' }}>
              Updated {formatRelativeDate(project.updated_at)}
            </span>
          </div>
        </>
      )}
    </article>
  )
}
