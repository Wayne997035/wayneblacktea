import { describe, it, expect, vi } from 'vitest'

// useDashboardUpcoming is a thin useQuery wrapper; we verify it calls the
// correct API path so a URL typo would be caught without spinning up a server.
const apiFetchMock = vi.fn()
vi.mock('../lib/api', () => ({
  apiFetch: (...args: unknown[]) => apiFetchMock(...args),
}))

describe('useDashboardUpcoming', () => {
  it('is importable and exports UpcomingTask type', async () => {
    // Verify the module exports without error and exposes the hook function.
    const mod = await import('./useDashboardUpcoming')
    expect(typeof mod.useDashboardUpcoming).toBe('function')
  })

  it('calls /api/dashboard/upcoming via apiFetch when queryFn is invoked', async () => {
    // Extract the queryFn by introspecting useQuery's config through the hook.
    // We mock @tanstack/react-query so we can capture the options object.
    const capturedOpts = { current: null as unknown }
    vi.doMock('@tanstack/react-query', async (importOriginal) => {
      const actual = await importOriginal<typeof import('@tanstack/react-query')>()
      return {
        ...actual,
        useQuery: (opts: unknown) => {
          capturedOpts.current = opts
          return { data: undefined, isLoading: false }
        },
      }
    })

    // Re-import after mocking to get the patched version.
    const { useDashboardUpcoming } = await import('./useDashboardUpcoming?test-hook')
    useDashboardUpcoming()

    const opts = capturedOpts.current as { queryKey: string[]; staleTime: number; queryFn: () => Promise<unknown> }
    expect(opts.queryKey).toEqual(['dashboard', 'upcoming'])
    expect(opts.staleTime).toBe(30_000)

    apiFetchMock.mockResolvedValueOnce([])
    await opts.queryFn()
    expect(apiFetchMock).toHaveBeenCalledWith('/api/dashboard/upcoming')

    vi.doUnmock('@tanstack/react-query')
  })
})
