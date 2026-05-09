import { useState } from 'react'
import { loginWithKey } from '../lib/api'
import { useAuthStore } from '../stores/authStore'

export function LoginPage() {
  const [key, setKey] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const setStatus = useAuthStore((s) => s.setStatus)

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    setLoading(true)
    try {
      await loginWithKey(key)
      setKey('')
      setStatus('authenticated')
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : ''
      if (msg.startsWith('429')) {
        setError('Too many attempts, wait a moment')
      } else {
        setError('Invalid API key')
      }
    } finally {
      setLoading(false)
    }
  }

  return (
    <div
      className="flex items-center justify-center min-h-screen"
      style={{ background: 'var(--color-bg-base)' }}
    >
      <div
        className="w-full max-w-sm p-8 rounded-xl"
        style={{
          background: 'var(--color-bg-card)',
          border: '1px solid var(--color-border)',
        }}
      >
        <h1
          className="text-xl font-semibold mb-6 text-center"
          style={{ color: 'var(--color-text-primary)' }}
        >
          wayneblacktea
        </h1>
        <form onSubmit={handleSubmit} className="flex flex-col gap-4">
          <input
            type="password"
            placeholder="Enter API key"
            value={key}
            onChange={(e) => setKey(e.target.value)}
            autoComplete="current-password"
            required
            className="w-full px-4 py-2 rounded-lg text-sm outline-none"
            style={{
              background: 'var(--color-bg-input)',
              border: '1px solid var(--color-border)',
              color: 'var(--color-text-primary)',
            }}
            onFocus={(e) => {
              e.currentTarget.style.borderColor = 'var(--color-border-focus)'
            }}
            onBlur={(e) => {
              e.currentTarget.style.borderColor = 'var(--color-border)'
            }}
          />
          {error && (
            <p className="text-sm" style={{ color: 'var(--color-error)' }}>
              {error}
            </p>
          )}
          <button
            type="submit"
            disabled={loading || key.length === 0}
            className="w-full py-2 rounded-lg text-sm font-medium transition-opacity disabled:opacity-50"
            style={{
              background: 'var(--color-accent-blue)',
              color: '#0a1628',
            }}
          >
            {loading ? 'Signing in…' : 'Sign in'}
          </button>
        </form>
      </div>
    </div>
  )
}
