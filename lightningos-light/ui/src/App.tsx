import { useEffect, useMemo, useState } from 'react'
import Sidebar from './components/Sidebar'
import Topbar from './components/Topbar'
import Dashboard from './pages/Dashboard'
import Wizard from './pages/Wizard'
import Wallet from './pages/Wallet'
import LightningOps from './pages/LightningOps'
import Disks from './pages/Disks'
import Logs from './pages/Logs'
import BitcoinRemote from './pages/BitcoinRemote'
import BitcoinLocal from './pages/BitcoinLocal'
import Notifications from './pages/Notifications'
import LndConfig from './pages/LndConfig'
import AppStore from './pages/AppStore'

const routes = [
  { key: 'dashboard', label: 'Dashboard', element: <Dashboard /> },
  { key: 'wizard', label: 'Wizard', element: <Wizard /> },
  { key: 'wallet', label: 'Wallet', element: <Wallet /> },
  { key: 'lightning-ops', label: 'Lightning Ops', element: <LightningOps /> },
  { key: 'bitcoin', label: 'Bitcoin Remote', element: <BitcoinRemote /> },
  { key: 'lnd', label: 'LND Config', element: <LndConfig /> },
  { key: 'disks', label: 'Disks', element: <Disks /> },
  { key: 'logs', label: 'Logs', element: <Logs /> },
  { key: 'apps', label: 'Apps', element: <AppStore /> },
  { key: 'bitcoin-local', label: 'Bitcoin Local', element: <BitcoinLocal /> },
  { key: 'notifications', label: 'Notifications', element: <Notifications /> }
]

function useHashRoute() {
  const [hash, setHash] = useState(window.location.hash.replace('#', '') || 'wizard')

  useEffect(() => {
    const handler = () => setHash(window.location.hash.replace('#', '') || 'wizard')
    window.addEventListener('hashchange', handler)
    return () => window.removeEventListener('hashchange', handler)
  }, [])

  return hash
}

export default function App() {
  const route = useHashRoute()
  const current = useMemo(() => routes.find((item) => item.key === route) || routes[0], [route])

  return (
    <div className="min-h-screen flex flex-col lg:flex-row text-fog">
      <Sidebar routes={routes} current={current.key} />
      <div className="flex-1 flex flex-col">
        <Topbar />
        <main className="px-6 pb-16 pt-6 lg:px-12">
          {current.element}
        </main>
      </div>
    </div>
  )
}
