import { createContext, useCallback, useContext, useEffect, useId, useMemo, useRef, useState, type ReactNode } from 'react'
import { ConfirmDialog } from '../components/ui'

export type Tab = 'overview' | 'rules' | 'items' | 'snapshots' | 'history'
export type Route =
  | { name: 'actions' }
  | { name: 'action'; id: string; tab: Tab }
  | { name: 'conflicts'; id: string }
  | { name: 'deletes'; id: string }
  | { name: 'profiles' }
  | { name: 'activity' }
  | { name: 'settings' }

interface RouterValue {
  route: Route
  navigate: (r: Route) => void
  setDirty: (key: string, dirty: boolean) => void
}

const Ctx = createContext<RouterValue | null>(null)

export function RouterProvider({ initial = { name: 'actions' }, children }: { initial?: Route; children: ReactNode }) {
  const [route, setRoute] = useState<Route>(initial)
  const [pending, setPending] = useState<Route | null>(null)
  const dirty = useRef(new Set<string>())
  const setDirty = useCallback((key: string, d: boolean) => {
    if (d) dirty.current.add(key)
    else dirty.current.delete(key)
  }, [])
  const navigate = useCallback((r: Route) => {
    if (dirty.current.size > 0) setPending(r)
    else setRoute(r)
  }, [])
  const value = useMemo(() => ({ route, navigate, setDirty }), [route, navigate, setDirty])
  return (
    <Ctx.Provider value={value}>
      {children}
      {pending && (
        <ConfirmDialog
          title="Discard changes?"
          message="You have unsaved changes. Leave this page and discard them?"
          confirmLabel="Discard"
          danger
          onCancel={() => setPending(null)}
          onConfirm={() => {
            dirty.current.clear()
            setRoute(pending)
            setPending(null)
          }}
        />
      )}
    </Ctx.Provider>
  )
}

export function useRouter(): RouterValue {
  const v = useContext(Ctx)
  if (!v) throw new Error('useRouter outside RouterProvider')
  return v
}

// useDirtyGuard makes navigation ask before leaving while dirty is true.
export function useDirtyGuard(dirty: boolean) {
  const { setDirty } = useRouter()
  const key = useId()
  useEffect(() => {
    setDirty(key, dirty)
    return () => setDirty(key, false)
  }, [dirty, key, setDirty])
}
