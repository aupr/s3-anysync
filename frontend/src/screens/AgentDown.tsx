import { useEffect, useState } from 'react'
import { Button, Spinner } from '../components/ui'
import { api } from '../lib/api'
import { useStore } from '../lib/store'

export function AgentDown() {
  const { state } = useStore()
  const [dir, setDir] = useState('')
  useEffect(() => {
    api.dataDir().then(setDir).catch(() => {})
  }, [])
  const reason = state.connection.reason ?? ''
  const connecting = reason.startsWith('connecting')
  return (
    <div className="flex h-full items-center justify-center p-8">
      <div className="max-w-lg space-y-4 text-center">
        <h1 className="text-xl font-semibold">{connecting ? 'Connecting to the agent…' : 'The s3-anysync agent is not running'}</h1>
        {connecting ? <Spinner /> : <p className="text-sm text-zinc-600 dark:text-zinc-400">{reason}</p>}
        {!connecting && (
          <>
            <Button variant="primary" onClick={() => api.retryConnect()}>Start the agent and retry</Button>
            <p className="text-xs text-zinc-500">
              Data folder: {dir || '…'}. The GUI starts <code>s3-anysync-agent</code> from its own folder, or from the path in
              <code> S3_ANYSYNC_AGENT</code>.
            </p>
          </>
        )}
      </div>
    </div>
  )
}
