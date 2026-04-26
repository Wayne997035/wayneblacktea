import { useTranslation } from 'react-i18next'

export function LanguageToggle() {
  const { i18n } = useTranslation()
  const current = i18n.language

  const toggle = () => {
    const next = current === 'zh-TW' ? 'en' : 'zh-TW'
    void i18n.changeLanguage(next)
    localStorage.setItem('wbt-lang', next)
  }

  return (
    <button
      type="button"
      onClick={toggle}
      aria-label="切換語言 / Switch language"
      className="flex items-center gap-1 px-3 py-2 rounded-md text-body-sm transition-colors min-h-[44px]"
      onMouseEnter={(e) => { e.currentTarget.style.background = 'var(--color-bg-hover)' }}
      onMouseLeave={(e) => { e.currentTarget.style.background = 'transparent' }}
    >
      <span style={{
        color: current === 'zh-TW' ? 'var(--color-text-primary)' : 'var(--color-text-muted)',
        fontWeight: current === 'zh-TW' ? 500 : 400,
      }}>
        ZH
      </span>
      <span style={{ color: 'var(--color-text-disabled)' }}>/</span>
      <span style={{
        color: current === 'en' ? 'var(--color-text-primary)' : 'var(--color-text-muted)',
        fontWeight: current === 'en' ? 500 : 400,
      }}>
        EN
      </span>
    </button>
  )
}
