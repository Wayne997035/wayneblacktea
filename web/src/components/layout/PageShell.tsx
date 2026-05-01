import { useState, useEffect } from 'react'
import { Outlet } from 'react-router-dom'
import { Sidebar } from './Sidebar'
import { Header } from './Header'
import { GlobalSearch } from '../GlobalSearch/GlobalSearch'
import { useUiStore } from '../../stores/uiStore'

/** Returns true when the active element is an editable field (input, textarea, contentEditable). */
function isEditableTarget(el: Element | null): boolean {
  if (!el) return false
  const tag = el.tagName
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return true
  if ((el as HTMLElement).isContentEditable) return true
  return false
}

export function PageShell() {
  const [sidebarOpen, setSidebarOpen] = useState(false)
  const { searchOpen, closeSearch, toggleSearch } = useUiStore()

  // Lock body scroll when overlay is open
  useEffect(() => {
    document.body.style.overflow = (sidebarOpen || searchOpen) ? 'hidden' : ''
    return () => { document.body.style.overflow = '' }
  }, [sidebarOpen, searchOpen])

  // Escape closes sidebar (search palette handles its own Escape with stopPropagation)
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setSidebarOpen(false) }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [])

  // ⌘K / Ctrl+K opens search palette — MUST skip when user is typing in an input
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'k' || !(e.metaKey || e.ctrlKey)) return
      // Skip if the active element is an editable field
      if (isEditableTarget(document.activeElement)) return
      e.preventDefault()
      e.stopPropagation()
      toggleSearch()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [toggleSearch])

  return (
    <div className="flex flex-col" style={{ minHeight: '100vh', background: 'var(--color-bg-base)' }}>
      <Header
        onMenuClick={() => setSidebarOpen((v) => !v)}
        sidebarOpen={sidebarOpen}
      />

      <GlobalSearch isOpen={searchOpen} onClose={closeSearch} />

      <div className="flex flex-1 overflow-hidden" style={{ height: 'calc(100vh - var(--spacing-header))' }}>
        {/* Desktop: always-visible 240px sidebar */}
        <div className="hidden lg:block shrink-0" style={{ width: '240px' }}>
          <Sidebar />
        </div>

        {/* Tablet: collapsed 56px icon rail */}
        <div className="hidden sm:block lg:hidden shrink-0" style={{ width: '56px' }}>
          <Sidebar collapsed />
        </div>

        {/* Overlay backdrop starts below the sticky header */}
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

        {/* Slide-in sidebar — always mounted for smooth animation */}
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
