import { useState } from 'react'
import { ExternalLink } from 'lucide-react'
import type { KnowledgeItem } from '../../types/api'
import { useCreateConceptFromKnowledge } from '../../hooks/useReviews'

interface KnowledgeCardProps {
  item: KnowledgeItem;
}

const TYPE_LABELS: Record<KnowledgeItem['type'], string> = {
  article: 'Article',
  til: 'TIL',
  bookmark: 'Bookmark',
  zettelkasten: 'Note',
}

function formatDate(dateStr: string): string {
  return new Date(dateStr).toLocaleDateString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  })
}

function StarRating({ value }: { value: number }) {
  return (
    <span aria-label={`Learning value: ${value} out of 5`} className="text-caption">
      {Array.from({ length: 5 }, (_, i) => (
        <span
          key={i}
          aria-hidden="true"
          style={{ color: i < value ? 'var(--color-warning)' : 'var(--color-text-disabled)' }}
        >
          ★
        </span>
      ))}
    </span>
  )
}

export function KnowledgeCard({ item }: KnowledgeCardProps) {
  const addToLearning = useCreateConceptFromKnowledge()
  const [added, setAdded] = useState(false)

  function handleAddToLearning() {
    addToLearning.mutate(
      { knowledge_id: item.id },
      {
        onSuccess: () => {
          setAdded(true)
          setTimeout(() => setAdded(false), 1000)
        },
      },
    )
  }

  return (
    <article
      aria-label={item.title}
      className="rounded-lg p-4"
      style={{
        background: 'var(--color-bg-card)',
        border: '1px solid var(--color-border)',
      }}
    >
      {/* Header row */}
      <div className="flex items-start justify-between gap-3 mb-2">
        <div className="flex items-center gap-2 flex-wrap min-w-0">
          <span
            className="text-label rounded px-2 py-0.5 shrink-0"
            style={{
              background: 'var(--color-bg-hover)',
              color: 'var(--color-accent-blue)',
              border: '1px solid var(--color-border)',
            }}
          >
            {TYPE_LABELS[item.type]}
          </span>
          <h3
            className="text-card-title truncate"
            style={{ color: 'var(--color-text-primary)' }}
          >
            {item.title}
          </h3>
        </div>
        <span className="text-caption shrink-0" style={{ color: 'var(--color-text-muted)' }}>
          {formatDate(item.created_at)}
        </span>
      </div>

      {/* Content */}
      <p
        className="text-body-sm mb-3"
        style={{
          color: 'var(--color-text-muted)',
          display: '-webkit-box',
          WebkitLineClamp: 3,
          WebkitBoxOrient: 'vertical',
          overflow: 'hidden',
        }}
      >
        {item.content}
      </p>

      {/* Footer row */}
      <div className="flex items-center justify-between gap-2 flex-wrap">
        <div className="flex items-center gap-2 flex-wrap">
          {/* Source badge */}
          <span
            className="text-label rounded-full px-2 py-0.5"
            style={{
              background: item.source === 'discord' ? 'rgba(79, 195, 247, 0.1)' : 'var(--color-bg-hover)',
              color: item.source === 'discord' ? 'var(--color-accent-blue)' : 'var(--color-text-muted)',
              border: `1px solid ${item.source === 'discord' ? 'var(--color-accent-blue)' : 'var(--color-border)'}`,
            }}
          >
            {item.source}
          </span>

          {/* Tags */}
          {item.tags.map((tag) => (
            <span
              key={tag}
              className="text-label rounded-full px-2 py-0.5"
              style={{
                background: 'var(--color-bg-hover)',
                color: 'var(--color-text-muted)',
                border: '1px solid var(--color-border)',
              }}
            >
              #{tag}
            </span>
          ))}
        </div>

        <div className="flex items-center gap-3">
          {/* Learning value stars */}
          {item.learning_value !== null && <StarRating value={item.learning_value} />}

          {/* Add to learning button */}
          <button
            type="button"
            onClick={handleAddToLearning}
            disabled={addToLearning.isPending || added}
            aria-label={`加入學習：${item.title}`}
            className="text-label rounded px-2 py-0.5 transition-opacity"
            style={{
              minHeight: '28px',
              background: added ? 'rgba(34,197,94,0.1)' : 'var(--color-bg-hover)',
              color: added ? '#22c55e' : 'var(--color-accent-blue)',
              border: `1px solid ${added ? '#22c55e' : 'var(--color-accent-blue)'}`,
              cursor: addToLearning.isPending || added ? 'not-allowed' : 'pointer',
              opacity: addToLearning.isPending ? 0.5 : 1,
              whiteSpace: 'nowrap',
            }}
          >
            {added ? '已加入' : addToLearning.isPending ? '加入中…' : '加入學習'}
          </button>

          {/* URL link */}
          {item.url !== null && (
            <a
              href={item.url}
              target="_blank"
              rel="noopener noreferrer"
              aria-label={`Open link for ${item.title}`}
              className="transition-colors"
              style={{ color: 'var(--color-accent-blue)' }}
            >
              <ExternalLink size={14} aria-hidden="true" />
            </a>
          )}
        </div>
      </div>
    </article>
  )
}
