import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { describeError } from '../lib/errors'

interface Toast { id: number; message: string; details?: string; tone: 'info' | 'error' }
interface ToastValue {
  show: (message: string, opts?: { details?: string; tone?: 'info' | 'error' }) => void
  error: (e: unknown) => void
}

const Ctx = createContext<ToastValue | null>(null)
let next = 1

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([])
  const [open, setOpen] = useState<number | null>(null)
  const timers = useRef(new Map<number, ReturnType<typeof setTimeout>>())
  const clearTimer = useCallback((id: number) => {
    const h = timers.current.get(id)
    if (h !== undefined) clearTimeout(h)
    timers.current.delete(id)
  }, [])
  const dismiss = useCallback((id: number) => {
    clearTimer(id)
    setToasts((t) => t.filter((x) => x.id !== id))
  }, [clearTimer])
  useEffect(() => {
    const m = timers.current
    return () => {
      m.forEach((h) => clearTimeout(h))
      m.clear()
    }
  }, [])
  const show = useCallback<ToastValue['show']>((message, opts) => {
    const t: Toast = { id: next++, message, details: opts?.details, tone: opts?.tone ?? 'info' }
    setToasts((all) => [...all, t])
    timers.current.set(t.id, setTimeout(() => dismiss(t.id), t.details ? 12000 : 6000))
  }, [dismiss])
  const error = useCallback((e: unknown) => {
    const d = describeError(e)
    show(d.message, { details: d.details, tone: 'error' })
  }, [show])
  // Stable, so screens can list the toast in effect dependencies.
  const value = useMemo(() => ({ show, error }), [show, error])
  return (
    <Ctx.Provider value={value}>
      {children}
      <div className="fixed bottom-4 right-4 z-50 flex w-96 flex-col gap-2">
        {toasts.map((t) => (
          <div key={t.id} role="alert" className={`rounded-md p-3 text-sm shadow-lg ${t.tone === 'error' ? 'bg-red-600 text-white' : 'bg-zinc-800 text-white'}`}>
            <div className="flex justify-between gap-2">
              <span>{t.message}</span>
              <button type="button" aria-label="Dismiss" onClick={() => dismiss(t.id)}>×</button>
            </div>
            {t.details && (
              <button type="button" className="mt-1 text-xs underline" onClick={() => {
                if (open !== t.id) clearTimer(t.id)
                setOpen(open === t.id ? null : t.id)
              }}>Details</button>
            )}
            {t.details && open === t.id && <pre className="mt-1 whitespace-pre-wrap break-all text-xs">{t.details}</pre>}
          </div>
        ))}
      </div>
    </Ctx.Provider>
  )
}

export function useToast(): ToastValue {
  const v = useContext(Ctx)
  if (!v) throw new Error('useToast outside ToastProvider')
  return v
}
