import { useCallback, useEffect, useRef, useState } from 'react'

export interface Loaded<T> {
  data: T | null
  // The message of the latest failed load, '' while there is none.
  error: string
  reload: () => void
}

// useLoad runs fetch on mount, whenever a value in deps changes, and on
// reload(). Only the newest request may apply its result: an older response
// arriving later, or one arriving after unmount, is dropped. A failure keeps
// the previous data and sets error.
export function useLoad<T>(fetch: () => Promise<T>, describe: (e: unknown) => string, deps: readonly unknown[]): Loaded<T> {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState('')
  const gen = useRef(0)
  const latest = useRef({ fetch, describe })
  latest.current = { fetch, describe }
  const alive = useRef(true)
  useEffect(() => {
    alive.current = true
    return () => { alive.current = false }
  }, [])
  const load = useCallback(() => {
    const mine = ++gen.current
    latest.current.fetch().then(
      (v) => {
        if (!alive.current || mine !== gen.current) return
        setData(v)
        setError('')
      },
      (e: unknown) => {
        if (!alive.current || mine !== gen.current) return
        setError(latest.current.describe(e))
      },
    )
  }, [])
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(load, [load, ...deps])
  return { data, error, reload: load }
}
