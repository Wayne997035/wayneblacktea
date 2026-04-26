import { createBrowserRouter, RouterProvider, Navigate } from 'react-router-dom'
import { PageShell } from './components/layout/PageShell'
import { DashboardPage } from './pages/DashboardPage'
import { GtdPage } from './pages/GtdPage'
import { WorkspacePage } from './pages/WorkspacePage'
import { DecisionsPage } from './pages/DecisionsPage'

const router = createBrowserRouter([
  {
    path: '/',
    element: <PageShell />,
    children: [
      { index: true, element: <DashboardPage /> },
      { path: 'gtd', element: <GtdPage /> },
      { path: 'workspace', element: <WorkspacePage /> },
      { path: 'decisions', element: <DecisionsPage /> },
      { path: 'knowledge', element: <Navigate to="/" replace /> },
      { path: 'reviews', element: <Navigate to="/" replace /> },
      { path: '*', element: <Navigate to="/" replace /> },
    ],
  },
])

export function App() {
  return <RouterProvider router={router} />
}
