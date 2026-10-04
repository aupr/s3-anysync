import { useEffect, useRef, type ButtonHTMLAttributes, type InputHTMLAttributes, type ReactNode, type SelectHTMLAttributes } from 'react'
import type { Tone } from '../lib/format'

type Variant = 'primary' | 'secondary' | 'danger' | 'ghost'
const variants: Record<Variant, string> = {
  primary: 'bg-blue-600 text-white hover:bg-blue-700 disabled:bg-blue-300',
  secondary: 'border border-zinc-300 bg-white text-zinc-800 hover:bg-zinc-50 dark:border-zinc-600 dark:bg-zinc-800 dark:text-zinc-100 dark:hover:bg-zinc-700',
  danger: 'bg-red-600 text-white hover:bg-red-700 disabled:bg-red-300',
  ghost: 'text-zinc-700 hover:bg-zinc-100 dark:text-zinc-200 dark:hover:bg-zinc-800',
}

export function Button({ variant = 'secondary', className = '', ...p }: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: Variant }) {
  return <button type="button" className={`rounded-md px-3 py-1.5 text-sm font-medium disabled:cursor-not-allowed disabled:opacity-60 ${variants[variant]} ${className}`} {...p} />
}

const tones: Record<Tone, string> = {
  neutral: 'bg-zinc-100 text-zinc-700 dark:bg-zinc-800 dark:text-zinc-300',
  ok: 'bg-green-100 text-green-800 dark:bg-green-900/40 dark:text-green-300',
  warn: 'bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-300',
  error: 'bg-red-100 text-red-800 dark:bg-red-900/40 dark:text-red-300',
}

export function Badge({ tone = 'neutral', children }: { tone?: Tone; children: ReactNode }) {
  return <span className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${tones[tone]}`}>{children}</span>
}

export function Field({ label, hint, error, children }: { label: string; hint?: ReactNode; error?: string; children: ReactNode }) {
  return (
    <label className="block space-y-1">
      <span className="text-sm font-medium">{label}</span>
      {children}
      {hint && <span className="block text-xs text-zinc-500">{hint}</span>}
      {error && <span role="alert" className="block text-xs text-red-600">{error}</span>}
    </label>
  )
}

const control = 'w-full rounded-md border border-zinc-300 bg-white px-2 py-1.5 text-sm dark:border-zinc-600 dark:bg-zinc-800'

export function Input(p: InputHTMLAttributes<HTMLInputElement>) {
  return <input className={control} {...p} />
}

export function Select(p: SelectHTMLAttributes<HTMLSelectElement>) {
  return <select className={control} {...p} />
}

export function Checkbox({ label, checked, onChange, disabled }: { label: ReactNode; checked: boolean; onChange: (v: boolean) => void; disabled?: boolean }) {
  return (
    <label className="flex items-center gap-2 text-sm">
      <input type="checkbox" checked={checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />
      <span>{label}</span>
    </label>
  )
}

// Open modals, topmost last; only the topmost one reacts to Escape.
const modalStack: (() => void)[] = []

export function Modal({ title, children, footer, onClose, wide }: { title: string; children: ReactNode; footer?: ReactNode; onClose: () => void; wide?: boolean }) {
  const close = useRef(onClose)
  close.current = onClose
  useEffect(() => {
    const handler = () => close.current()
    modalStack.push(handler)
    const k = (e: KeyboardEvent) => e.key === 'Escape' && modalStack[modalStack.length - 1] === handler && handler()
    window.addEventListener('keydown', k)
    return () => {
      window.removeEventListener('keydown', k)
      const i = modalStack.indexOf(handler)
      if (i >= 0) modalStack.splice(i, 1)
    }
  }, [])
  return (
    <div className="fixed inset-0 z-40 flex items-center justify-center bg-black/40 p-6">
      <div role="dialog" aria-modal="true" aria-label={title} className={`flex max-h-full w-full flex-col rounded-lg bg-white shadow-xl dark:bg-zinc-900 ${wide ? 'max-w-4xl' : 'max-w-lg'}`}>
        <h2 className="border-b border-zinc-200 px-4 py-3 text-base font-semibold dark:border-zinc-700">{title}</h2>
        <div className="overflow-auto px-4 py-3">{children}</div>
        {footer && <div className="flex justify-end gap-2 border-t border-zinc-200 px-4 py-3 dark:border-zinc-700">{footer}</div>}
      </div>
    </div>
  )
}

export function ConfirmDialog(p: { title: string; message: ReactNode; confirmLabel: string; danger?: boolean; busy?: boolean; onConfirm: () => void; onCancel: () => void }) {
  return (
    <Modal title={p.title} onClose={p.onCancel} footer={
      <>
        <Button disabled={p.busy} onClick={p.onCancel}>Cancel</Button>
        <Button variant={p.danger ? 'danger' : 'primary'} disabled={p.busy} onClick={p.onConfirm}>{p.confirmLabel}</Button>
      </>
    }>
      <div className="text-sm">{p.message}</div>
    </Modal>
  )
}

export function Spinner({ label = 'Loading' }: { label?: string }) {
  return <span role="status" aria-label={label} className="inline-block h-4 w-4 animate-spin rounded-full border-2 border-zinc-300 border-t-blue-600" />
}

export function EmptyState({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="rounded-lg border border-dashed border-zinc-300 p-8 text-center dark:border-zinc-700">
      <p className="font-medium">{title}</p>
      {children && <div className="mt-2 text-sm text-zinc-500">{children}</div>}
    </div>
  )
}

export const th = 'px-3 py-2 text-left text-xs font-semibold uppercase text-zinc-500'
export const td = 'px-3 py-2 text-sm align-top'
