import type { Route } from '../lib/router'
import { ActionDetail } from './ActionDetail'
import { Actions } from './Actions'
import { Activity } from './Activity'
import { Conflicts } from './Conflicts'
import { DeleteReview } from './DeleteReview'
import { Profiles } from './Profiles'
import { SettingsScreen } from './Settings'

export function Screen({ route }: { route: Route }) {
  switch (route.name) {
    case 'actions':
      return <Actions />
    case 'action':
      return <ActionDetail id={route.id} tab={route.tab} />
    case 'conflicts':
      return <Conflicts key={route.id} id={route.id} />
    case 'deletes':
      return <DeleteReview key={route.id} id={route.id} />
    case 'profiles':
      return <Profiles />
    case 'activity':
      return <Activity />
    case 'settings':
      return <SettingsScreen />
    default:
      return <Actions />
  }
}
