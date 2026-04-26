import { useState } from 'react'
import { Outlet } from 'react-router-dom'
import { Sidebar } from './Sidebar'
import { Header } from './Header'

export function PageShell() {
  const [sidebarOpen, setSidebarOpen] = useState(false)

  return (
    <div className="flex flex-col" style={{ minHeight: '100vh', background: 'var(--color-bg-base)' }}>
      <Header
        showMenu={true}
        onMenuClick={() => setSidebarOpen((v) => !v)}
      />
      <div className="flex flex-1 overflow-hidden" style={{ height: 'calc(100vh - 56px)' }}>
        {/* Desktop sidebar: always visible, 240px */}
        <div className="hidden lg:block shrink-0" style={{ width: '240px' }}>
          <Sidebar />
        </div>

        {/* Tablet sidebar: icon rail 56px */}
        <div className="hidden sm:block lg:hidden shrink-0" style={{ width: '56px' }}>
          <Sidebar collapsed />
        </div>

        {/* Mobile: slide-in overlay */}
        {sidebarOpen && (
          <>
            <div
              className="fixed inset-0 z-40 sm:hidden"
              style={{ background: 'var(--color-bg-overlay)', opacity: 0.6 }}
              aria-hidden="true"
              onClick={() => setSidebarOpen(false)}
            />
            <div
              className="fixed left-0 top-0 h-full z-50 sm:hidden"
              style={{
                width: '240px',
                transform: sidebarOpen ? 'translateX(0)' : 'translateX(-100%)',
                transition: 'transform 300ms ease-in-out',
              }}
            >
              <Sidebar onNavigate={() => setSidebarOpen(false)} />
            </div>
          </>
        )}

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
