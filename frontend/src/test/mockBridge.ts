import { vi } from 'vitest'

type Fn = ReturnType<typeof vi.fn>
const fns: Record<string, Fn> = {}
// Any binding is a vi.fn resolving undefined until a test sets it.
export const bindings: Record<string, Fn> = new Proxy(fns, {
  get(target, name: string) {
    if (!(name in target)) target[name] = vi.fn(async () => undefined)
    return target[name]
  },
})

const handlers = new Map<string, Set<(d: any) => void>>()

export function onEvent(name: string, cb: (d: any) => void): () => void {
  if (!handlers.has(name)) handlers.set(name, new Set())
  handlers.get(name)!.add(cb)
  return () => handlers.get(name)?.delete(cb)
}

export function emit(name: string, data: unknown) {
  handlers.get(name)?.forEach((h) => h(data))
}

export function resetBridge() {
  handlers.clear()
  for (const k of Object.keys(fns)) delete fns[k]
}
