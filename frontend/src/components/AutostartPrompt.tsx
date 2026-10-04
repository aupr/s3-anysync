import { useEffect, useRef, useState } from 'react'
import { api } from '../lib/api'
import type { Settings } from '../lib/types'
import { useToast } from './toast'
import { Button, Modal } from './ui'

// AutostartPrompt asks once, after the first connection, whether the agent
// should start at login. The answer is stored by the agent (autostartOffered),
// which only records it after the login item was set up: a failed attempt is
// shown as a toast, closes the prompt, and the question comes back next start.
export function AutostartPrompt() {
  const toast = useToast()
  const [settings, setSettings] = useState<Settings | null>(null)
  const [busy, setBusy] = useState(false)
  const asked = useRef(false)
  const alive = useRef(true)
  const inFlight = useRef(false)
  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
    }
  }, [])
  useEffect(() => {
    if (asked.current) return
    asked.current = true
    api.settings().then((s) => {
      if (alive.current && s?.autostartSupported && !s.autostartOffered && !s.autostart) setSettings(s)
    }).catch(() => {}) // the Settings screen reports load errors
  }, [])
  if (!settings) return null
  const answer = async (enable: boolean) => {
    if (inFlight.current) return
    inFlight.current = true
    setBusy(true)
    try {
      await api.updateSettings({ ...settings, autostart: enable || settings.autostart, autostartOffered: true })
    } catch (e) {
      toast.error(e)
    } finally {
      inFlight.current = false
      if (alive.current) {
        setBusy(false)
        setSettings(null)
      }
    }
  }
  return (
    <Modal title="Start s3-anysync at login?" onClose={() => (inFlight.current ? undefined : setSettings(null))} footer={
      <>
        <Button disabled={busy} onClick={() => answer(false)}>Not now</Button>
        <Button variant="primary" disabled={busy} onClick={() => answer(true)}>Yes, start at login</Button>
      </>
    }>
      <p className="text-sm">The background agent keeps your settings in sync even when this window is closed. You can change this later in Settings.</p>
    </Modal>
  )
}
