import { EmptyState } from '../components/ui'
import type { Route } from '../lib/router'
import { Profiles } from './Profiles'

export function Screen({ route }: { route: Route }) {
  switch (route.name) {
    case 'profiles':
      return <Profiles />
    default:
      return <EmptyState title="This screen is added in a later task." />
  }
}
