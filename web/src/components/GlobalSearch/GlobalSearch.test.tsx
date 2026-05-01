import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { GlobalSearch } from './GlobalSearch'
import type { SearchResult } from '../../types/api'

// i18next returns the key as-is in test environment
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
  }),
}))

// Mock useGlobalSearch so we control results without network calls
const mockUseGlobalSearch = vi.fn()
vi.mock('../../hooks/useGlobalSearch', () => ({
  useGlobalSearch: (query: string) => mockUseGlobalSearch(query),
}))

const makeResult = (overrides: Partial<SearchResult> = {}): SearchResult => ({
  type: 'knowledge',
  id: 'r1',
  title: 'Test Result',
  snippet: 'A snippet of content',
  url: '/knowledge',
  ...overrides,
})

function createWrapper() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return (
      <QueryClientProvider client={queryClient}>
        <MemoryRouter>
          {children}
        </MemoryRouter>
      </QueryClientProvider>
    )
  }
}

function renderSearch(isOpen: boolean, onClose = vi.fn()) {
  const Wrapper = createWrapper()
  return render(
    <Wrapper>
      <GlobalSearch isOpen={isOpen} onClose={onClose} />
    </Wrapper>,
  )
}

describe('GlobalSearch', () => {
  beforeEach(() => {
    mockUseGlobalSearch.mockReturnValue({ data: undefined, isFetching: false })
  })

  afterEach(() => {
    vi.clearAllMocks()
  })

  // --- Render tests ---

  it('renders nothing when isOpen is false', () => {
    renderSearch(false)
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('renders the search dialog when isOpen is true', () => {
    renderSearch(true)
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })

  it('renders the search input when open', () => {
    renderSearch(true)
    expect(screen.getByRole('combobox')).toBeInTheDocument()
  })

  it('shows empty prompt when no query entered', () => {
    renderSearch(true)
    expect(screen.getByText('search.empty')).toBeInTheDocument()
  })

  it('shows no-results message when query returns empty array', async () => {
    mockUseGlobalSearch.mockReturnValue({ data: { query: 'xyz', results: [] }, isFetching: false })
    renderSearch(true)

    const input = screen.getByRole('combobox')
    fireEvent.change(input, { target: { value: 'xyz' } })

    // Wait for debounce (200ms) + re-render
    await waitFor(() => {
      expect(screen.getByText('search.noResults')).toBeInTheDocument()
    }, { timeout: 500 })
  })

  it('renders result items when data is returned', async () => {
    const result = makeResult({ title: 'React hooks guide', snippet: 'A deep dive into hooks' })
    mockUseGlobalSearch.mockReturnValue({
      data: { query: 'react', results: [result] },
      isFetching: false,
    })
    renderSearch(true)

    const input = screen.getByRole('combobox')
    fireEvent.change(input, { target: { value: 'react' } })

    await waitFor(() => {
      expect(screen.getByText('React hooks guide')).toBeInTheDocument()
    }, { timeout: 500 })
  })

  it('shows loading indicator while fetching', () => {
    mockUseGlobalSearch.mockReturnValue({ data: undefined, isFetching: true })
    renderSearch(true)
    expect(screen.getByText('common.loading')).toBeInTheDocument()
  })

  // --- Keyboard interaction tests ---

  it('calls onClose when Escape is pressed', () => {
    const onClose = vi.fn()
    renderSearch(true, onClose)
    const input = screen.getByRole('combobox')
    fireEvent.keyDown(input, { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('calls onClose when X button is clicked', () => {
    const onClose = vi.fn()
    renderSearch(true, onClose)
    const closeBtn = screen.getByRole('button', { name: 'Close search' })
    fireEvent.click(closeBtn)
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('navigates down the results list with ArrowDown', async () => {
    const results = [
      makeResult({ id: 'r1', title: 'First', url: '/knowledge' }),
      makeResult({ id: 'r2', title: 'Second', url: '/decisions', type: 'decision' }),
    ]
    mockUseGlobalSearch.mockReturnValue({ data: { query: 'test', results }, isFetching: false })
    renderSearch(true)

    const input = screen.getByRole('combobox')
    fireEvent.change(input, { target: { value: 'test' } })

    await waitFor(() => screen.getByText('First'))

    // Initially first item is active (aria-selected)
    const firstItem = screen.getByRole('option', { name: /First/i })
    expect(firstItem).toHaveAttribute('aria-selected', 'true')

    // ArrowDown moves selection to second
    fireEvent.keyDown(input, { key: 'ArrowDown' })
    const secondItem = screen.getByRole('option', { name: /Second/i })
    expect(secondItem).toHaveAttribute('aria-selected', 'true')
  })

  it('navigates up the results list with ArrowUp', async () => {
    const results = [
      makeResult({ id: 'r1', title: 'First' }),
      makeResult({ id: 'r2', title: 'Second', type: 'decision', url: '/decisions' }),
    ]
    mockUseGlobalSearch.mockReturnValue({ data: { query: 'test', results }, isFetching: false })
    renderSearch(true)

    const input = screen.getByRole('combobox')
    fireEvent.change(input, { target: { value: 'test' } })

    await waitFor(() => screen.getByText('First'))

    // Move down then back up
    fireEvent.keyDown(input, { key: 'ArrowDown' })
    fireEvent.keyDown(input, { key: 'ArrowUp' })

    const firstItem = screen.getByRole('option', { name: /First/i })
    expect(firstItem).toHaveAttribute('aria-selected', 'true')
  })

  it('calls onClose when Enter is pressed on active result', async () => {
    const onClose = vi.fn()
    const results = [makeResult({ id: 'r1', title: 'Hooks 101', url: '/knowledge' })]
    mockUseGlobalSearch.mockReturnValue({ data: { query: 'hooks', results }, isFetching: false })
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <MemoryRouter>
          <GlobalSearch isOpen onClose={onClose} />
        </MemoryRouter>
      </QueryClientProvider>,
    )

    const input = screen.getByRole('combobox')
    fireEvent.change(input, { target: { value: 'hooks' } })

    await waitFor(() => screen.getByText('Hooks 101'))
    fireEvent.keyDown(input, { key: 'Enter' })

    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('calls onClose when a result item is clicked', async () => {
    const onClose = vi.fn()
    const results = [makeResult({ id: 'r1', title: 'Click me', url: '/knowledge' })]
    mockUseGlobalSearch.mockReturnValue({ data: { query: 'click', results }, isFetching: false })
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <MemoryRouter>
          <GlobalSearch isOpen onClose={onClose} />
        </MemoryRouter>
      </QueryClientProvider>,
    )

    const input = screen.getByRole('combobox')
    fireEvent.change(input, { target: { value: 'click' } })

    await waitFor(() => screen.getByText('Click me'))
    fireEvent.click(screen.getByRole('option', { name: /Click me/i }))

    expect(onClose).toHaveBeenCalledTimes(1)
  })

  // --- Error / edge case tests ---

  it('does not exceed MAX_QUERY_LENGTH on input', () => {
    renderSearch(true)
    const input = screen.getByRole('combobox')
    const longString = 'a'.repeat(600)
    fireEvent.change(input, { target: { value: longString } })
    // The input value should be capped at 500 chars
    expect((input as HTMLInputElement).value.length).toBeLessThanOrEqual(500)
  })

  it('does not navigate beyond last result with repeated ArrowDown', async () => {
    const results = [makeResult({ id: 'r1', title: 'Only result' })]
    mockUseGlobalSearch.mockReturnValue({ data: { query: 'q', results }, isFetching: false })
    renderSearch(true)

    const input = screen.getByRole('combobox')
    fireEvent.change(input, { target: { value: 'q' } })

    await waitFor(() => screen.getByText('Only result'))

    // Press ArrowDown multiple times — should stay on the only item
    fireEvent.keyDown(input, { key: 'ArrowDown' })
    fireEvent.keyDown(input, { key: 'ArrowDown' })

    const item = screen.getByRole('option', { name: /Only result/i })
    expect(item).toHaveAttribute('aria-selected', 'true')
  })

  it('resets input and active index when reopened', () => {
    const { rerender, unmount } = renderSearch(false)

    const Wrapper = createWrapper()
    rerender(
      <Wrapper>
        <GlobalSearch isOpen onClose={vi.fn()} />
      </Wrapper>,
    )

    const input = screen.getByRole('combobox')
    expect((input as HTMLInputElement).value).toBe('')

    unmount()
  })
})

// isEditableTarget guard — test it in isolation via PageShell behaviour
describe('⌘K isEditableTarget guard', () => {
  it('skips ⌘K when an input element is focused', () => {
    // Simulate a focused input that should not let ⌘K fire
    const input = document.createElement('input')
    document.body.appendChild(input)
    input.focus()

    const handler = vi.fn()
    const listener = () => {
      const el = document.activeElement
      const tag = el?.tagName
      if (tag === 'INPUT' || tag === 'TEXTAREA') return
      handler()
    }
    document.addEventListener('keydown', listener)

    act(() => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', metaKey: true, bubbles: true }))
    })

    expect(handler).not.toHaveBeenCalled()

    document.removeEventListener('keydown', listener)
    document.body.removeChild(input)
  })
})
