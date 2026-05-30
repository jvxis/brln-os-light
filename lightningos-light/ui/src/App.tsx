import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import AuthScreen from './components/AuthScreen'
import Sidebar from './components/Sidebar'
import Topbar from './components/Topbar'
import Dashboard from './pages/Dashboard'
import Reports from './pages/Reports'
import Wizard from './pages/Wizard'
import Wallet from './pages/Wallet'
import NetworkAtlas from './pages/NetworkAtlas'
import GraphExplorer from './pages/GraphExplorer'
import LightningOps from './pages/LightningOps'
import FeeCenter from './pages/FeeCenter'
import ChannelRanking from './pages/ChannelRanking'
import ChannelOpenCandidates from './pages/ChannelOpenCandidates'
import RebalanceCenter from './pages/RebalanceCenter'
import OnchainHub from './pages/OnchainHub'
import Chat from './pages/Chat'
import Disks from './pages/Disks'
import Logs from './pages/Logs'
import BitcoinRemote from './pages/BitcoinRemote'
import BitcoinLocal from './pages/BitcoinLocal'
import Elements from './pages/Elements'
import Notifications from './pages/Notifications'
import AuditLog from './pages/AuditLog'
import LndConfig from './pages/LndConfig'
import LndInfo from './pages/LndInfo'
import AppStore from './pages/AppStore'
import Terminal from './pages/Terminal'
import BuyDepix from './pages/BuyDepix'
import Shortcuts from './pages/Shortcuts'
import PayBoleto from './pages/PayBoleto'
import NodeRetirement from './pages/NodeRetirement'
import { getAuthState, getBitcoinLocalStatus, getBoletoConfig, getDepixConfig, getLndStatus, getWizardStatus, logoutAuth, type AuthState } from './api'
import { defaultPalette, paletteOrder, resolvePalette, resolveTheme, type PaletteKey, type ThemeMode } from './theme'

const readHashRoute = () => {
  const rawHash = window.location.hash.startsWith('#')
    ? window.location.hash.slice(1)
    : window.location.hash
  if (!rawHash) return ''
  const queryIndex = rawHash.indexOf('?')
  return queryIndex >= 0 ? rawHash.slice(0, queryIndex) : rawHash
}

function useHashRoute() {
  const [hash, setHash] = useState(readHashRoute)

  useEffect(() => {
    const handler = () => setHash(readHashRoute())
    window.addEventListener('hashchange', handler)
    return () => window.removeEventListener('hashchange', handler)
  }, [])

  return hash
}

type RouteItem = {
  key: string
  label: string
  element: JSX.Element
}

type MenuConfig = {
  favorites: string[]
  hidden: string[]
}

const MENU_CONFIG_KEY = 'los-menu-config'

const readMenuConfig = (): MenuConfig | null => {
  try {
    const raw = window.localStorage.getItem(MENU_CONFIG_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw)
    if (!parsed || typeof parsed !== 'object') return null
    return {
      favorites: Array.isArray(parsed.favorites)
        ? parsed.favorites.filter((item: unknown) => typeof item === 'string')
        : [],
      hidden: Array.isArray(parsed.hidden)
        ? parsed.hidden.filter((item: unknown) => typeof item === 'string')
        : []
    }
  } catch {
    return null
  }
}

const uniqueKeys = (items: string[]) => {
  const seen = new Set<string>()
  const result: string[] = []
  for (const item of items) {
    if (seen.has(item)) continue
    seen.add(item)
    result.push(item)
  }
  return result
}

const normalizeMenuConfig = (config: MenuConfig | null, keys: string[]) => {
  const keySet = new Set(keys)
  const favoritesInput = config?.favorites ?? []
  const hiddenInput = config?.hidden ?? []
  const hidden = uniqueKeys(hiddenInput.filter((item) => keySet.has(item)))
  const hiddenSet = new Set(hidden)
  const favorites = uniqueKeys(favoritesInput.filter((item) => keySet.has(item) && !hiddenSet.has(item)))
  return { favorites, hidden }
}

const sameMenuConfig = (left: MenuConfig, right: MenuConfig) => {
  if (left.favorites.length !== right.favorites.length || left.hidden.length !== right.hidden.length) {
    return false
  }
  for (let index = 0; index < left.favorites.length; index += 1) {
    if (left.favorites[index] !== right.favorites[index]) return false
  }
  for (let index = 0; index < left.hidden.length; index += 1) {
    if (left.hidden[index] !== right.hidden[index]) return false
  }
  return true
}

const applyMenuConfig = (routes: RouteItem[], config: MenuConfig) => {
  const hiddenSet = new Set(config.hidden)
  const favoriteSet = new Set(config.favorites)
  const routeMap = new Map(routes.map((route) => [route.key, route]))
  const favorites = config.favorites
    .map((key) => routeMap.get(key))
    .filter((route): route is RouteItem => {
      if (!route) return false
      return !hiddenSet.has(route.key)
    })
  const rest = routes.filter((route) => !favoriteSet.has(route.key) && !hiddenSet.has(route.key))
  return [...favorites, ...rest]
}

export default function App() {
  const { t, i18n } = useTranslation()
  const route = useHashRoute()
  const [theme, setTheme] = useState<ThemeMode>(() => resolveTheme(window.localStorage.getItem('los-theme')))
  const [palette, setPalette] = useState<PaletteKey>(() => resolvePalette(window.localStorage.getItem('los-palette')))
  const [authState, setAuthState] = useState<AuthState | null>(null)
  const [authLoading, setAuthLoading] = useState(true)
  const [authError, setAuthError] = useState('')
  const [walletUnlocked, setWalletUnlocked] = useState<boolean | null>(null)
  const [walletExists, setWalletExists] = useState<boolean | null>(null)
  const [depixEnabled, setDepixEnabled] = useState(false)
  const [boletoEnabled, setBoletoEnabled] = useState(false)
  const [externalBitcoinDetected, setExternalBitcoinDetected] = useState(false)
  const [menuOpen, setMenuOpen] = useState(false)
  const refreshAuthState = useCallback(async () => {
    try {
      const state = await getAuthState()
      setAuthError('')
      setAuthState(state)
    } catch (err: any) {
      setAuthError(err?.message || 'Failed to load admin access state')
    } finally {
      setAuthLoading(false)
    }
  }, [])
  const refreshDepixEnabled = useCallback(async () => {
    try {
      const data: any = await getDepixConfig()
      setDepixEnabled(Boolean(data?.enabled))
    } catch {
      setDepixEnabled(false)
    }
  }, [])
  const refreshBoletoEnabled = useCallback(async () => {
    try {
      const data: any = await getBoletoConfig()
      setBoletoEnabled(Boolean(data?.enabled))
    } catch {
      setBoletoEnabled(false)
    }
  }, [])
  const refreshExternalBitcoinDetected = useCallback(async () => {
    try {
      const data: any = await getBitcoinLocalStatus()
      setExternalBitcoinDetected(data?.source === 'external')
    } catch {
      // keep previous state on transient failures
    }
  }, [])
  const baseRoutes = useMemo(() => {
    const depixRoute = depixEnabled
      ? [{ key: 'buy-depix', label: t('nav.buyDepix'), element: <BuyDepix /> }]
      : []
    const boletoRoute = boletoEnabled
      ? [{ key: 'pay-boleto', label: t('nav.payBoleto'), element: <PayBoleto /> }]
      : []
    return [
      { key: 'dashboard', label: t('nav.dashboard'), element: <Dashboard authState={authState} /> },
      { key: 'reports', label: t('nav.reports'), element: <Reports /> },
      { key: 'wallet', label: t('nav.wallet'), element: <Wallet /> },
      { key: 'network-atlas', label: t('nav.networkAtlas'), element: <NetworkAtlas /> },
      { key: 'graph-explorer', label: t('nav.graphExplorer'), element: <GraphExplorer /> },
      { key: 'lightning-ops', label: t('nav.lightningOps'), element: <LightningOps /> },
      { key: 'fee-center', label: t('nav.feeCenter'), element: <FeeCenter /> },
      { key: 'channel-ranking', label: t('nav.channelRanking'), element: <ChannelRanking /> },
      { key: 'new-channels', label: t('nav.newChannels'), element: <ChannelOpenCandidates /> },
      { key: 'rebalance-center', label: t('nav.rebalanceCenter'), element: <RebalanceCenter /> },
      { key: 'onchain-hub', label: t('nav.onchainHub'), element: <OnchainHub /> },
      { key: 'chat', label: t('nav.chat'), element: <Chat /> },
      {
        key: 'lnd',
        label: externalBitcoinDetected ? t('nav.lndInfo') : t('nav.lndConfig'),
        element: externalBitcoinDetected ? <LndInfo /> : <LndConfig />
      },
      { key: 'apps', label: t('nav.apps'), element: <AppStore /> },
      ...depixRoute,
      ...boletoRoute,
      { key: 'bitcoin', label: t('nav.bitcoinRemote'), element: <BitcoinRemote /> },
      { key: 'bitcoin-local', label: t('nav.bitcoinLocal'), element: <BitcoinLocal /> },
      { key: 'elements', label: t('nav.elements'), element: <Elements /> },
      { key: 'notifications', label: t('nav.notifications'), element: <Notifications /> },
      { key: 'audit-log', label: t('nav.auditLog'), element: <AuditLog /> },
      { key: 'disks', label: t('nav.disks'), element: <Disks /> },
      { key: 'terminal', label: t('nav.terminal'), element: <Terminal /> },
      { key: 'shortcuts', label: t('nav.shortcuts'), element: <Shortcuts /> },
      { key: 'logs', label: t('nav.logs'), element: <Logs /> },
      { key: 'node-retirement', label: t('nav.nodeRetirement'), element: <NodeRetirement /> }
    ]
  }, [authState, depixEnabled, boletoEnabled, externalBitcoinDetected, i18n.language, t])
  const baseRouteKeys = useMemo(() => baseRoutes.map((item) => item.key), [baseRoutes])
  const [menuConfig, setMenuConfig] = useState<MenuConfig>(() => normalizeMenuConfig(readMenuConfig(), baseRouteKeys))

  useEffect(() => {
    document.documentElement.setAttribute('data-theme', theme)
    window.localStorage.setItem('los-theme', theme)
  }, [theme])

  useEffect(() => {
    document.documentElement.setAttribute('data-palette', palette)
    window.localStorage.setItem('los-palette', palette)
  }, [palette])

  useEffect(() => {
    void refreshAuthState()
    const handleAuthRequired = () => {
      void refreshAuthState()
    }
    window.addEventListener('auth:required', handleAuthRequired as EventListener)
    return () => {
      window.removeEventListener('auth:required', handleAuthRequired as EventListener)
    }
  }, [refreshAuthState])

  const authReady = !authLoading && (authState?.enabled !== true || authState?.authenticated === true)

  useEffect(() => {
    if (!authReady) {
      setWalletUnlocked(null)
      setWalletExists(null)
      return
    }

    let active = true
    const load = async () => {
      try {
        const data: any = await getWizardStatus()
        if (!active) return
        setWalletExists(Boolean(data?.wallet_exists))
      } catch {
        if (!active) return
      }
      try {
        const status: any = await getLndStatus()
        if (!active) return
        if (typeof status?.wallet_state === 'string') {
          setWalletUnlocked(status.wallet_state === 'unlocked')
        }
      } catch {
        if (!active) return
      }
    }
    load()
    const timer = window.setInterval(load, 30000)
    return () => {
      active = false
      window.clearInterval(timer)
    }
  }, [authReady])

  useEffect(() => {
    if (!authReady) {
      setDepixEnabled(false)
      setBoletoEnabled(false)
      setExternalBitcoinDetected(false)
      return
    }

    const handleAppsChanged = (event: Event) => {
      void refreshExternalBitcoinDetected()
      const detail = (event as CustomEvent<{ id?: string }>).detail
      if (detail?.id === 'depixbuy') {
        void refreshDepixEnabled()
        return
      }
      if (detail?.id === 'fswap') {
        void refreshBoletoEnabled()
      }
    }
    void refreshDepixEnabled()
    void refreshBoletoEnabled()
    void refreshExternalBitcoinDetected()
    const timer = window.setInterval(refreshDepixEnabled, 30000)
    const boletoTimer = window.setInterval(refreshBoletoEnabled, 30000)
    const externalBitcoinTimer = window.setInterval(refreshExternalBitcoinDetected, 300000)
    window.addEventListener('apps:changed', handleAppsChanged as EventListener)
    return () => {
      window.clearInterval(timer)
      window.clearInterval(boletoTimer)
      window.clearInterval(externalBitcoinTimer)
      window.removeEventListener('apps:changed', handleAppsChanged as EventListener)
    }
  }, [authReady, refreshDepixEnabled, refreshBoletoEnabled, refreshExternalBitcoinDetected])

  useEffect(() => {
    setMenuConfig((current) => {
      const normalized = normalizeMenuConfig(current, baseRouteKeys)
      return sameMenuConfig(current, normalized) ? current : normalized
    })
  }, [baseRouteKeys])

  useEffect(() => {
    try {
      window.localStorage.setItem(MENU_CONFIG_KEY, JSON.stringify(menuConfig))
    } catch {
      // ignore storage errors
    }
  }, [menuConfig])

  const wizardHidden = walletUnlocked === true
  const wizardRequired = walletExists === false && !wizardHidden

  const wizardRoute = useMemo(
    () => ({ key: 'wizard', label: t('nav.wizard'), element: <Wizard /> }),
    [t]
  )
  const menuRoutes = useMemo(() => applyMenuConfig(baseRoutes, menuConfig), [baseRoutes, menuConfig])
  const sidebarRoutes = useMemo(
    () => (wizardHidden ? menuRoutes : [wizardRoute, ...menuRoutes]),
    [menuRoutes, wizardHidden, wizardRoute]
  )
  const allRoutes = useMemo(
    () => (wizardHidden ? baseRoutes : [wizardRoute, ...baseRoutes]),
    [baseRoutes, wizardHidden, wizardRoute]
  )

  useEffect(() => {
    setMenuOpen(false)
  }, [route])

  useEffect(() => {
    document.body.style.overflow = menuOpen ? 'hidden' : ''
    if (!menuOpen) {
      return
    }
    const handleKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setMenuOpen(false)
      }
    }
    window.addEventListener('keydown', handleKey)
    return () => {
      window.removeEventListener('keydown', handleKey)
      document.body.style.overflow = ''
    }
  }, [menuOpen])

  const current = useMemo(() => {
    const matched = allRoutes.find((item) => item.key === route)
    if (wizardRequired) {
      return allRoutes.find((item) => item.key === 'wizard') || matched || allRoutes[0]
    }
    if (matched) {
      return matched
    }
    return allRoutes.find((item) => item.key === 'dashboard') || allRoutes[0]
  }, [allRoutes, route, wizardRequired])

  const handlePaletteToggle = () => {
    setPalette((current) => {
      const index = paletteOrder.indexOf(current)
      if (index === -1) {
        return defaultPalette
      }
      return paletteOrder[(index + 1) % paletteOrder.length]
    })
  }

  const handleLogout = useCallback(async () => {
    try {
      await logoutAuth()
    } finally {
      await refreshAuthState()
    }
    setMenuOpen(false)
  }, [refreshAuthState])

  if (authLoading || authState == null) {
    return (
      <div className="min-h-screen px-6 py-10 lg:px-12">
        <div className="mx-auto max-w-3xl section-card">
          <p className="text-sm uppercase tracking-[0.3em] text-fog/50">{t('auth.kicker')}</p>
          <h1 className="mt-3 text-3xl font-semibold">{t('auth.loadingTitle')}</h1>
          <p className="mt-3 text-fog/65">{t('auth.loadingBody')}</p>
          {!authLoading && authError && (
            <p className="mt-4 text-sm text-brass">{authError}</p>
          )}
        </div>
      </div>
    )
  }

  if (authState.enabled && !authState.authenticated) {
    return <AuthScreen state={authState} onAuthenticated={setAuthState} />
  }

  return (
    <>
      <div
        className={`fixed inset-0 z-30 bg-black/60 backdrop-blur-sm transition-opacity lg:hidden ${
          menuOpen ? 'opacity-100' : 'opacity-0 pointer-events-none'
        }`}
        onClick={() => setMenuOpen(false)}
        aria-hidden="true"
      />
      <div className="min-h-screen flex flex-col lg:flex-row text-fog">
        <Sidebar
          routes={sidebarRoutes}
          allRoutes={baseRoutes}
          menuConfig={menuConfig}
          onMenuConfigChange={(next) => setMenuConfig(normalizeMenuConfig(next, baseRouteKeys))}
          current={current.key}
          open={menuOpen}
          onClose={() => setMenuOpen(false)}
        />
        <div className="flex-1 flex flex-col">
          <Topbar
            onMenuToggle={() => setMenuOpen((prev) => !prev)}
            menuOpen={menuOpen}
            theme={theme}
            palette={palette}
            onThemeToggle={() => setTheme((prev) => (prev === 'dark' ? 'light' : 'dark'))}
            onPaletteToggle={handlePaletteToggle}
            authState={authState}
            onAuthUpdated={setAuthState}
            onAuthRefresh={refreshAuthState}
            onLogout={authState.enabled ? handleLogout : undefined}
          />
          <main className="px-6 pb-16 pt-6 lg:px-12">
            {current.element}
          </main>
        </div>
      </div>
    </>
  )
}
