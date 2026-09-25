import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ToastContainer } from './Toast'
import { useToastStore } from '../../stores/toastStore'

beforeEach(() => {
  useToastStore.setState({ toasts: [] })
})

describe('Toast', () => {
  it('renders nothing when there are no toasts', () => {
    const { container } = render(<ToastContainer />)
    expect(container.firstChild).toBeNull()
  })

  it('renders a toast message with role=alert', () => {
    useToastStore.setState({ toasts: [{ id: 't1', message: 'Saved', type: 'success' }] })
    render(<ToastContainer />)
    expect(screen.getByRole('alert')).toHaveTextContent('Saved')
  })

  // [F0925-25] Color-token reconciliation step 1: jsdom cannot resolve CSS
  // custom properties, so we assert the inline style references the
  // expected var(--x) name. Step 2 (index.css: --color-white = the
  // original literal #ffffff) is verified via
  // `grep -n '\-\-color-white:' src/index.css` — see 收工單.
  it('error toast references --color-error / --color-white (was hardcoded #fff)', () => {
    useToastStore.setState({ toasts: [{ id: 't2', message: 'Failed', type: 'error' }] })
    render(<ToastContainer />)
    const toast = screen.getByRole('alert')
    expect(toast.style.background).toBe('var(--color-error)')
    expect(toast.style.color).toBe('var(--color-white)')
  })
})
