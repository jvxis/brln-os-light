import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import AuthScreen from './components/AuthScreen'
import Sidebar from './components/Sidebar'
import Topbar from './components/Topbar'
import ReportsReconciliationNotice from './components/ReportsReconciliationNotice'
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
import AutomationInterlock from './pages/AutomationInterlock'
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
import AppStore from './pages/AppStore'
import Terminal from './pages/Terminal'
import Shortcuts from './pages/Shortcuts'
import PayBoleto from './pages/PayBoleto'
import NodeRetirement from './pages/NodeRetirement'
import TaprootAssets from './pages/TaprootAssets'
import LightningLoop from './pages/LightningLoop'
import LoopOutBRLN from './pages/LoopOutBRLN'
import MagmaSales from './pages/MagmaSales'
import {
  getAuthState,
  getBitcoinLocalStatus,
  getBitcoinSource,
  getBoletoConfig,
  getApps,
  getLndStatus,
  getMenuPreferences,
  getWizardStatus,
  logoutAuth,
  updateMenuPreferences,
  type AuthState
} from './api'
import {
  appearanceStorageKeys,
  isDarkOnlyTheme,
  resolveStoredAppearance,
  type PaletteKey,
  type ThemeMode,
  type VisualThemeKey
} from './theme'

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
  group?: MenuGroupKey
}

type MenuGroupKey = 'lightning' | 'network' | 'apps' | 'node' | 'system'

type MenuConfig = {
  favorites: string[]
  hidden: string[]
}

const MENU_CONFIG_KEY = 'los-menu-config'
const MENU_CONFIG_VERSION = 1
const OPTIONAL_MENU_ROUTE_KEYS = ['pay-boleto', 'taproot-assets', 'lightning-loop', 'loop-out-brln', 'magma-sales']

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
  const [initialAppearance] = useState(() => resolveStoredAppearance(window.localStorage))
  const [visualTheme, setVisualTheme] = useState<VisualThemeKey>(initialAppearance.visualTheme)
  const [palette, setPalette] = useState<PaletteKey>(initialAppearance.palette)
  const [theme, setTheme] = useState<ThemeMode>(initialAppearance.mode)
  const [authState, setAuthState] = useState<AuthState | null>(null)
  const [authLoading, setAuthLoading] = useState(true)
  const [authError, setAuthError] = useState('')
  const [walletUnlocked, setWalletUnlocked] = useState<boolean | null>(null)
  const [walletExists, setWalletExists] = useState<boolean | null>(null)
  const [boletoEnabled, setBoletoEnabled] = useState(false)
  const [installedAppIDs, setInstalledAppIDs] = useState<Set<string>>(() => new Set())
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
  const refreshBoletoEnabled = useCallback(async () => {
    try {
      const data: any = await getBoletoConfig()
      setBoletoEnabled(Boolean(data?.enabled))
    } catch {
      setBoletoEnabled(false)
    }
  }, [])
  const refreshInstalledApps = useCallback(async () => {
    try {
      const apps: Array<{ id?: string; installed?: boolean }> = await getApps()
      setInstalledAppIDs(new Set(
        (Array.isArray(apps) ? apps : [])
          .filter((app) => app?.installed && typeof app.id === 'string')
          .map((app) => app.id as string)
      ))
    } catch {
      // Keep the previous menu on transient App Store failures.
    }
  }, [])
  const refreshExternalBitcoinDetected = useCallback(async () => {
    try {
      const [localStatus, sourceStatus]: any[] = await Promise.all([
        getBitcoinLocalStatus(),
        getBitcoinSource()
      ])
      setExternalBitcoinDetected(sourceStatus?.source === 'local' && localStatus?.source === 'external')
    } catch {
      // keep previous state on transient failures
    }
  }, [])
  const baseRoutes = useMemo(() => {
    const boletoRoute = boletoEnabled
      ? [{ key: 'pay-boleto', label: t('nav.payBoleto'), element: <PayBoleto />, group: 'apps' as const }]
      : []
    const installedAppRoutes = [
      ...(installedAppIDs.has('tapd')
        ? [{ key: 'taproot-assets', label: t('nav.taprootAssets'), element: <TaprootAssets />, group: 'apps' as const }]
        : []),
      ...(installedAppIDs.has('loop')
        ? [{ key: 'lightning-loop', label: t('nav.lightningLoop'), element: <LightningLoop />, group: 'apps' as const }]
        : []),
      ...(installedAppIDs.has('loopout-brln')
        ? [{ key: 'loop-out-brln', label: t('nav.loopOutBrln'), element: <LoopOutBRLN />, group: 'apps' as const }]
        : []),
      ...(installedAppIDs.has('magma-sales')
        ? [{ key: 'magma-sales', label: t('nav.magmaSales'), element: <MagmaSales />, group: 'apps' as const }]
        : [])
    ]
    return [
      { key: 'dashboard', label: t('nav.dashboard'), element: <Dashboard authState={authState} /> },
      { key: 'reports', label: t('nav.reports'), element: <Reports />, group: 'network' as const },
      { key: 'wallet', label: t('nav.wallet'), element: <Wallet /> },
      { key: 'network-atlas', label: t('nav.networkAtlas'), element: <NetworkAtlas />, group: 'network' as const },
      { key: 'graph-explorer', label: t('nav.graphExplorer'), element: <GraphExplorer />, group: 'network' as const },
      { key: 'lightning-ops', label: t('nav.lightningOps'), element: <LightningOps />, group: 'lightning' as const },
      { key: 'fee-center', label: t('nav.feeCenter'), element: <FeeCenter />, group: 'lightning' as const },
      { key: 'rebalance-center', label: t('nav.rebalanceCenter'), element: <RebalanceCenter />, group: 'lightning' as const },
      { key: 'automation-interlock', label: t('nav.automationInterlock'), element: <AutomationInterlock />, group: 'lightning' as const },
      { key: 'channel-ranking', label: t('nav.channelRanking'), element: <ChannelRanking />, group: 'lightning' as const },
      { key: 'new-channels', label: t('nav.newChannels'), element: <ChannelOpenCandidates />, group: 'lightning' as const },
      { key: 'onchain-hub', label: t('nav.onchainHub'), element: <OnchainHub />, group: 'network' as const },
      { key: 'chat', label: t('nav.chat'), element: <Chat /> },
      {
        key: 'lnd',
        label: t('nav.lndConfig'),
        element: <LndConfig externalBitcoinDetected={externalBitcoinDetected} />,
        group: 'node' as const
      },
      { key: 'apps', label: t('nav.apps'), element: <AppStore />, group: 'apps' as const },
      ...boletoRoute,
      { key: 'bitcoin', label: t('nav.bitcoinRemote'), element: <BitcoinRemote />, group: 'node' as const },
      { key: 'bitcoin-local', label: t('nav.bitcoinLocal'), element: <BitcoinLocal />, group: 'node' as const },
      { key: 'elements', label: t('nav.elements'), element: <Elements />, group: 'node' as const },
      ...installedAppRoutes,
      { key: 'notifications', label: t('nav.notifications'), element: <Notifications />, group: 'system' as const },
      { key: 'audit-log', label: t('nav.auditLog'), element: <AuditLog />, group: 'system' as const },
      { key: 'disks', label: t('nav.disks'), element: <Disks />, group: 'system' as const },
      { key: 'terminal', label: t('nav.terminal'), element: <Terminal />, group: 'system' as const },
      { key: 'shortcuts', label: t('nav.shortcuts'), element: <Shortcuts />, group: 'system' as const },
      { key: 'logs', label: t('nav.logs'), element: <Logs />, group: 'system' as const },
      { key: 'node-retirement', label: t('nav.nodeRetirement'), element: <NodeRetirement />, group: 'system' as const }
    ]
  }, [authState, boletoEnabled, externalBitcoinDetected, i18n.language, installedAppIDs, t])
  const baseRouteKeys = useMemo(() => baseRoutes.map((item) => item.key), [baseRoutes])
  const menuPreferenceKeysRef = useRef<string[]>([])
  if (menuPreferenceKeysRef.current.length === 0) {
    menuPreferenceKeysRef.current = uniqueKeys([...baseRouteKeys, ...OPTIONAL_MENU_ROUTE_KEYS])
  }
  const [initialLocalMenuConfig] = useState<MenuConfig | null>(() => readMenuConfig())
  const [menuConfig, setMenuConfig] = useState<MenuConfig>(() =>
    normalizeMenuConfig(initialLocalMenuConfig, menuPreferenceKeysRef.current)
  )
  const [menuSyncState, setMenuSyncState] = useState<'syncing' | 'synced' | 'local'>('syncing')
  const menuPreferencesLoadedRef = useRef(false)
  const menuEditRevisionRef = useRef(0)
  const menuSaveRequestRef = useRef(0)

  useEffect(() => {
    document.documentElement.setAttribute('data-theme', theme)
    window.localStorage.setItem(appearanceStorageKeys.mode, theme)
  }, [theme])

  useEffect(() => {
    document.documentElement.setAttribute('data-palette', palette)
    window.localStorage.setItem(appearanceStorageKeys.palette, palette)
  }, [palette])

  useEffect(() => {
    document.documentElement.setAttribute('data-visual-theme', visualTheme)
    window.localStorage.setItem(appearanceStorageKeys.visualTheme, visualTheme)
    if (isDarkOnlyTheme(visualTheme)) {
      setTheme('dark')
    }
  }, [visualTheme])

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
      menuPreferencesLoadedRef.current = false
      return
    }
    if (menuPreferencesLoadedRef.current) {
      return
    }
    menuPreferencesLoadedRef.current = true

    let active = true
    const startingRevision = menuEditRevisionRef.current
    const loadMenuPreferences = async () => {
      setMenuSyncState('syncing')
      try {
        const remote = await getMenuPreferences()
        if (!active || menuEditRevisionRef.current !== startingRevision) return
        if (remote.exists) {
          const normalized = normalizeMenuConfig(remote, menuPreferenceKeysRef.current)
          setMenuConfig(normalized)
          setMenuSyncState('synced')
          return
        }

        const initial = normalizeMenuConfig(initialLocalMenuConfig, menuPreferenceKeysRef.current)
        await updateMenuPreferences({
          version: MENU_CONFIG_VERSION,
          favorites: initial.favorites,
          hidden: initial.hidden
        })
        if (!active || menuEditRevisionRef.current !== startingRevision) return
        setMenuConfig(initial)
        setMenuSyncState('synced')
      } catch {
        if (!active || menuEditRevisionRef.current !== startingRevision) return
        setMenuSyncState('local')
      }
    }
    void loadMenuPreferences()
    return () => {
      active = false
    }
  }, [authReady, initialLocalMenuConfig])

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
          if (status.wallet_state === 'unlocked') {
            setWalletUnlocked(true)
          } else if (status.wallet_state === 'locked') {
            setWalletUnlocked(false)
          }
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
      setBoletoEnabled(false)
      setInstalledAppIDs(new Set())
      setExternalBitcoinDetected(false)
      return
    }

    const handleAppsChanged = (event: Event) => {
      void refreshInstalledApps()
      void refreshExternalBitcoinDetected()
      const detail = (event as CustomEvent<{ id?: string }>).detail
      if (detail?.id === 'fswap') {
        void refreshBoletoEnabled()
      }
    }
    void refreshBoletoEnabled()
    void refreshInstalledApps()
    void refreshExternalBitcoinDetected()
    const boletoTimer = window.setInterval(refreshBoletoEnabled, 30000)
    const installedAppsTimer = window.setInterval(refreshInstalledApps, 30000)
    const externalBitcoinTimer = window.setInterval(refreshExternalBitcoinDetected, 300000)
    window.addEventListener('apps:changed', handleAppsChanged as EventListener)
    return () => {
      window.clearInterval(boletoTimer)
      window.clearInterval(installedAppsTimer)
      window.clearInterval(externalBitcoinTimer)
      window.removeEventListener('apps:changed', handleAppsChanged as EventListener)
    }
  }, [authReady, refreshBoletoEnabled, refreshExternalBitcoinDetected, refreshInstalledApps])

  useEffect(() => {
    setMenuConfig((current) => {
      const normalized = normalizeMenuConfig(current, menuPreferenceKeysRef.current)
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

  // Wallet existence is durable setup state. A temporarily busy/unreachable
  // GetInfo call must not make a configured node look like a fresh install.
  const wizardHidden = walletExists === true || walletUnlocked === true
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

  const handleLogout = useCallback(async () => {
    try {
      await logoutAuth()
    } finally {
      await refreshAuthState()
    }
    setMenuOpen(false)
  }, [refreshAuthState])

  const handleMenuConfigChange = useCallback((next: MenuConfig) => {
    const normalized = normalizeMenuConfig(next, menuPreferenceKeysRef.current)
    menuEditRevisionRef.current += 1
    menuSaveRequestRef.current += 1
    const requestID = menuSaveRequestRef.current
    setMenuConfig(normalized)
    setMenuSyncState('syncing')
    void updateMenuPreferences({
      version: MENU_CONFIG_VERSION,
      favorites: normalized.favorites,
      hidden: normalized.hidden
    })
      .then(() => {
        if (menuSaveRequestRef.current === requestID) {
          setMenuSyncState('synced')
        }
      })
      .catch(() => {
        if (menuSaveRequestRef.current === requestID) {
          setMenuSyncState('local')
        }
      })
  }, [])

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
      <div className="app-shell min-h-screen flex flex-col lg:flex-row text-fog">
        <div
          className={`fixed inset-0 z-30 bg-black/60 backdrop-blur-sm transition-opacity lg:hidden ${
            menuOpen ? 'opacity-100' : 'opacity-0 pointer-events-none'
          }`}
          onClick={() => setMenuOpen(false)}
          aria-hidden="true"
        />
        <Sidebar
          routes={sidebarRoutes}
          allRoutes={baseRoutes}
          menuConfig={menuConfig}
          onMenuConfigChange={handleMenuConfigChange}
          syncState={menuSyncState}
          current={current.key}
          open={menuOpen}
          onClose={() => setMenuOpen(false)}
        />
        <div className="app-workspace flex-1 flex flex-col min-w-0">
          <Topbar
            onMenuToggle={() => setMenuOpen((prev) => !prev)}
            menuOpen={menuOpen}
            theme={theme}
            visualTheme={visualTheme}
            palette={palette}
            onAppearanceApply={(next) => {
              setVisualTheme(next.visualTheme)
              setPalette(next.palette)
              setTheme(isDarkOnlyTheme(next.visualTheme) ? 'dark' : next.mode)
            }}
            authState={authState}
            onAuthUpdated={setAuthState}
            onAuthRefresh={refreshAuthState}
            onLogout={authState.enabled ? handleLogout : undefined}
          />
          <main className="app-content px-6 pb-16 pt-6 lg:px-12">
            <ReportsReconciliationNotice />
            {current.element}
          </main>
        </div>
      </div>
    </>
  )
}
