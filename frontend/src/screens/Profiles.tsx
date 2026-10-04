import { useState } from 'react'
import { useToast } from '../components/toast'
import { Button, ConfirmDialog, EmptyState, Modal, td, th } from '../components/ui'
import { api } from '../lib/api'
import { providerLabel } from '../lib/providers'
import { useStore } from '../lib/store'
import type { ProbeResult, Profile } from '../lib/types'
import { ProbeChecklist, ProfileForm } from './ProfileForm'

export function Profiles() {
  const { state, reloadProfiles } = useStore()
  const toast = useToast()
  const [editing, setEditing] = useState<Profile | 'new' | null>(null)
  const [removing, setRemoving] = useState<Profile | null>(null)
  const [probe, setProbe] = useState<{ p: Profile; r: ProbeResult | null } | null>(null)

  const usedBy = (id: string) => Object.values(state.actions).filter((a) => a.profileId === id).map((a) => a.action.name)
  const done = () => {
    setEditing(null)
    reloadProfiles().catch(toast.error)
  }
  const test = async (p: Profile) => {
    setProbe({ p, r: null })
    try {
      setProbe({ p, r: await api.testProfile(p.id) })
      reloadProfiles().catch(toast.error)
    } catch (e) {
      setProbe(null)
      toast.error(e)
    }
  }
  const remove = async (p: Profile) => {
    try {
      await api.removeProfile(p.id)
      setRemoving(null)
      await reloadProfiles()
    } catch (e) {
      setRemoving(null)
      toast.error(e)
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">Profiles</h1>
        <Button variant="primary" onClick={() => setEditing('new')}>New profile</Button>
      </div>
      {state.profiles.length === 0 ? (
        <EmptyState title="No profiles yet">A profile is a connection to an S3-compatible bucket.</EmptyState>
      ) : (
        <table className="w-full">
          <thead><tr><th className={th}>Name</th><th className={th}>Bucket</th><th className={th}>Endpoint</th><th className={th}>Cloud lock</th><th className={th} /></tr></thead>
          <tbody>
            {state.profiles.map((p) => {
              const used = usedBy(p.id)
              return (
                <tr key={p.id} className="border-t border-zinc-200 dark:border-zinc-800">
                  <td className={td}>{p.name}<div className="text-xs text-zinc-500">{providerLabel(p.provider)}</div></td>
                  <td className={td}>{p.bucket}{p.rootPrefix && `/${p.rootPrefix}`}</td>
                  <td className={td}>{p.endpoint || 'AWS default'}</td>
                  <td className={td}>{p.conditionalWrites ? 'Conditional' : 'Best effort'}</td>
                  <td className={`${td} space-x-2 whitespace-nowrap text-right`}>
                    <Button onClick={() => setEditing(p)}>Edit</Button>
                    <Button onClick={() => test(p)}>Test connection</Button>
                    <Button variant="danger" disabled={used.length > 0} title={used.length ? `Used by: ${used.join(', ')}` : undefined} onClick={() => setRemoving(p)}>Remove</Button>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      )}
      {editing && <ProfileForm profile={editing === 'new' ? undefined : editing} onSaved={done} onCancel={() => setEditing(null)} />}
      {removing && (
        <ConfirmDialog title={`Remove ${removing.name}?`} danger confirmLabel="Remove"
          message="The profile and its stored secret are deleted from this machine. The bucket is not touched."
          onCancel={() => setRemoving(null)} onConfirm={() => remove(removing)} />
      )}
      {probe && (
        <Modal title={`Test ${probe.p.name}`} onClose={() => setProbe(null)} footer={<Button onClick={() => setProbe(null)}>Close</Button>}>
          {probe.r ? <ProbeChecklist result={probe.r} /> : <p className="text-sm">Testing…</p>}
        </Modal>
      )}
    </div>
  )
}
