import { useMemo } from 'react'
import { useToast } from '../components/toast'
import { api, toAgentError } from './api'
import { summarizeResult } from './format'

// Every command resolves (failures become toasts), so callers can await it
// without a catch. The returned object is stable while the toast is.
export function useActionCommands() {
  const toast = useToast()
  return useMemo(() => {
    const quietCancel = (what: string) => (e: unknown) => {
      if (toAgentError(e).code === 'cancelled') toast.show(`${what} cancelled.`)
      else toast.error(e)
    }
    return {
      sync: (id: string) =>
        api.sync(id).then((r) => {
          if (r.outcome === 'conflict') toast.show(`${r.conflicts?.length ?? 0} conflict(s) need your choice.`)
          else if (r.outcome === 'deletes-pending') toast.show(`${r.pendingDeletes?.length ?? 0} deletion(s) are waiting for your approval.`)
          else toast.show(`Synced: ${summarizeResult(r)}`)
        }, quietCancel('Sync')),
      snapshot: (id: string) => api.snapshot(id).then((s) => toast.show(`Snapshot ${s.ts} created.`), quietCancel('Snapshot')),
      pause: (id: string) => api.pause(id).catch(toast.error),
      resume: (id: string) => api.resume(id).catch(toast.error),
      cancel: (id: string) => api.cancelSync(id).catch(toast.error),
    }
  }, [toast])
}
