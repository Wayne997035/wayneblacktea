import type { ReactNode } from 'react'

export interface FormFieldProps {
  /**
   * The id of the input/select/textarea this label is associated with.
   * The child input MUST also use this id and `aria-describedby={id}-err` /
   * `aria-describedby={id}-hint` when applicable.
   */
  id: string;
  label: ReactNode;
  /** Optional caption shown beneath the input, e.g. "kebab-case identifier". */
  hint?: ReactNode;
  /** Per-field error message. Overrides hint when present. */
  error?: string;
  /** Renders a small "*" after the label. Visual only; required validation lives in the form. */
  required?: boolean;
  children: ReactNode;
}

/**
 * Shared label + error/hint wrapper for modal form fields.
 *
 * Per UI-5 spec §2.2 — extracts only label + the focus-border style boilerplate.
 * Does NOT abstract over input vs select vs textarea; the `children` prop receives
 * the raw input. Keeps the API minimal.
 */
export function FormField({ id, label, hint, error, required, children }: FormFieldProps) {
  return (
    <div>
      <label
        htmlFor={id}
        className="text-label block mb-1"
        style={{ color: 'var(--color-text-muted)' }}
      >
        {label}
        {required && (
          <span aria-hidden="true" style={{ color: 'var(--color-error)' }}>
            {' *'}
          </span>
        )}
      </label>
      {children}
      {error ? (
        <p
          id={`${id}-err`}
          role="alert"
          className="text-body-sm mt-1"
          style={{ color: 'var(--color-error)' }}
        >
          {error}
        </p>
      ) : hint ? (
        <p
          id={`${id}-hint`}
          className="text-caption mt-1"
          style={{ color: 'var(--color-text-muted)' }}
        >
          {hint}
        </p>
      ) : null}
    </div>
  )
}
