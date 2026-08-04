import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getHealth, getLndConfig, getLndStatus, type AuthState } from '../api'
import { setLanguage } from '../i18n'
import { type Appearance, type PaletteKey, type ThemeMode, type VisualThemeKey } from '../theme'
import AccountSecurityModal from './AccountSecurityModal'
import AppearanceMenu from './AppearanceMenu'
import StatusBadge from './dashboard/StatusBadge'

const BITCOIN_HEADER_EVENTS = [
  {
    key: 'genesis-block',
    month: 0,
    day: 3,
    emoji: '🧱',
    tooltipKey: 'topbar.bitcoinEvents.genesisBlock',
    burst: ['0', '₿', '03']
  },
  {
    key: 'first-client',
    month: 0,
    day: 9,
    emoji: '💾',
    tooltipKey: 'topbar.bitcoinEvents.firstClient',
    burst: ['v0.1', '⚡', '09']
  },
  {
    key: 'first-transaction',
    month: 0,
    day: 12,
    emoji: '🤝',
    tooltipKey: 'topbar.bitcoinEvents.firstTransaction',
    burst: ['10', '₿', '12']
  },
  {
    key: 'lightning-paper',
    month: 0,
    day: 14,
    emoji: '⚡',
    tooltipKey: 'topbar.bitcoinEvents.lightningPaper',
    burst: ['LN', 'P2P', '16']
  },
  {
    key: 'p2p-foundation',
    month: 1,
    day: 11,
    emoji: '🌐',
    tooltipKey: 'topbar.bitcoinEvents.p2pFoundation',
    burst: ['P2P', '₿', '09']
  },
  {
    key: 'lnd-mainnet-beta',
    month: 2,
    day: 15,
    emoji: '⚡',
    tooltipKey: 'topbar.bitcoinEvents.lndMainnetBeta',
    burst: ['lnd', 'β', '18']
  },
  {
    key: 'satoshi-farewell',
    month: 3,
    day: 26,
    emoji: '🕶️',
    tooltipKey: 'topbar.bitcoinEvents.satoshiFarewell',
    burst: ['S', '∞', '11']
  },
  {
    key: 'first-lightning-mainnet',
    month: 4,
    day: 10,
    emoji: '⚡',
    tooltipKey: 'topbar.bitcoinEvents.firstLightningMainnet',
    burst: ['LN', '₿', '10']
  },
  {
    key: 'pizza-day',
    month: 4,
    day: 22,
    emoji: '🍕',
    tooltipKey: 'topbar.bitcoinEvents.pizzaDay',
    burst: ['⚡', '₿', '22']
  },
  {
    key: 'el-salvador-law',
    month: 5,
    day: 8,
    emoji: '📜',
    tooltipKey: 'topbar.bitcoinEvents.elSalvadorLaw',
    burst: ['law', '₿', '21']
  },
  {
    key: 'second-halving',
    month: 6,
    day: 9,
    emoji: '¼',
    tooltipKey: 'topbar.bitcoinEvents.secondHalving',
    burst: ['25', '12.5', '16']
  },
  {
    key: 'bitcoin-independence',
    month: 7,
    day: 1,
    emoji: '🟧',
    tooltipKey: 'topbar.bitcoinEvents.bitcoinIndependence',
    burst: ['UASF', '₿', '01']
  },
  {
    key: 'segwit',
    month: 7,
    day: 24,
    emoji: '🧩',
    tooltipKey: 'topbar.bitcoinEvents.segwit',
    burst: ['SW', '⚡', '24']
  },
  {
    key: 'el-salvador',
    month: 8,
    day: 7,
    emoji: '🌋',
    tooltipKey: 'topbar.bitcoinEvents.elSalvador',
    burst: ['₿', 'SV', '21']
  },
  {
    key: 'first-price',
    month: 9,
    day: 5,
    emoji: '💱',
    tooltipKey: 'topbar.bitcoinEvents.firstPrice',
    burst: ['$', '₿', '09']
  },
  {
    key: 'whitepaper',
    month: 9,
    day: 31,
    emoji: '📄',
    tooltipKey: 'topbar.bitcoinEvents.whitepaper',
    burst: ['P2P', '₿', '08']
  },
  {
    key: 'brln-foundation',
    month: 10,
    day: 1,
    emoji: '🇧🇷',
    tooltipKey: 'topbar.bitcoinEvents.brlnFoundation',
    burst: ['BR', '⚡', 'LN']
  },
  {
    key: 'taproot',
    month: 10,
    day: 14,
    emoji: '🌱',
    tooltipKey: 'topbar.bitcoinEvents.taproot',
    burst: ['TR', '₿', '21']
  },
  {
    key: 'bitcoin-forum',
    month: 10,
    day: 22,
    emoji: '💬',
    tooltipKey: 'topbar.bitcoinEvents.bitcoinForum',
    burst: ['talk', '₿', '09']
  },
  {
    key: 'first-halving',
    month: 10,
    day: 28,
    emoji: '½',
    tooltipKey: 'topbar.bitcoinEvents.firstHalving',
    burst: ['50', '25', '12']
  },
  {
    key: 'satoshi-last-post',
    month: 11,
    day: 12,
    emoji: '🕯️',
    tooltipKey: 'topbar.bitcoinEvents.satoshiLastPost',
    burst: ['S', '₿', '10']
  }
] as const

const getActiveBitcoinHeaderEvent = (date: Date) => {
  const monthEvents = BITCOIN_HEADER_EVENTS
    .filter((event) => event.month === date.getMonth())
    .sort((left, right) => left.day - right.day)
  if (!monthEvents.length) return null

  return monthEvents.reduce(
    (activeEvent, event) => (date.getDate() >= event.day ? event : activeEvent),
    monthEvents[0]
  )
}

type TopbarProps = {
  onMenuToggle?: () => void
  menuOpen?: boolean
  theme: ThemeMode
  visualTheme: VisualThemeKey
  palette: PaletteKey
  onAppearanceApply: (value: Appearance) => void
  onLogout?: () => void
  authState?: AuthState | null
  onAuthUpdated?: (state: AuthState) => void
  onAuthRefresh?: () => Promise<AuthState | void>
}

export default function Topbar({
  onMenuToggle,
  menuOpen,
  theme,
  visualTheme,
  palette,
  onAppearanceApply,
  onLogout,
  authState,
  onAuthUpdated,
  onAuthRefresh
}: TopbarProps) {
  const { t, i18n } = useTranslation()
  const [status, setStatus] = useState('...')
  const [issues, setIssues] = useState<Array<{ component?: string; level?: string; message?: string }>>([])
  const [nodeAlias, setNodeAlias] = useState('')
  const [nodePubkey, setNodePubkey] = useState('')
  const [securityModalOpen, setSecurityModalOpen] = useState(false)
  const [appearanceMenuOpen, setAppearanceMenuOpen] = useState(false)
  const closeAppearanceMenu = useCallback(() => setAppearanceMenuOpen(false), [])
  const isPortuguese = i18n.language === 'pt-BR'
  const today = new Date()
  const activeBitcoinHeaderEvent = getActiveBitcoinHeaderEvent(today)
  const isBitcoinEventDay = Boolean(activeBitcoinHeaderEvent && today.getDate() === activeBitcoinHeaderEvent.day)
  const bitcoinEventTooltip = activeBitcoinHeaderEvent ? t(activeBitcoinHeaderEvent.tooltipKey) : ''
  const bitcoinEventTooltipID = activeBitcoinHeaderEvent
    ? `bitcoin-header-event-${activeBitcoinHeaderEvent.key}`
    : undefined

  useEffect(() => {
    let mounted = true
    const load = async () => {
      try {
        const data = await getHealth()
        if (!mounted) return
        setStatus(data.status)
        setIssues(Array.isArray(data.issues) ? data.issues : [])
      } catch {
        if (!mounted) return
        setStatus('ERR')
        setIssues([{ component: 'system', level: 'ERR', message: t('topbar.healthCheckFailed') }])
      }
    }

    load()
    const timer = setInterval(load, 30000)
    return () => {
      mounted = false
      clearInterval(timer)
    }
  }, [])

  useEffect(() => {
    let mounted = true
    const load = async () => {
      const [statusRes, configRes] = await Promise.allSettled([getLndStatus(), getLndConfig()])
      if (!mounted) return
      if (statusRes.status === 'fulfilled') {
        const pubkey = typeof statusRes.value?.pubkey === 'string' ? statusRes.value.pubkey.trim() : ''
        setNodePubkey(pubkey)
      }
      if (configRes.status === 'fulfilled') {
        const alias = typeof configRes.value?.current?.alias === 'string' ? configRes.value.current.alias.trim() : ''
        setNodeAlias(alias)
      }
    }

    load()
    const timer = setInterval(load, 30000)
    return () => {
      mounted = false
      clearInterval(timer)
    }
  }, [])

  const resolvedNodeLabel = nodeAlias || nodePubkey
  const compactPubkey = nodePubkey.length > 20
    ? `${nodePubkey.slice(0, 12)}...${nodePubkey.slice(-6)}`
    : nodePubkey
  const displayNodeLabel = nodeAlias || compactPubkey

  return (
    <header className="app-topbar px-6 lg:px-12 pt-8">
      {onMenuToggle && (
        <div className="mb-6 flex items-center justify-between lg:hidden">
          <button
            type="button"
            className="inline-flex items-center gap-2 rounded-full border border-white/15 bg-ink/60 px-3 py-2 text-xs uppercase tracking-wide text-fog/70 hover:text-white hover:border-white/40 transition"
            onClick={onMenuToggle}
            aria-label={menuOpen ? t('topbar.closeMenu') : t('topbar.openMenu')}
            aria-expanded={menuOpen ? true : false}
            aria-controls="app-sidebar"
          >
            {menuOpen ? (
              <svg viewBox="0 0 24 24" className="h-4 w-4" fill="none" stroke="currentColor" strokeWidth="1.8">
                <path d="M6 6l12 12M18 6l-12 12" />
              </svg>
            ) : (
              <svg viewBox="0 0 24 24" className="h-4 w-4" fill="none" stroke="currentColor" strokeWidth="1.8">
                <path d="M4 7h16M4 12h16M4 17h10" />
              </svg>
            )}
            <span>{menuOpen ? t('common.close') : t('common.menu')}</span>
          </button>
          <div className="text-right text-xs text-fog/60">
            <p className="text-fog font-semibold">{t('topbar.productName')}</p>
            <p>{t('topbar.mainnetOnly')}</p>
          </div>
        </div>
      )}
      <div className="flex flex-col lg:flex-row lg:items-center lg:justify-between gap-4">
        <div>
          <p className="text-sm uppercase tracking-[0.3em] text-fog/50">{t('topbar.statusOverview')}</p>
          <h1 className="flex flex-wrap items-center gap-x-2 gap-y-1 text-3xl lg:text-4xl font-semibold">
            <span>{t('topbar.controlCenter')}</span>
            {activeBitcoinHeaderEvent && (
              <span
                className={`bitcoin-date-badge bitcoin-date-badge--${activeBitcoinHeaderEvent.key}${isBitcoinEventDay ? ' bitcoin-date-badge--today' : ''}`}
                tabIndex={0}
                role="img"
                aria-label={bitcoinEventTooltip}
                aria-describedby={bitcoinEventTooltipID}
                title={bitcoinEventTooltip}
              >
                <span className="bitcoin-date-badge__emoji" aria-hidden="true">{activeBitcoinHeaderEvent.emoji}</span>
                {isBitcoinEventDay && (
                  <span className="bitcoin-date-badge__burst" aria-hidden="true">
                    {activeBitcoinHeaderEvent.burst.map((item) => (
                      <span key={item}>{item}</span>
                    ))}
                  </span>
                )}
                <span id={bitcoinEventTooltipID} className="bitcoin-date-badge__tooltip" role="tooltip">
                  {bitcoinEventTooltip}
                </span>
              </span>
            )}
          </h1>
          {displayNodeLabel && (
            <p className="mt-2 text-base lg:text-lg font-semibold text-fog/80" title={resolvedNodeLabel}>
              {t('topbar.nodeLabel', { node: displayNodeLabel })}
            </p>
          )}
        </div>
        <div className="flex flex-wrap items-center justify-end gap-4">
          <StatusBadge
            label={status}
            tone={status === 'OK' ? 'ok' : status === 'WARN' ? 'warn' : status === 'ERR' ? 'danger' : 'muted'}
            size="md"
          />
          <div className="text-xs text-fog/60 max-w-xs">
            {issues.length
              ? issues
                .map((issue) => {
                  const label = issue.component ? issue.component.toUpperCase() : t('topbar.systemLabel')
                  const message = issue.message || t('topbar.issueDetected')
                  return `${label}: ${message}`
                })
                .join(' • ')
              : status === '...'
                ? t('topbar.checkingStatus')
                : status === 'OK'
                  ? t('topbar.allSystemsGreen')
                  : t('topbar.statusUnavailable')}
          </div>
          <button
            type="button"
            className="inline-flex items-center gap-1 rounded-full border border-white/15 bg-ink/60 px-3 py-2 text-xs uppercase tracking-wide text-fog/70 hover:text-white hover:border-white/40 transition"
            onClick={() => setLanguage(isPortuguese ? 'en' : 'pt-BR')}
            aria-label={t('topbar.toggleLanguage')}
            title={t('topbar.toggleLanguage')}
          >
            <span className={isPortuguese ? 'text-fog/50' : 'text-white'}>EN</span>
            <span className="text-fog/40">|</span>
            <span className={isPortuguese ? 'text-white' : 'text-fog/50'}>PT</span>
          </button>
          <button
            type="button"
            className="appearance-trigger"
            onClick={() => setAppearanceMenuOpen(true)}
            aria-label={t('appearance.open')}
            title={t('appearance.open')}
          >
            <span className="appearance-trigger__icon" aria-hidden="true">
              <i />
              <i />
              <i />
            </span>
            <span className="appearance-trigger__label">{t('appearance.button')}</span>
          </button>
          {authState?.enabled && onAuthUpdated && (
            <button
              type="button"
              className="inline-flex items-center gap-2 rounded-full border border-white/15 bg-ink/60 px-3 py-2 text-xs uppercase tracking-wide text-fog/70 hover:text-white hover:border-white/40 transition"
              onClick={() => setSecurityModalOpen(true)}
            >
              {t('topbar.changePassword')}
            </button>
          )}
          {onLogout && (
            <button
              type="button"
              className="inline-flex items-center gap-2 rounded-full border border-white/15 bg-ink/60 px-3 py-2 text-xs uppercase tracking-wide text-fog/70 hover:text-white hover:border-white/40 transition"
              onClick={onLogout}
            >
              {t('common.logout')}
            </button>
          )}
        </div>
      </div>
      <div className="glow-divider mt-6" />
      <AppearanceMenu
        open={appearanceMenuOpen}
        visualTheme={visualTheme}
        palette={palette}
        mode={theme}
        onApply={onAppearanceApply}
        onClose={closeAppearanceMenu}
      />
      {authState?.enabled && onAuthUpdated && (
        <AccountSecurityModal
          open={securityModalOpen}
          state={authState}
          onClose={() => setSecurityModalOpen(false)}
          onAuthenticated={(next) => {
            onAuthUpdated(next)
            setSecurityModalOpen(false)
          }}
          onRefreshState={onAuthRefresh}
        />
      )}
    </header>
  )
}
