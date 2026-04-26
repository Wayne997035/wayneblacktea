import { Lock } from 'lucide-react'
import { useTranslation } from 'react-i18next'

export function ComingSoon() {
  const { t } = useTranslation()

  return (
    <div
      className="flex flex-col items-center justify-center min-h-[60vh] gap-4"
    >
      <Lock size={48} aria-hidden="true" style={{ color: 'var(--color-text-muted)' }} />
      <h1 className="text-page-title" style={{ color: 'var(--color-text-primary)' }}>
        {t('comingSoon.title')}
      </h1>
      <p className="text-body text-center max-w-sm" style={{ color: 'var(--color-text-muted)' }}>
        {t('comingSoon.message')}
      </p>
    </div>
  )
}
