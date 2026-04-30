import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import './i18n/index'
import './index.css'
import { App } from './App'
import { ToastContainer } from './components/ui/Toast'
import { initSession } from './lib/api'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      refetchOnWindowFocus: false,
    },
  },
})

const rootElement = document.getElementById('root')
if (!rootElement) throw new Error('Root element not found')

// Obtain the wbt_session httpOnly cookie from the server before mounting the app.
// If this fails (e.g. network error), the app still mounts; API calls will get 401s.
initSession().catch(() => {
  // Intentionally silent — the app will degrade gracefully with 401 responses.
})

createRoot(rootElement).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
      <ToastContainer />
    </QueryClientProvider>
  </StrictMode>,
)
