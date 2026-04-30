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

// Kick off session establishment in the background — do NOT block rendering.
// apiFetch retries automatically on 401, so first-load users are handled
// without delaying the initial paint by a full Railway round-trip.
initSession()

createRoot(rootElement).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
      <ToastContainer />
    </QueryClientProvider>
  </StrictMode>,
)
