import { useTranslation } from 'react-i18next'

interface SystemHealthProps {
  isOnline: boolean;
  isLoading: boolean;
}

export function SystemHealth({ isOnline, isLoading }: SystemHealthProps) {
  const { t } = useTranslation()

  return (
    <div
      className="rounded-lg p-4 flex items-center gap-3"
      style={{
        background: 'var(--color-bg-card)',
        border: '1px solid var(--color-border)',
      }}
    >
      <span
        className="w-2.5 h-2.5 rounded-full shrink-0"
        style={{
          background: isLoading
            ? 'var(--color-text-muted)'
            : isOnline
              ? 'var(--color-success)'
              : 'var(--color-error)',
          boxShadow: !isLoading && isOnline ? '0 0 6px var(--color-success)' : undefined,
        }}
        aria-hidden="true"
      />
      <span className="text-body-sm" style={{ color: 'var(--color-text-muted)' }}>
        {isLoading
          ? t('common.loading')
          : isOnline
            ? t('api.connected')
            : t('api.disconnected')}
      </span>
    </div>
  )
}
