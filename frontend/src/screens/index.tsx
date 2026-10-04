import { EmptyState } from '../components/ui'
import type { Route } from '../lib/router'
import { Actions } from './Actions'
import { Profiles } from './Profiles'

export function Screen({ route }: { route: Route }) {
  switch (route.name) {
    case 'actions':
      return <Actions />
    case 'profiles':
      return <Profiles />
    default:
      return <EmptyState title="This screen is added in a later task." />
  }
}
