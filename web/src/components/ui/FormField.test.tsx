import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { FormField } from './FormField'

describe('FormField', () => {
  it('associates label with the child input via htmlFor + id', () => {
    render(
      <FormField id="my-input" label="My label">
        <input id="my-input" type="text" />
      </FormField>,
    )
    const input = screen.getByLabelText('My label')
    expect(input).toBeInTheDocument()
    expect(input.tagName).toBe('INPUT')
  })

  it('renders a visual required marker when required', () => {
    render(
      <FormField id="x" label="Name" required>
        <input id="x" type="text" />
      </FormField>,
    )
    // The "*" is aria-hidden so it won't be in the accessible name; query by text.
    const star = screen.getByText('*', { exact: false })
    expect(star).toBeInTheDocument()
    expect(star.getAttribute('aria-hidden')).toBe('true')
  })

  it('does not render a marker when required is false', () => {
    render(
      <FormField id="x" label="Name">
        <input id="x" type="text" />
      </FormField>,
    )
    expect(screen.queryByText(/\*/)).toBeNull()
  })

  it('renders an optional hint paragraph beneath the input', () => {
    render(
      <FormField id="x" label="Name" hint="kebab-case identifier">
        <input id="x" type="text" />
      </FormField>,
    )
    const hint = screen.getByText('kebab-case identifier')
    expect(hint.id).toBe('x-hint')
    expect(hint.tagName).toBe('P')
  })

  it('renders error message with role="alert" and overrides hint', () => {
    render(
      <FormField id="x" label="Name" hint="some hint" error="Required field">
        <input id="x" type="text" />
      </FormField>,
    )
    const err = screen.getByRole('alert')
    expect(err).toHaveTextContent('Required field')
    expect(err.id).toBe('x-err')
    // Hint should not render when error is set.
    expect(screen.queryByText('some hint')).toBeNull()
  })

  it('renders neither hint nor error block when both are absent', () => {
    const { container } = render(
      <FormField id="x" label="Name">
        <input id="x" type="text" />
      </FormField>,
    )
    expect(container.querySelectorAll('p').length).toBe(0)
  })
})
