import type { CSSProperties, FocusEvent } from 'react'

/**
 * Shared input style + focus handlers for modal form fields.
 *
 * Per UI-5 spec §2.3 — keep inline-style approach (CSS variables) instead of
 * Tailwind classes; matches existing CreateGoalModal / CreateProjectModal /
 * QuickAddModal patterns and avoids a mid-refactor style migration.
 */
export const inputBaseStyle: CSSProperties = {
  background: 'var(--color-bg-input)',
  border: '1px solid var(--color-border)',
  color: 'var(--color-text-primary)',
  outline: 'none',
}

export const inputDateStyle: CSSProperties = {
  ...inputBaseStyle,
  colorScheme: 'dark',
}

export function onInputFocus(e: FocusEvent<HTMLElement>): void {
  ;(e.currentTarget as HTMLElement).style.borderColor = 'var(--color-border-focus)'
}

export function onInputBlur(e: FocusEvent<HTMLElement>): void {
  ;(e.currentTarget as HTMLElement).style.borderColor = 'var(--color-border)'
}
