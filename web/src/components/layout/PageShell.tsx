import { useState, useEffect } from 'react'
import { Outlet } from 'react-router-dom'
import { Sidebar } from './Sidebar'
import { Header } from './Header'

export function PageShell() {
  const [sidebarOpen, setSidebarOpen] = useState(false)

  // Lock body scroll when overlay is open
  useEffect(() => {
    document.body.style.overflow = sidebarOpen ? 'hidden' : ''
    return () => { document.body.style.overflow = '' }
  }, [sidebarOpen])

  // Escape to close
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setSidebarOpen(false) }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [])

  return (
    <div className="flex flex-col" style={{ minHeight: '100vh', background: 'var(--color-bg-base)' }}>
      <Header
        onMenuClick={() => setSidebarOpen((v) => !v)}
        sidebarOpen={sidebarOpen}
      />

      <div className="flex flex-1 overflow-hidden" style={{ height: 'calc(100vh - var(--spacing-header))' }}>
        {/* Desktop: always-visible 240px sidebar */}
        <div className="hidden lg:block shrink-0" style={{ width: '240px' }}>
          <Sidebar />
        </div>

        {/* Tablet: collapsed 56px icon rail */}
        <div className="hidden sm:block lg:hidden shrink-0" style={{ width: '56px' }}>
          <Sidebar collapsed />
        </div>

        {/* Overlay backdrop starts below the sticky header and stays aligned with its tokenized height. */}
        <div
          className="fixed inset-x-0 bottom-0 z-40 lg:hidden"
          aria-hidden="true"
          style={{
            top: 'var(--spacing-header)',
            background: 'rgba(0, 0, 0, 0.55)',
            opacity: sidebarOpen ? 1 : 0,
            pointerEvents: sidebarOpen ? 'auto' : 'none',
            transition: 'opacity 250ms ease',
          }}
          onClick={() => setSidebarOpen(false)}
        />

        {/* Slide-in sidebar — starts below header, always mounted for smooth animation */}
        <div
          className="fixed left-0 bottom-0 z-50 lg:hidden"
          role="dialog"
          aria-modal="true"
          aria-label="Navigation"
          style={{
            top: 'var(--spacing-header)',
            width: '240px',
            transform: sidebarOpen ? 'translateX(0)' : 'translateX(-240px)',
            transition: 'transform 280ms cubic-bezier(0.4, 0, 0.2, 1)',
            willChange: 'transform',
          }}
        >
          <Sidebar onNavigate={() => setSidebarOpen(false)} />
        </div>

        <main
          className="flex-1 overflow-y-auto"
          style={{ background: 'var(--color-bg-base)' }}
        >
          <Outlet />
        </main>
      </div>
    </div>
  )
}
