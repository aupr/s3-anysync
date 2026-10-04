import * as App from '../../wailsjs/go/gui/App'
import { EventsOn } from '../../wailsjs/runtime/runtime'

export const bindings: Record<string, (...args: unknown[]) => Promise<unknown>> =
  App as unknown as Record<string, (...args: unknown[]) => Promise<unknown>>

// onEvent subscribes to a Go event; it returns the unsubscribe function.
export function onEvent(name: string, cb: (data: any) => void): () => void {
  return EventsOn(name, cb)
}
