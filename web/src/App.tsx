import { createBrowserRouter, RouterProvider, Navigate } from 'react-router-dom'
import { PageShell } from './components/layout/PageShell'
import { DashboardPage } from './pages/DashboardPage'
import { GtdPage } from './pages/GtdPage'
import { WorkspacePage } from './pages/WorkspacePage'
import { DecisionsPage } from './pages/DecisionsPage'
import { KnowledgePage } from './pages/KnowledgePage'
import { ReviewsPage } from './pages/ReviewsPage'
import { ProjectDetailPage } from './pages/ProjectDetailPage'

const router = createBrowserRouter([
  {
    path: '/',
    element: <PageShell />,
    children: [
      { index: true, element: <DashboardPage /> },
      { path: 'gtd', element: <GtdPage /> },
      { path: 'workspace', element: <WorkspacePage /> },
      { path: 'workspace/projects/:id', element: <ProjectDetailPage /> },
      { path: 'decisions', element: <DecisionsPage /> },
      { path: 'knowledge', element: <KnowledgePage /> },
      { path: 'reviews', element: <ReviewsPage /> },
      { path: '*', element: <Navigate to="/" replace /> },
    ],
  },
])

export function App() {
  return <RouterProvider router={router} />
}
