import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ComposableMap, Geographies, Geography, Marker, Sphere, ZoomableGroup, useMapContext } from 'react-simple-maps'
import worldGeo from 'world-atlas/countries-110m.json'
import { getNetworkAtlasConfig, getNetworkAtlasMap, updateNetworkAtlasConfig } from '../api'

type AtlasNode = {
  alias: string
  pubkey: string
  label: string
  lat: number
  lon: number
  location_set: boolean
  source: 'configured' | 'uri' | 'detected' | 'unavailable' | string
  warnings?: string[]
}

type AtlasLink = {
  pubkey: string
  alias: string
  channel_point?: string
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
  mapped: boolean
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

const shortPubkey = (value: string) => {
  const trimmed = value.trim()
  if (trimmed.length <= 18) return trimmed
  return `${trimmed.slice(0, 9)}...${trimmed.slice(-6)}`
}

const formatSats = (value: number) => `${Math.max(0, Math.trunc(value || 0)).toLocaleString()} sat`
const clampZoom = (value: number) => Math.min(6, Math.max(1, value))
const LIGHTNING_OPS_ROUTE_KEY = 'lightning-ops'
const buildLightningOpsHash = (channelPoint: string) =>
  `#${LIGHTNING_OPS_ROUTE_KEY}?channel_point=${encodeURIComponent(channelPoint)}`

const AtlasConnections = ({
  localNode,
  links,
  mapZoom,
  selectedKey,
  onSelect,
  onActivate,
  onOpenChannel
}: {
  localNode: AtlasNode | null
  links: AtlasLink[]
  mapZoom: number
  selectedKey: string
  onSelect: (key: string) => void
  onActivate: (link: AtlasLink) => void
  onOpenChannel: (link: AtlasLink) => void
}) => {
  const { projection } = useMapContext()
  if (!localNode?.location_set) return null

  const origin = projection([localNode.lon, localNode.lat])
  if (!origin) return null

  return (
    <g>
      {links.map((link) => {
        if (typeof link.lon !== 'number' || typeof link.lat !== 'number') return null
        const destination = projection([link.lon, link.lat])
        if (!destination) return null

        const [x1, y1] = origin
        const [x2, y2] = destination
        const mx = (x1 + x2) / 2
        const my = (y1 + y2) / 2
        const arcHeight = Math.max(22, Math.abs(x2 - x1) * 0.14 + Math.abs(y2 - y1) * 0.08)
        const d = `M ${x1} ${y1} C ${mx} ${my - arcHeight}, ${mx} ${my - arcHeight}, ${x2} ${y2}`
        const selected = selectedKey === link.pubkey
        const dimmed = Boolean(selectedKey) && !selected
        const pathId = `atlas-arc-${link.pubkey}`

        return (
          <g
            key={link.pubkey}
            className={`cursor-pointer ${selected ? 'atlas-link-state atlas-link-state--selected' : dimmed ? 'atlas-link-state atlas-link-state--dimmed' : 'atlas-link-state'}`}
            onPointerEnter={() => onSelect(link.pubkey)}
            onFocus={() => onSelect(link.pubkey)}
          >
            <path
              d={d}
              className="atlas-arc-hit"
              strokeWidth={selected ? 18 : 14}
              onClick={() => onActivate(link)}
              onDoubleClick={() => onOpenChannel(link)}
            />
            <path
              id={pathId}
              d={d}
              className={link.connection_kind === 'channel' ? 'atlas-arc atlas-arc--channel' : 'atlas-arc atlas-arc--peer'}
              strokeWidth={selected ? 2.6 : link.connection_kind === 'channel' ? 1.8 : 1.2}
            />
            {link.connection_kind === 'channel' && (
              <>
                <circle r={selected ? 3.4 : 2.6} className="atlas-pulse-dot">
                  <animateMotion dur={selected ? '1.4s' : '1.9s'} repeatCount="indefinite" rotate="auto">
                    <mpath href={`#${pathId}`} />
                  </animateMotion>
                </circle>
                <circle r={selected ? 2.6 : 2.1} className="atlas-pulse-dot atlas-pulse-dot--secondary">
                  <animateMotion begin="0.7s" dur={selected ? '1.8s' : '2.4s'} repeatCount="indefinite" rotate="auto">
                    <mpath href={`#${pathId}`} />
                  </animateMotion>
                </circle>
              </>
            )}
          </g>
        )
      })}
      <g transform={`translate(${origin[0]} ${origin[1]}) scale(${1 / mapZoom})`} className="atlas-node-anchor">
        <circle r="22" />
        <circle r="7.5" className="atlas-node-anchor__core" />
      </g>
    </g>
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
  const [mapCenter, setMapCenter] = useState<[number, number]>([0, 0])
  const [mapZoom, setMapZoom] = useState(1)
  const [focusPulseKey, setFocusPulseKey] = useState('')

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
    const base = (payload?.links ?? []).filter((item) => typeof item.lat === 'number' && typeof item.lon === 'number')
    if (filterMode === 'all') return base
    return base.filter((item) => item.connection_kind === filterMode)
  }, [filterMode, payload?.links])

  const selectedLink = useMemo(
    () => filteredLinks.find((item) => item.pubkey === selectedKey) || filteredLinks[0] || null,
    [filteredLinks, selectedKey]
  )

  const localNode = payload?.local_node ?? null
  const localNodeLabel = localNode ? (localNode.label || localNode.alias || shortPubkey(localNode.pubkey)) : ''
  const localNodeSourceLabel = localNode
    ? localNode.source === 'configured'
      ? t('networkAtlas.locationConfigured')
      : localNode.source === 'uri'
        ? t('networkAtlas.locationFromUri')
        : localNode.source === 'detected'
          ? t('networkAtlas.locationDetected')
          : t('networkAtlas.locationUnavailable')
    : t('networkAtlas.locationUnavailable')

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

  const handleZoom = (direction: 'in' | 'out' | 'reset') => {
    if (direction === 'reset') {
      setMapCenter([0, 0])
      setMapZoom(1)
      return
    }
    setMapZoom((current) => clampZoom(current + (direction === 'in' ? 0.55 : -0.55)))
  }

  const handlePeerActivate = (link: AtlasLink) => {
    setSelectedKey(link.pubkey)
    if (typeof link.lon === 'number' && typeof link.lat === 'number') {
      setMapCenter([link.lon, link.lat])
      setMapZoom((current) => clampZoom(current < 2.4 ? 2.4 : current))
      setFocusPulseKey(link.pubkey)
      window.setTimeout(() => {
        setFocusPulseKey((current) => (current === link.pubkey ? '' : current))
      }, 1100)
    }
  }

  const handleOpenChannel = (link: AtlasLink) => {
    const channelPoint = String(link.channel_point || '').trim()
    if (!channelPoint) return
    window.location.hash = buildLightningOpsHash(channelPoint)
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
      <div className="atlas-shell section-card">
        <div className="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
          <div className="max-w-3xl">
            <p className="atlas-eyebrow">{t('networkAtlas.eyebrow')}</p>
            <h2 className="mt-2 text-3xl font-semibold tracking-tight sm:text-4xl">{t('networkAtlas.title')}</h2>
            <p className="mt-3 text-sm text-fog/70 sm:text-base">{t('networkAtlas.subtitle')}</p>
          </div>
          <div className="flex flex-wrap items-center gap-3">
            {localNode && !localNode.location_set && (
              <button className="atlas-control-pill" type="button" onClick={() => setSettingsOpen((current) => !current)}>
                {settingsOpen ? t('networkAtlas.hideSettings') : t('networkAtlas.showSettings')}
              </button>
            )}
            <button className="btn-primary" type="button" onClick={handleRefresh}>{t('common.refresh')}</button>
          </div>
        </div>

        <div className="mt-6 grid gap-3 md:grid-cols-2 xl:grid-cols-4">
          <div className="atlas-kpi">
            <span>{t('networkAtlas.kpiPeers')}</span>
            <strong>{payload?.summary.total_peers?.toLocaleString() ?? '--'}</strong>
          </div>
          <div className="atlas-kpi">
            <span>{t('networkAtlas.kpiChannels')}</span>
            <strong>{payload?.summary.channel_peers?.toLocaleString() ?? '--'}</strong>
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

        <div className="atlas-map-card mt-6">
          <div className="atlas-map-header">
            <div>
              <p className="atlas-map-label">{t('networkAtlas.localNode')}</p>
              <h3 className="mt-2 text-xl font-semibold">{localNodeLabel || '--'}</h3>
              <p className="mt-1 text-sm text-fog/62">{localNodeSourceLabel}</p>
            </div>
            <div className="atlas-map-badge">
              <span>{t('networkAtlas.renderedLinks')}</span>
              <strong>{filteredLinks.length.toLocaleString()}</strong>
            </div>
          </div>

          <div className="atlas-world-stage">
            <div className="atlas-map-controls">
              <button type="button" className="atlas-map-control" onClick={() => handleZoom('in')} aria-label={t('networkAtlas.zoomIn')} title={t('networkAtlas.zoomIn')}>+</button>
              <button type="button" className="atlas-map-control" onClick={() => handleZoom('out')} aria-label={t('networkAtlas.zoomOut')} title={t('networkAtlas.zoomOut')}>-</button>
              <button type="button" className="atlas-map-control atlas-map-control--wide" onClick={() => handleZoom('reset')} aria-label={t('networkAtlas.resetView')} title={t('networkAtlas.resetView')}>{t('networkAtlas.resetViewShort')}</button>
            </div>
            <ComposableMap
              projection="geoEqualEarth"
              projectionConfig={{ scale: 220 }}
              width={1200}
              height={560}
              className="atlas-world-map"
            >
              <ZoomableGroup
                center={mapCenter}
                zoom={mapZoom}
                minZoom={1}
                maxZoom={6}
                onMoveEnd={(position: any) => {
                  const coords = Array.isArray(position?.coordinates) ? position.coordinates : [0, 0]
                  setMapCenter([Number(coords[0]) || 0, Number(coords[1]) || 0])
                  setMapZoom(clampZoom(Number(position?.zoom) || 1))
                }}
              >
                <Sphere id="atlasSphere" stroke="rgba(110,128,168,0.22)" strokeWidth={0.8} fill="transparent" />
                <Geographies geography={worldGeo as any}>
                  {({ geographies }: { geographies: any[] }) =>
                    geographies.map((geo) => (
                      <Geography
                        key={geo.rsmKey}
                        geography={geo}
                        className="atlas-country-shape"
                        style={{
                          default: { outline: 'none' },
                          hover: { outline: 'none' },
                          pressed: { outline: 'none' }
                        }}
                      />
                    ))
                  }
                </Geographies>

                <AtlasConnections
                  localNode={localNode}
                  links={filteredLinks}
                  mapZoom={mapZoom}
                  selectedKey={selectedLink?.pubkey || ''}
                  onSelect={setSelectedKey}
                  onActivate={handlePeerActivate}
                  onOpenChannel={handleOpenChannel}
                />

                        {filteredLinks.map((link) => {
                  if (typeof link.lon !== 'number' || typeof link.lat !== 'number') return null
                  const selected = selectedLink?.pubkey === link.pubkey
                  const dimmed = Boolean(selectedKey) && !selected
                  return (
                    <Marker key={`marker-${link.pubkey}`} coordinates={[link.lon, link.lat]}>
                      <g
                        transform={`scale(${1 / mapZoom})`}
                        className={`cursor-pointer ${selected ? 'atlas-link-state atlas-link-state--selected' : dimmed ? 'atlas-link-state atlas-link-state--dimmed' : 'atlas-link-state'}`}
                        onPointerEnter={() => setSelectedKey(link.pubkey)}
                        onFocus={() => setSelectedKey(link.pubkey)}
                        onClick={() => handlePeerActivate(link)}
                        onDoubleClick={() => handleOpenChannel(link)}
                      >
                        <circle r="13" className="atlas-dot-hit" />
                        {focusPulseKey === link.pubkey && <circle r="9" className="atlas-focus-ring" />}
                        <circle
                          r={selected ? 5.2 : link.connection_kind === 'channel' ? 3.9 : 3.1}
                          className={link.connection_kind === 'channel' ? 'atlas-dot atlas-dot--channel' : 'atlas-dot atlas-dot--peer'}
                        />
                      </g>
                    </Marker>
                  )
                })}
              </ZoomableGroup>
            </ComposableMap>

            {!localNode?.location_set && (
              <div className="atlas-map-empty">
                <h4>{t('networkAtlas.locationNeededTitle')}</h4>
                <p>{t('networkAtlas.locationNeededBody')}</p>
                <button className="btn-primary" type="button" onClick={() => setSettingsOpen(true)}>
                  {t('networkAtlas.openLocationSettings')}
                </button>
              </div>
            )}
          </div>

          {localNode?.warnings?.length ? (
            <div className="atlas-map-note">
              {localNode.warnings.map((warning) => (
                <p key={warning}>{warning}</p>
              ))}
            </div>
          ) : null}
        </div>

        {selectedLink && (
          <div className="atlas-selected-bar mt-5">
            <div>
              <p className="atlas-map-label">{t('networkAtlas.selectedPeer')}</p>
              <h3 className="mt-2 text-lg font-semibold">{selectedLink.alias || shortPubkey(selectedLink.pubkey)}</h3>
              <p className="mt-1 text-xs text-fog/55 break-all">{selectedLink.pubkey}</p>
            </div>
            <div className="atlas-selected-chip">
              <span>{t('networkAtlas.connectionType')}</span>
              <strong>{selectedLink.connection_kind === 'channel' ? t('networkAtlas.legendChannel') : t('networkAtlas.legendPeer')}</strong>
            </div>
            <div className="atlas-selected-chip">
              <span>{t('networkAtlas.capacity')}</span>
              <strong>{selectedLink.capacity_sat > 0 ? formatSats(selectedLink.capacity_sat) : t('common.na')}</strong>
            </div>
            <div className="atlas-selected-chip">
              <span>{t('networkAtlas.location')}</span>
              <strong>{selectedLink.city && selectedLink.country ? `${selectedLink.city}, ${selectedLink.country}` : selectedLink.country || t('common.na')}</strong>
            </div>
          </div>
        )}

        {settingsOpen && (
          <div className="atlas-settings-card mt-5">
            <div className="max-w-2xl">
              <p className="atlas-map-label">{t('networkAtlas.settingsTitle')}</p>
              <p className="mt-2 text-sm text-fog/62">
                {config?.has_explicit_location ? t('networkAtlas.manualLocationActive') : t('networkAtlas.autoLocationActive')}
              </p>
            </div>
            <div className="mt-4 grid gap-3 md:grid-cols-3">
              <input
                className="input-field"
                value={labelInput}
                onChange={(event) => setLabelInput(event.target.value)}
                placeholder={t('networkAtlas.nodeLabelPlaceholder')}
              />
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
            <div className="mt-4 flex flex-wrap gap-3">
              <button className="btn-primary" type="button" onClick={handleSaveConfig} disabled={saving}>
                {saving ? t('common.saving') : t('common.save')}
              </button>
              <button className="atlas-control-pill" type="button" onClick={handleUseAutoLocation} disabled={saving}>
                {t('networkAtlas.useAutoLocation')}
              </button>
            </div>
          </div>
        )}
      </div>
    </section>
  )
}
