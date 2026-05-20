import { useEffect } from 'react'
import { createBrowserRouter, RouterProvider, Navigate } from 'react-router-dom'
import { PageShell } from './components/layout/PageShell'
import { DashboardPage } from './pages/DashboardPage'
import { GtdPage } from './pages/GtdPage'
import { WorkspacePage } from './pages/WorkspacePage'
import { DecisionsPage } from './pages/DecisionsPage'
import { KnowledgePage } from './pages/KnowledgePage'
import { ReviewsPage } from './pages/ReviewsPage'
import { ProjectDetailPage } from './pages/ProjectDetailPage'
import { RepoDetailPage } from './pages/RepoDetailPage'
import { LearningHistoryPage } from './pages/LearningHistoryPage'
import { CalendarPage } from './pages/CalendarPage'
import { ProposalsPage } from './pages/ProposalsPage'
import { VisionPage } from './pages/VisionPage'
import { AutomationCandidatesPage } from './pages/AutomationCandidatesPage'
import { LoginPage } from './pages/LoginPage'
import { useAuthStore } from './stores/authStore'
import { apiFetch } from './lib/api'

const router = createBrowserRouter([
  {
    path: '/',
    element: <PageShell />,
    children: [
      { index: true, element: <DashboardPage /> },
      { path: 'gtd', element: <GtdPage /> },
      { path: 'workspace', element: <WorkspacePage /> },
      { path: 'workspace/projects/:id', element: <ProjectDetailPage /> },
      { path: 'workspace/repos/:id', element: <RepoDetailPage /> },
      { path: 'decisions', element: <DecisionsPage /> },
      { path: 'knowledge', element: <KnowledgePage /> },
      { path: 'reviews', element: <ReviewsPage /> },
      { path: 'learning/history', element: <LearningHistoryPage /> },
      { path: 'calendar', element: <CalendarPage /> },
      { path: 'proposals', element: <ProposalsPage /> },
      { path: 'vision', element: <VisionPage /> },
      { path: 'automation/candidates', element: <AutomationCandidatesPage /> },
      { path: '*', element: <Navigate to="/" replace /> },
    ],
  },
])

export function App() {
  const status = useAuthStore((s) => s.status)
  const setStatus = useAuthStore((s) => s.setStatus)

  useEffect(() => {
    apiFetch('/api/context/today')
      .then(() => setStatus('authenticated'))
      .catch(() => setStatus('unauthenticated'))
  }, [setStatus])

  if (status === 'loading') {
    return (
      <div
        className="flex items-center justify-center min-h-screen"
        style={{ background: 'var(--color-bg-base)', color: 'var(--color-text-muted)' }}
      >
        <span className="text-sm">Loading…</span>
      </div>
    )
  }

  if (status === 'unauthenticated') {
    return <LoginPage />
  }

  return <RouterProvider router={router} />
}
