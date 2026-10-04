import { EmptyState } from '../components/ui'
import type { Route } from '../lib/router'

export function Screen({ route }: { route: Route }) {
  switch (route.name) {
    default:
      return <EmptyState title="This screen is added in a later task." />
  }
}
