import { Shell } from './components/Shell'
import { ToastProvider } from './components/toast'
import { RouterProvider, useRouter } from './lib/router'
import { StoreProvider, useStore } from './lib/store'
import { AgentDown } from './screens/AgentDown'
import { Screen } from './screens'

function Main() {
  const { state } = useStore()
  const { route } = useRouter()
  // Before the first successful connection there is nothing to show.
  if (!state.connection.connected && !state.status) return <AgentDown />
  return (
    <Shell>
      <Screen route={route} />
    </Shell>
  )
}

export default function App() {
  return (
    <ToastProvider>
      <StoreProvider>
        <RouterProvider>
          <Main />
        </RouterProvider>
      </StoreProvider>
    </ToastProvider>
  )
}
