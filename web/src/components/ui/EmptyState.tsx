import { Inbox, type LucideIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'

interface EmptyStateProps {
  icon?: LucideIcon;
  messageKey: string;
  ctaLabelKey?: string;
  onCta?: () => void;
}

export function EmptyState({ icon: Icon = Inbox, messageKey, ctaLabelKey, onCta }: EmptyStateProps) {
  const { t } = useTranslation()
  return (
    <div className="flex items-center justify-center flex-col min-h-[120px]">
      <Icon size={40} style={{ color: 'var(--color-text-muted)' }} aria-hidden="true" />
      <p className="text-body mt-3 text-center" style={{ color: 'var(--color-text-muted)' }}>
        {t(messageKey)}
      </p>
      {ctaLabelKey && onCta && (
        <button
          type="button"
          onClick={onCta}
          className="mt-4 rounded-md px-4 py-2 text-body-sm transition-colors"
          style={{
            border: '1px solid var(--color-accent-blue)',
            color: 'var(--color-accent-blue)',
            background: 'transparent',
          }}
          onMouseEnter={(e) => {
            const el = e.currentTarget
            el.style.background = 'var(--color-accent-blue)'
            el.style.color = 'var(--color-bg-base)'
          }}
          onMouseLeave={(e) => {
            const el = e.currentTarget
            el.style.background = 'transparent'
            el.style.color = 'var(--color-accent-blue)'
          }}
        >
          {t(ctaLabelKey)}
        </button>
      )}
    </div>
  )
}
