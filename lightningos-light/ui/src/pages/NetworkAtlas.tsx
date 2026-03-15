import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getNetworkAtlasConfig, getNetworkAtlasMap, updateNetworkAtlasConfig } from '../api'

type AtlasNode = {
  alias: string
  pubkey: string
  label: string
  lat: number
  lon: number
  location_set: boolean
  source: 'configured' | 'detected' | 'unavailable' | string
  warnings?: string[]
}

type AtlasLink = {
  pubkey: string
  alias: string
  socket?: string
  host?: string
  country?: string
  country_code?: string
  city?: string
  lat?: number
  lon?: number
  connection_kind: 'channel' | 'peer'
  channel_count: number
  capacity_sat: number
  active: boolean
  is_onion: boolean
  is_private_ip: boolean
  mapped: boolean
  reason?: string
}

type AtlasSummary = {
  total_peers: number
  channel_peers: number
  peer_only: number
  unknown_location: number
  countries: number
  mapped_capacity_sat: number
}

type AtlasResponse = {
  local_node: AtlasNode
  summary: AtlasSummary
  links: AtlasLink[]
  unmapped: AtlasLink[]
}

type AtlasConfig = {
  label: string
  lat?: number
  lon?: number
  has_explicit_location: boolean
}

type FilterMode = 'all' | 'channel' | 'peer'

const WORLD_PATHS = [
  'M68 214C95 188 128 177 165 182C194 186 221 201 234 221C248 242 260 258 281 264C309 273 349 268 378 258C395 252 404 238 417 225C396 211 371 200 343 192C304 181 270 183 242 190C209 198 184 215 157 227C126 241 89 240 68 214Z',
  'M371 153C403 132 441 121 482 122C514 123 548 131 574 146C589 155 602 170 616 183C628 193 647 195 653 211C636 224 614 228 594 228C565 228 539 233 513 242C484 252 446 264 415 255C403 251 391 241 385 230C379 218 378 205 375 192C372 179 362 169 371 153Z',
  'M630 163C655 147 690 138 720 138C751 138 784 144 811 159C837 174 861 201 860 228C858 254 833 274 807 285C771 300 728 303 689 295C664 289 639 278 624 259C608 239 603 212 607 191C610 179 618 170 630 163Z',
  'M548 294C574 286 606 293 624 312C639 327 646 349 650 370C654 391 655 412 648 432C640 456 619 476 594 479C573 482 554 469 544 451C532 430 530 406 526 383C521 355 512 327 520 305C525 297 536 294 548 294Z',
  'M809 330C830 316 859 308 886 311C910 314 936 327 946 349C956 370 951 396 938 415C922 439 892 453 863 453C839 452 818 441 803 422C786 401 779 372 786 349C790 341 798 336 809 330Z',
  'M920 196C943 180 974 173 1003 176C1028 178 1052 189 1062 208C1069 223 1067 243 1057 257C1044 275 1020 287 994 291C964 295 930 289 909 270C895 258 889 240 891 223C893 212 901 203 920 196Z'
]

const STARFIELD = [
  [86, 72], [148, 116], [212, 94], [290, 83], [344, 132], [418, 70], [507, 112], [592, 86], [664, 108],
  [741, 76], [828, 120], [906, 74], [980, 108], [1052, 86], [1128, 120], [1206, 82], [1292, 114], [1388, 96]
]

const projectPoint = (lon: number, lat: number) => ({
  x: ((lon + 180) / 360) * 1600,
  y: ((90 - lat) / 180) * 760
})

const clamp = (value: number, min: number, max: number) => Math.min(max, Math.max(min, value))

const formatReasonKey = (reason?: string) => {
  switch (reason) {
    case 'tor_only':
      return 'torOnly'
    case 'private_ip':
      return 'privateIp'
    case 'address_unavailable':
      return 'addressUnavailable'
    case 'host_unavailable':
      return 'hostUnavailable'
    case 'geo_unavailable':
      return 'geoUnavailable'
    default:
      return 'unknown'
  }
}

const shortPubkey = (value: string) => {
  const trimmed = value.trim()
  if (trimmed.length <= 18) return trimmed
  return `${trimmed.slice(0, 9)}...${trimmed.slice(-6)}`
}

const formatSats = (value: number) => `${Math.max(0, Math.trunc(value || 0)).toLocaleString()} sat`

const ArcLayer = ({
  localNode,
  links,
  selectedKey,
  onSelect
}: {
  localNode: AtlasNode
  links: AtlasLink[]
  selectedKey: string
  onSelect: (key: string) => void
}) => {
  const origin = projectPoint(localNode.lon, localNode.lat)

  return (
    <svg viewBox="0 0 1600 760" className="atlas-map-svg" role="img" aria-label="Network atlas map">
      <defs>
        <linearGradient id="atlasGlowGradient" x1="0%" y1="0%" x2="100%" y2="0%">
          <stop offset="0%" stopColor="rgba(82,255,152,0)" />
          <stop offset="48%" stopColor="rgba(82,255,152,0.95)" />
          <stop offset="100%" stopColor="rgba(166,253,255,0.2)" />
        </linearGradient>
        <linearGradient id="atlasPeerGradient" x1="0%" y1="0%" x2="100%" y2="0%">
          <stop offset="0%" stopColor="rgba(156,163,175,0)" />
          <stop offset="50%" stopColor="rgba(156,163,175,0.55)" />
          <stop offset="100%" stopColor="rgba(226,232,240,0.08)" />
        </linearGradient>
        <filter id="atlasNeonBlur" x="-50%" y="-50%" width="200%" height="200%">
          <feGaussianBlur stdDeviation="5" result="blur" />
          <feMerge>
            <feMergeNode in="blur" />
            <feMergeNode in="SourceGraphic" />
          </feMerge>
        </filter>
        <radialGradient id="atlasCoreGlow" cx="50%" cy="50%" r="50%">
          <stop offset="0%" stopColor="rgba(82,255,152,0.95)" />
          <stop offset="45%" stopColor="rgba(82,255,152,0.32)" />
          <stop offset="100%" stopColor="rgba(82,255,152,0)" />
        </radialGradient>
      </defs>

      <rect x="0" y="0" width="1600" height="760" rx="36" fill="rgba(3,7,18,0.76)" />
      <g opacity="0.75">
        {STARFIELD.map(([x, y], index) => (
          <circle key={`${x}-${y}-${index}`} cx={x * 10} cy={y * 6} r={index % 4 === 0 ? 2.4 : 1.2} fill="rgba(255,255,255,0.32)" />
        ))}
      </g>
      <g className="atlas-grid-lines">
        {Array.from({ length: 7 }).map((_, index) => (
          <line key={`h-${index}`} x1="0" y1={120 + index * 85} x2="1600" y2={120 + index * 85} />
        ))}
        {Array.from({ length: 11 }).map((_, index) => (
          <line key={`v-${index}`} x1={120 + index * 135} y1="0" x2={120 + index * 135} y2="760" />
        ))}
      </g>
      <g className="atlas-continent-layer">
        {WORLD_PATHS.map((path, index) => (
          <path key={index} d={path} transform="scale(2.15 2.2)" />
        ))}
      </g>

      <circle cx={origin.x} cy={origin.y} r="76" fill="url(#atlasCoreGlow)" />

      <g>
        {links.map((link, index) => {
          if (typeof link.lon !== 'number' || typeof link.lat !== 'number') return null
          const point = projectPoint(link.lon, link.lat)
          const distance = Math.abs(point.x - origin.x)
          const curveLift = clamp(distance * 0.16 + Math.abs(point.y - origin.y) * 0.22, 34, 190)
          const controlX = (origin.x + point.x) / 2
          const controlY = Math.min(origin.y, point.y) - curveLift
          const path = `M ${origin.x} ${origin.y} Q ${controlX} ${controlY} ${point.x} ${point.y}`
          const emphasized = selectedKey === link.pubkey
          const lineClass = link.connection_kind === 'channel' ? 'atlas-line atlas-line--channel' : 'atlas-line atlas-line--peer'
          return (
            <g
              key={`${link.pubkey}-${index}`}
              onMouseEnter={() => onSelect(link.pubkey)}
              onFocus={() => onSelect(link.pubkey)}
              className="cursor-pointer"
            >
              <path
                d={path}
                className={lineClass}
                strokeWidth={emphasized ? 4 : link.connection_kind === 'channel' ? 2.3 : 1.5}
                filter={link.connection_kind === 'channel' ? 'url(#atlasNeonBlur)' : undefined}
              />
              <circle cx={point.x} cy={point.y} r={emphasized ? 5.8 : link.connection_kind === 'channel' ? 4.6 : 3.6} className={link.connection_kind === 'channel' ? 'atlas-point atlas-point--channel' : 'atlas-point atlas-point--peer'} />
            </g>
          )
        })}
      </g>

      <g className="atlas-node-marker">
        <circle cx={origin.x} cy={origin.y} r="10" />
        <circle cx={origin.x} cy={origin.y} r="18" className="atlas-node-pulse" />
      </g>
    </svg>
  )
}

export default function NetworkAtlas() {
  const { t } = useTranslation()
  const [payload, setPayload] = useState<AtlasResponse | null>(null)
  const [config, setConfig] = useState<AtlasConfig | null>(null)
  const [filterMode, setFilterMode] = useState<FilterMode>('all')
  const [status, setStatus] = useState('')
  const [saving, setSaving] = useState(false)
  const [loading, setLoading] = useState(true)
  const [selectedKey, setSelectedKey] = useState('')
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [labelInput, setLabelInput] = useState('')
  const [latInput, setLatInput] = useState('')
  const [lonInput, setLonInput] = useState('')

  useEffect(() => {
    let active = true

    const load = async (initial = false) => {
      const [mapResult, configResult] = await Promise.allSettled([getNetworkAtlasMap(), getNetworkAtlasConfig()])
      if (!active) return

      if (mapResult.status === 'fulfilled') {
        const nextPayload = mapResult.value as AtlasResponse
        setPayload(nextPayload)
        setSelectedKey((current) => {
          if (current && nextPayload.links.some((item) => item.pubkey === current)) return current
          return nextPayload.links[0]?.pubkey || ''
        })
        setStatus('')
      } else {
        setStatus((mapResult.reason as Error)?.message || t('networkAtlas.loadFailed'))
      }

      if (configResult.status === 'fulfilled') {
        const nextConfig = configResult.value as AtlasConfig
        setConfig(nextConfig)
        setLabelInput(nextConfig?.label || '')
        setLatInput(typeof nextConfig?.lat === 'number' ? String(nextConfig.lat) : '')
        setLonInput(typeof nextConfig?.lon === 'number' ? String(nextConfig.lon) : '')
      }

      if (initial) setLoading(false)
    }

    void load(true)
    const timer = window.setInterval(() => void load(false), 60000)

    return () => {
      active = false
      window.clearInterval(timer)
    }
  }, [t])

  const filteredLinks = useMemo(() => {
    const base = payload?.links ?? []
    if (filterMode === 'all') return base
    return base.filter((item) => item.connection_kind === filterMode)
  }, [filterMode, payload?.links])

  const selectedLink = useMemo(
    () => filteredLinks.find((item) => item.pubkey === selectedKey) || filteredLinks[0] || null,
    [filteredLinks, selectedKey]
  )

  const topLinks = useMemo(() => filteredLinks.slice(0, 10), [filteredLinks])
  const unmappedLinks = payload?.unmapped ?? []
  const localNode = payload?.local_node

  const handleRefresh = async () => {
    setStatus(t('networkAtlas.refreshing'))
    try {
      const nextPayload = await getNetworkAtlasMap()
      setPayload(nextPayload as AtlasResponse)
      setSelectedKey((nextPayload as AtlasResponse)?.links?.[0]?.pubkey || '')
      setStatus('')
    } catch (err) {
      setStatus((err as Error)?.message || t('networkAtlas.loadFailed'))
    }
  }

  const handleSaveConfig = async () => {
    setSaving(true)
    setStatus(t('networkAtlas.savingLocation'))
    try {
      const nextConfig = await updateNetworkAtlasConfig({
        label: labelInput.trim(),
        lat: latInput.trim() ? latInput.trim() : null,
        lon: lonInput.trim() ? lonInput.trim() : null
      })
      setConfig(nextConfig as AtlasConfig)
      const nextPayload = await getNetworkAtlasMap()
      setPayload(nextPayload as AtlasResponse)
      setStatus(t('networkAtlas.locationSaved'))
      setSettingsOpen(false)
    } catch (err) {
      setStatus((err as Error)?.message || t('networkAtlas.saveFailed'))
    } finally {
      setSaving(false)
    }
  }

  const handleUseAutoLocation = async () => {
    setLatInput('')
    setLonInput('')
    setSaving(true)
    setStatus(t('networkAtlas.savingLocation'))
    try {
      const nextConfig = await updateNetworkAtlasConfig({
        label: labelInput.trim(),
        lat: null,
        lon: null
      })
      setConfig(nextConfig as AtlasConfig)
      const nextPayload = await getNetworkAtlasMap()
      setPayload(nextPayload as AtlasResponse)
      setStatus(t('networkAtlas.autoLocationEnabled'))
      setSettingsOpen(false)
    } catch (err) {
      setStatus((err as Error)?.message || t('networkAtlas.saveFailed'))
    } finally {
      setSaving(false)
    }
  }

  return (
    <section className="space-y-6">
      <div className="atlas-shell section-card overflow-hidden">
        <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
          <div className="max-w-3xl">
            <p className="atlas-eyebrow">{t('networkAtlas.eyebrow')}</p>
            <h2 className="mt-2 text-3xl font-semibold tracking-tight sm:text-4xl">{t('networkAtlas.title')}</h2>
            <p className="mt-3 max-w-2xl text-sm text-fog/70 sm:text-base">{t('networkAtlas.subtitle')}</p>
          </div>
          <div className="flex flex-wrap items-center gap-3">
            <button className="atlas-control-pill" type="button" onClick={() => setSettingsOpen((current) => !current)}>
              {settingsOpen ? t('networkAtlas.hideSettings') : t('networkAtlas.showSettings')}
            </button>
            <button className="btn-primary" type="button" onClick={handleRefresh}>
              {t('common.refresh')}
            </button>
          </div>
        </div>

        <div className="mt-6 grid gap-3 md:grid-cols-2 xl:grid-cols-5">
          <div className="atlas-kpi">
            <span>{t('networkAtlas.kpiPeers')}</span>
            <strong>{payload?.summary.total_peers?.toLocaleString() ?? '--'}</strong>
          </div>
          <div className="atlas-kpi">
            <span>{t('networkAtlas.kpiChannels')}</span>
            <strong>{payload?.summary.channel_peers?.toLocaleString() ?? '--'}</strong>
          </div>
          <div className="atlas-kpi">
            <span>{t('networkAtlas.kpiPeerOnly')}</span>
            <strong>{payload?.summary.peer_only?.toLocaleString() ?? '--'}</strong>
          </div>
          <div className="atlas-kpi">
            <span>{t('networkAtlas.kpiCountries')}</span>
            <strong>{payload?.summary.countries?.toLocaleString() ?? '--'}</strong>
          </div>
          <div className="atlas-kpi">
            <span>{t('networkAtlas.kpiCapacity')}</span>
            <strong>{payload ? formatSats(payload.summary.mapped_capacity_sat) : '--'}</strong>
          </div>
        </div>

        <div className="mt-6 flex flex-wrap items-center gap-3">
          <button className={filterMode === 'all' ? 'btn-primary' : 'atlas-control-pill'} type="button" onClick={() => setFilterMode('all')}>
            {t('common.all')}
          </button>
          <button className={filterMode === 'channel' ? 'btn-primary' : 'atlas-control-pill'} type="button" onClick={() => setFilterMode('channel')}>
            {t('networkAtlas.channelsOnly')}
          </button>
          <button className={filterMode === 'peer' ? 'btn-primary' : 'atlas-control-pill'} type="button" onClick={() => setFilterMode('peer')}>
            {t('networkAtlas.peersOnly')}
          </button>
          <div className="ml-auto flex flex-wrap items-center gap-4 text-xs text-fog/65">
            <span className="atlas-legend-item"><i className="atlas-legend-dot atlas-legend-dot--channel" />{t('networkAtlas.legendChannel')}</span>
            <span className="atlas-legend-item"><i className="atlas-legend-dot atlas-legend-dot--peer" />{t('networkAtlas.legendPeer')}</span>
            <span className="atlas-legend-item"><i className="atlas-legend-dot atlas-legend-dot--node" />{t('networkAtlas.legendNode')}</span>
          </div>
        </div>

        {status && <p className="mt-4 text-sm text-brass">{status}</p>}
        {loading && !payload && <p className="mt-4 text-sm text-fog/60">{t('networkAtlas.loading')}</p>}

        <div className="mt-6 grid gap-6 xl:grid-cols-[minmax(0,1.65fr)_380px]">
          <div className="atlas-map-frame">
            {payload && localNode?.location_set ? (
              <>
                <ArcLayer localNode={localNode} links={filteredLinks} selectedKey={selectedLink?.pubkey || ''} onSelect={setSelectedKey} />
                <div className="atlas-map-overlay atlas-map-overlay--top">
                  <div>
                    <p className="text-[11px] uppercase tracking-[0.28em] text-fog/45">{t('networkAtlas.localNode')}</p>
                    <h3 className="mt-2 text-xl font-semibold">{localNode.label || localNode.alias || shortPubkey(localNode.pubkey)}</h3>
                    <p className="mt-1 text-sm text-fog/65">
                      {localNode.source === 'configured' ? t('networkAtlas.locationConfigured') : t('networkAtlas.locationDetected')}
                    </p>
                  </div>
                  <div className="atlas-map-pill">
                    <span>{t('networkAtlas.renderedLinks')}</span>
                    <strong>{filteredLinks.length.toLocaleString()}</strong>
                  </div>
                </div>
                <div className="atlas-map-overlay atlas-map-overlay--bottom">
                  {localNode.warnings?.map((warning) => (
                    <p key={warning} className="text-xs text-fog/68">{warning}</p>
                  ))}
                </div>
              </>
            ) : (
              <div className="flex min-h-[520px] items-center justify-center px-6 text-center">
                <div className="max-w-lg space-y-3">
                  <h3 className="text-2xl font-semibold">{t('networkAtlas.locationNeededTitle')}</h3>
                  <p className="text-sm text-fog/68">{t('networkAtlas.locationNeededBody')}</p>
                  <button className="btn-primary" type="button" onClick={() => setSettingsOpen(true)}>
                    {t('networkAtlas.openLocationSettings')}
                  </button>
                </div>
              </div>
            )}
          </div>

          <aside className="space-y-4">
            <div className="atlas-sidecard">
              <p className="atlas-sidecard-title">{t('networkAtlas.selectedPeer')}</p>
              {selectedLink ? (
                <div className="space-y-4">
                  <div>
                    <h3 className="text-xl font-semibold">{selectedLink.alias || shortPubkey(selectedLink.pubkey)}</h3>
                    <p className="mt-1 text-xs text-fog/55 break-all">{selectedLink.pubkey}</p>
                  </div>
                  <div className="grid gap-3 sm:grid-cols-2">
                    <div className="atlas-mini-stat">
                      <span>{t('networkAtlas.connectionType')}</span>
                      <strong>{selectedLink.connection_kind === 'channel' ? t('networkAtlas.legendChannel') : t('networkAtlas.legendPeer')}</strong>
                    </div>
                    <div className="atlas-mini-stat">
                      <span>{t('networkAtlas.capacity')}</span>
                      <strong>{selectedLink.capacity_sat > 0 ? formatSats(selectedLink.capacity_sat) : t('common.na')}</strong>
                    </div>
                    <div className="atlas-mini-stat">
                      <span>{t('networkAtlas.location')}</span>
                      <strong>{selectedLink.city || selectedLink.country_code || t('common.na')}</strong>
                    </div>
                    <div className="atlas-mini-stat">
                      <span>{t('common.status')}</span>
                      <strong>{selectedLink.active ? t('common.active') : t('common.inactive')}</strong>
                    </div>
                  </div>
                  <div className="space-y-2 text-sm text-fog/72">
                    <p>{selectedLink.city && selectedLink.country ? `${selectedLink.city}, ${selectedLink.country}` : selectedLink.country || t('common.na')}</p>
                    <p className="break-all">{selectedLink.socket || selectedLink.host || t('common.na')}</p>
                  </div>
                </div>
              ) : (
                <p className="text-sm text-fog/60">{t('networkAtlas.noPeerSelected')}</p>
              )}
            </div>

            {settingsOpen && (
              <div className="atlas-sidecard">
                <p className="atlas-sidecard-title">{t('networkAtlas.settingsTitle')}</p>
                <div className="space-y-3">
                  <input
                    className="input-field"
                    value={labelInput}
                    onChange={(event) => setLabelInput(event.target.value)}
                    placeholder={t('networkAtlas.nodeLabelPlaceholder')}
                  />
                  <div className="grid gap-3 sm:grid-cols-2">
                    <input
                      className="input-field"
                      value={latInput}
                      onChange={(event) => setLatInput(event.target.value)}
                      placeholder={t('networkAtlas.latitude')}
                      inputMode="decimal"
                    />
                    <input
                      className="input-field"
                      value={lonInput}
                      onChange={(event) => setLonInput(event.target.value)}
                      placeholder={t('networkAtlas.longitude')}
                      inputMode="decimal"
                    />
                  </div>
                  <p className="text-xs text-fog/55">
                    {config?.has_explicit_location ? t('networkAtlas.manualLocationActive') : t('networkAtlas.autoLocationActive')}
                  </p>
                  <div className="flex flex-wrap gap-3">
                    <button className="btn-primary" type="button" onClick={handleSaveConfig} disabled={saving}>
                      {saving ? t('common.saving') : t('common.save')}
                    </button>
                    <button className="atlas-control-pill" type="button" onClick={handleUseAutoLocation} disabled={saving}>
                      {t('networkAtlas.useAutoLocation')}
                    </button>
                  </div>
                </div>
              </div>
            )}
          </aside>
        </div>
      </div>

      <div className="grid gap-6 xl:grid-cols-[minmax(0,1.15fr)_minmax(0,0.85fr)]">
        <div className="section-card">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <h3 className="text-xl font-semibold">{t('networkAtlas.topPeersTitle')}</h3>
              <p className="text-sm text-fog/60">{t('networkAtlas.topPeersSubtitle')}</p>
            </div>
            <span className="text-xs text-fog/55">{filteredLinks.length.toLocaleString()} {t('networkAtlas.visiblePeers')}</span>
          </div>
          <div className="atlas-list mt-5">
            {topLinks.map((link) => (
              <button
                key={link.pubkey}
                type="button"
                onClick={() => setSelectedKey(link.pubkey)}
                className={`atlas-list-row ${selectedLink?.pubkey === link.pubkey ? 'atlas-list-row--active' : ''}`}
              >
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium">{link.alias || shortPubkey(link.pubkey)}</p>
                  <p className="mt-1 truncate text-xs text-fog/55">{link.city && link.country ? `${link.city}, ${link.country}` : link.country || link.host || t('common.na')}</p>
                </div>
                <div className="text-right">
                  <p className={`text-xs uppercase tracking-[0.24em] ${link.connection_kind === 'channel' ? 'text-emerald-200' : 'text-fog/50'}`}>
                    {link.connection_kind === 'channel' ? t('networkAtlas.legendChannel') : t('networkAtlas.legendPeer')}
                  </p>
                  <p className="mt-1 text-sm text-fog/80">{link.capacity_sat > 0 ? formatSats(link.capacity_sat) : '--'}</p>
                </div>
              </button>
            ))}
            {!topLinks.length && <p className="text-sm text-fog/60">{t('networkAtlas.noPeersForFilter')}</p>}
          </div>
        </div>

        <div className="section-card">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <h3 className="text-xl font-semibold">{t('networkAtlas.unmappedTitle')}</h3>
              <p className="text-sm text-fog/60">{t('networkAtlas.unmappedSubtitle')}</p>
            </div>
            <span className="text-xs text-fog/55">{unmappedLinks.length.toLocaleString()}</span>
          </div>
          <div className="atlas-list mt-5">
            {unmappedLinks.slice(0, 12).map((link) => (
              <div key={link.pubkey} className="atlas-list-row">
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium">{link.alias || shortPubkey(link.pubkey)}</p>
                  <p className="mt-1 truncate text-xs text-fog/55">{link.socket || link.host || shortPubkey(link.pubkey)}</p>
                </div>
                <div className="text-right">
                  <p className="text-xs uppercase tracking-[0.24em] text-fog/45">{t(`networkAtlas.reasons.${formatReasonKey(link.reason)}`)}</p>
                  <p className="mt-1 text-xs text-fog/60">{link.connection_kind === 'channel' ? t('networkAtlas.legendChannel') : t('networkAtlas.legendPeer')}</p>
                </div>
              </div>
            ))}
            {!unmappedLinks.length && <p className="text-sm text-fog/60">{t('networkAtlas.noUnmappedPeers')}</p>}
          </div>
        </div>
      </div>
    </section>
  )
}
