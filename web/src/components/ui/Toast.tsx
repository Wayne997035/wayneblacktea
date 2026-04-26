import { useEffect } from 'react'
import { X } from 'lucide-react'
import { useToastStore, type Toast } from '../../stores/toastStore'

interface ToastItemProps {
  toast: Toast;
}

const typeStyles: Record<Toast['type'], { bg: string; color: string }> = {
  error:   { bg: 'var(--color-error)',   color: '#fff' },
  success: { bg: 'var(--color-success)', color: '#fff' },
  info:    { bg: 'var(--color-info)',    color: 'var(--color-bg-base)' },
}

function ToastItem({ toast }: ToastItemProps) {
  const { removeToast } = useToastStore()
  const duration = toast.duration ?? 5000

  useEffect(() => {
    const timer = setTimeout(() => removeToast(toast.id), duration)
    return () => clearTimeout(timer)
  }, [toast.id, duration, removeToast])

  const { bg, color } = typeStyles[toast.type]

  return (
    <div
      role="alert"
      aria-live="assertive"
      className="flex items-center gap-3 px-4 py-3 rounded-lg shadow-lg"
      style={{ background: bg, color, minWidth: '240px', maxWidth: '400px' }}
    >
      <span className="text-body flex-1">{toast.message}</span>
      <button
        type="button"
        onClick={() => removeToast(toast.id)}
        aria-label="Dismiss"
        className="shrink-0 opacity-80 hover:opacity-100"
        style={{ color, background: 'transparent', border: 'none', cursor: 'pointer', padding: 0 }}
      >
        <X size={16} />
      </button>
    </div>
  )
}

export function ToastContainer() {
  const { toasts } = useToastStore()
  if (toasts.length === 0) return null

  return (
    <div
      className="fixed bottom-6 right-6 z-[100] flex flex-col gap-2"
      aria-label="Notifications"
    >
      {toasts.map((t) => <ToastItem key={t.id} toast={t} />)}
    </div>
  )
}
