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
  render_lat?: number
  render_lon?: number
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
const clampLatitude = (value: number) => Math.min(85, Math.max(-85, value))
const normalizeLongitude = (value: number) => {
  let next = value
  while (next > 180) next -= 360
  while (next < -180) next += 360
  return next
}
const atlasCoordinateKey = (lat: number, lon: number) => `${lat.toFixed(3)}:${lon.toFixed(3)}`
const atlasChannelStrokeWidth = (capacitySat: number, emphasized: boolean) => {
  const capacity = Math.max(20_000, Number.isFinite(capacitySat) ? capacitySat : 0)
  const scale = Math.min(1, Math.max(0, (Math.log10(capacity) - Math.log10(20_000)) / 4))
  return 1.25 + (scale * 1.65) + (emphasized ? 0.65 : 0)
}
const spreadAtlasLinks = (links: AtlasLink[]) => {
  const groups = new Map<string, AtlasLink[]>()
  links.forEach((link) => {
    if (typeof link.lat !== 'number' || typeof link.lon !== 'number') return
    const key = atlasCoordinateKey(link.lat, link.lon)
    const group = groups.get(key)
    if (group) {
      group.push(link)
      return
    }
    groups.set(key, [link])
  })

  return links.map((link) => {
    if (typeof link.lat !== 'number' || typeof link.lon !== 'number') return link
    const group = groups.get(atlasCoordinateKey(link.lat, link.lon))
    if (!group || group.length <= 1) {
      return {
        ...link,
        render_lat: link.lat,
        render_lon: link.lon
      }
    }

    const index = group.findIndex((item) => item.pubkey === link.pubkey)
    const angle = ((Math.PI * 2) / group.length) * Math.max(0, index)
    const radiusLon = 0.8 + Math.min(group.length - 2, 4) * 0.14
    const radiusLat = 0.45 + Math.min(group.length - 2, 4) * 0.08

    return {
      ...link,
      render_lat: clampLatitude(link.lat + Math.sin(angle) * radiusLat),
      render_lon: normalizeLongitude(link.lon + Math.cos(angle) * radiusLon)
    }
  })
}
const LIGHTNING_OPS_ROUTE_KEY = 'lightning-ops'
const GRAPH_EXPLORER_ROUTE_KEY = 'graph-explorer'
const buildLightningOpsChannelHash = (channelPoint: string) =>
  `#${LIGHTNING_OPS_ROUTE_KEY}?channel_point=${encodeURIComponent(channelPoint)}`
const buildLightningOpsPeerHash = (pubkey: string) =>
  `#${LIGHTNING_OPS_ROUTE_KEY}?peer_pubkey=${encodeURIComponent(pubkey)}`
const buildGraphExplorerPeerHash = (pubkey: string) =>
  `#${GRAPH_EXPLORER_ROUTE_KEY}?pubkey=${encodeURIComponent(pubkey)}`

const AtlasConnections = ({
  localNode,
  links,
  mapZoom,
  selectedKey,
  hoveredKey,
  onHover,
  onHoverEnd,
  onActivate,
  onOpenChannel
}: {
  localNode: AtlasNode | null
  links: AtlasLink[]
  mapZoom: number
  selectedKey: string
  hoveredKey: string
  onHover: (key: string) => void
  onHoverEnd: (key: string) => void
  onActivate: (link: AtlasLink) => void
  onOpenChannel: (link: AtlasLink) => void
}) => {
  const { projection } = useMapContext()
  if (!localNode?.location_set) return null

  const origin = projection([localNode.lon, localNode.lat])
  if (!origin) return null

  const nodeHaloRadius = Math.max(7.5, 14 / Math.sqrt(Math.max(1, mapZoom)))
  const nodeCoreRadius = Math.max(3.6, 4.8 / Math.pow(Math.max(1, mapZoom), 0.22))

  return (
    <g>
      {links.map((link) => {
        const linkLon = typeof link.render_lon === 'number' ? link.render_lon : link.lon
        const linkLat = typeof link.render_lat === 'number' ? link.render_lat : link.lat
        if (typeof linkLon !== 'number' || typeof linkLat !== 'number') return null
        const destination = projection([linkLon, linkLat])
        if (!destination) return null

        const [x1, y1] = origin
        const [x2, y2] = destination
        const mx = (x1 + x2) / 2
        const my = (y1 + y2) / 2
        const arcHeight = Math.max(22, Math.abs(x2 - x1) * 0.14 + Math.abs(y2 - y1) * 0.08)
        const d = `M ${x1} ${y1} C ${mx} ${my - arcHeight}, ${mx} ${my - arcHeight}, ${x2} ${y2}`
        const selected = selectedKey === link.pubkey
        const hovered = hoveredKey === link.pubkey
        const dimmed = Boolean(selectedKey) && !selected
        const emphasized = selected || hovered
        const pathId = `atlas-arc-${link.pubkey}`

        return (
          <g
            key={link.pubkey}
            className={`cursor-pointer ${selected ? 'atlas-link-state atlas-link-state--selected' : hovered ? 'atlas-link-state atlas-link-state--hovered' : dimmed ? 'atlas-link-state atlas-link-state--dimmed' : 'atlas-link-state'}`}
            onPointerEnter={() => onHover(link.pubkey)}
            onPointerLeave={() => onHoverEnd(link.pubkey)}
          >
            <path
              d={d}
              className="atlas-arc-hit"
              strokeWidth={selected ? 18 : 14}
              onClick={(event) => {
                event.stopPropagation()
                onActivate(link)
              }}
              onDoubleClick={(event) => {
                event.preventDefault()
                event.stopPropagation()
                onOpenChannel(link)
              }}
            />
            <path
              id={pathId}
              d={d}
              className={`${link.connection_kind === 'channel' ? 'atlas-arc atlas-arc--channel' : 'atlas-arc atlas-arc--peer'}${link.connection_kind === 'channel' && !link.active ? ' atlas-arc--inactive' : ''}`}
              strokeWidth={link.connection_kind === 'channel' ? atlasChannelStrokeWidth(link.capacity_sat, emphasized) : emphasized ? 1.8 : 1.05}
            />
            {link.connection_kind === 'channel' && link.active && selected && (
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
        <circle r={nodeHaloRadius} />
        <circle r={nodeCoreRadius} className="atlas-node-anchor__core" />
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
  const [hoveredKey, setHoveredKey] = useState('')
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
          return ''
        })
        setHoveredKey('')
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
    const visible = filterMode === 'all'
      ? base
      : base.filter((item) => item.connection_kind === filterMode)
    return spreadAtlasLinks(visible)
  }, [filterMode, payload?.links])

  const selectedLink = useMemo(
    () => filteredLinks.find((item) => item.pubkey === selectedKey) || null,
    [filteredLinks, selectedKey]
  )
  const hoveredLink = useMemo(
    () => filteredLinks.find((item) => item.pubkey === hoveredKey) || null,
    [filteredLinks, hoveredKey]
  )
  const detailLink = selectedLink || hoveredLink

  const totalPeerCount = Math.max(0, Number(payload?.summary.total_peers) || 0)
  const unknownLocationCount = Math.max(0, Number(payload?.summary.unknown_location) || 0)
  const mappedPeerCount = Math.max(0, totalPeerCount - unknownLocationCount)
  const mappedCoveragePct = totalPeerCount
    ? Math.round((mappedPeerCount / totalPeerCount) * 100)
    : 0
  const mappedChannelCount = useMemo(
    () => (payload?.links ?? []).filter((item) => item.connection_kind === 'channel' && typeof item.lat === 'number' && typeof item.lon === 'number').length,
    [payload?.links]
  )
  const mappedPeerOnlyCount = useMemo(
    () => (payload?.links ?? []).filter((item) => item.connection_kind === 'peer' && typeof item.lat === 'number' && typeof item.lon === 'number').length,
    [payload?.links]
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
      setSelectedKey((current) => (nextPayload as AtlasResponse)?.links?.some((item) => item.pubkey === current) ? current : '')
      setHoveredKey('')
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
    const linkLon = typeof link.render_lon === 'number' ? link.render_lon : link.lon
    const linkLat = typeof link.render_lat === 'number' ? link.render_lat : link.lat
    if (typeof linkLon === 'number' && typeof linkLat === 'number') {
      setMapCenter([linkLon, linkLat])
      setMapZoom((current) => clampZoom(current < 2.4 ? 2.4 : current))
      setFocusPulseKey(link.pubkey)
      window.setTimeout(() => {
        setFocusPulseKey((current) => (current === link.pubkey ? '' : current))
      }, 1100)
    }
  }

  const handlePeerHoverEnd = (pubkey: string) => {
    setHoveredKey((current) => current === pubkey ? '' : current)
  }

  const handleFocusLocalNode = () => {
    if (!localNode?.location_set) return
    setMapCenter([localNode.lon, localNode.lat])
    setMapZoom(2.2)
  }

  const handleOpenChannel = (link: AtlasLink) => {
    const channelPoint = String(link.channel_point || '').trim()
    if (channelPoint) {
      window.location.hash = buildLightningOpsChannelHash(channelPoint)
      return
    }
    const peerPubkey = String(link.pubkey || '').trim()
    if (!peerPubkey) return
    window.location.hash = buildLightningOpsPeerHash(peerPubkey)
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
            {localNode && (
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
          <button className={`atlas-control-pill${filterMode === 'all' ? ' atlas-control-pill--active' : ''}`} type="button" aria-pressed={filterMode === 'all'} onClick={() => setFilterMode('all')}>
            {t('common.all')} <span className="atlas-filter-count">{mappedPeerCount}</span>
          </button>
          <button className={`atlas-control-pill${filterMode === 'channel' ? ' atlas-control-pill--active' : ''}`} type="button" aria-pressed={filterMode === 'channel'} onClick={() => setFilterMode('channel')}>
            {t('networkAtlas.channelsOnly')} <span className="atlas-filter-count">{mappedChannelCount}</span>
          </button>
          <button className={`atlas-control-pill${filterMode === 'peer' ? ' atlas-control-pill--active' : ''}`} type="button" aria-pressed={filterMode === 'peer'} onClick={() => setFilterMode('peer')}>
            {t('networkAtlas.peersOnly')} <span className="atlas-filter-count">{mappedPeerOnlyCount}</span>
          </button>
          <div className="ml-auto flex flex-wrap items-center gap-4 text-xs text-fog/65">
            <span className="atlas-legend-item"><i className="atlas-legend-dot atlas-legend-dot--channel" />{t('networkAtlas.legendChannel')}</span>
            <span className="atlas-legend-item"><i className="atlas-legend-dot atlas-legend-dot--peer" />{t('networkAtlas.legendPeer')}</span>
            <span className="atlas-legend-item"><i className="atlas-legend-dot atlas-legend-dot--inactive" />{t('common.inactive')}</span>
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
              <span>{t('networkAtlas.mappedCoverage')}</span>
              <strong>{mappedPeerCount.toLocaleString()} / {payload ? totalPeerCount.toLocaleString() : '--'}</strong>
              <small>{t('networkAtlas.coverageDetail', { percent: mappedCoveragePct, count: unknownLocationCount })}</small>
            </div>
          </div>

          <div className="atlas-world-stage">
            <div className="atlas-map-controls">
              <button type="button" className="atlas-map-control" onClick={() => handleZoom('in')} aria-label={t('networkAtlas.zoomIn')} title={t('networkAtlas.zoomIn')}>+</button>
              <button type="button" className="atlas-map-control" onClick={() => handleZoom('out')} aria-label={t('networkAtlas.zoomOut')} title={t('networkAtlas.zoomOut')}>-</button>
              {localNode?.location_set && <button type="button" className="atlas-map-control atlas-map-control--wide" onClick={handleFocusLocalNode} aria-label={t('networkAtlas.focusLocalNode')} title={t('networkAtlas.focusLocalNode')}>{t('networkAtlas.focusLocalNodeShort')}</button>}
              <button type="button" className="atlas-map-control atlas-map-control--wide" onClick={() => handleZoom('reset')} aria-label={t('networkAtlas.resetView')} title={t('networkAtlas.resetView')}>{t('networkAtlas.resetViewShort')}</button>
            </div>
            <ComposableMap
              projection="geoEqualEarth"
              projectionConfig={{ scale: 220 }}
              width={1200}
              height={560}
              className="atlas-world-map"
              onClick={() => setSelectedKey('')}
            >
              <ZoomableGroup
                center={mapCenter}
                zoom={mapZoom}
                minZoom={1}
                maxZoom={6}
                filterZoomEvent={(event: any) => event?.type !== 'dblclick'}
                onMoveEnd={(position: any) => {
                  const coords = Array.isArray(position?.coordinates) ? position.coordinates : [0, 0]
                  setMapCenter([Number(coords[0]) || 0, Number(coords[1]) || 0])
                  setMapZoom(clampZoom(Number(position?.zoom) || 1))
                }}
              >
                <Sphere id="atlasSphere" stroke="var(--atlas-sphere-stroke)" strokeWidth={0.8} fill="transparent" />
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
                  hoveredKey={hoveredLink?.pubkey || ''}
                  onHover={setHoveredKey}
                  onHoverEnd={handlePeerHoverEnd}
                  onActivate={handlePeerActivate}
                  onOpenChannel={handleOpenChannel}
                />

                {filteredLinks.map((link) => {
                  const linkLon = typeof link.render_lon === 'number' ? link.render_lon : link.lon
                  const linkLat = typeof link.render_lat === 'number' ? link.render_lat : link.lat
                  if (typeof linkLon !== 'number' || typeof linkLat !== 'number') return null
                  const selected = selectedLink?.pubkey === link.pubkey
                  const hovered = hoveredLink?.pubkey === link.pubkey
                  const dimmed = Boolean(selectedLink) && !selected
                  return (
                    <Marker key={`marker-${link.pubkey}`} coordinates={[linkLon, linkLat]}>
                      <g
                        transform={`scale(${1 / mapZoom})`}
                        className={`cursor-pointer ${selected ? 'atlas-link-state atlas-link-state--selected' : hovered ? 'atlas-link-state atlas-link-state--hovered' : dimmed ? 'atlas-link-state atlas-link-state--dimmed' : 'atlas-link-state'}`}
                        role="button"
                        tabIndex={0}
                        aria-label={link.alias || shortPubkey(link.pubkey)}
                        aria-pressed={selected}
                        onPointerEnter={() => setHoveredKey(link.pubkey)}
                        onPointerLeave={() => handlePeerHoverEnd(link.pubkey)}
                        onFocus={() => setHoveredKey(link.pubkey)}
                        onBlur={() => handlePeerHoverEnd(link.pubkey)}
                        onClick={(event) => {
                          event.stopPropagation()
                          handlePeerActivate(link)
                        }}
                        onKeyDown={(event) => {
                          if (event.key !== 'Enter' && event.key !== ' ') return
                          event.preventDefault()
                          event.stopPropagation()
                          handlePeerActivate(link)
                        }}
                        onDoubleClick={(event) => {
                          event.preventDefault()
                          event.stopPropagation()
                          handleOpenChannel(link)
                        }}
                      >
                        <circle r="13" className="atlas-dot-hit" />
                        {focusPulseKey === link.pubkey && <circle r="9" className="atlas-focus-ring" />}
                        <circle
                          r={selected ? 5.2 : link.connection_kind === 'channel' ? 3.9 : 3.1}
                          className={`${link.connection_kind === 'channel' ? 'atlas-dot atlas-dot--channel' : 'atlas-dot atlas-dot--peer'}${link.connection_kind === 'channel' && !link.active ? ' atlas-dot--inactive' : ''}`}
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

        {detailLink && (
          <div className="atlas-selected-bar mt-5">
            <div>
              <p className="atlas-map-label">{selectedLink ? t('networkAtlas.selectedPeer') : t('networkAtlas.previewPeer')}</p>
              <h3 className="mt-2 text-lg font-semibold">{detailLink.alias || shortPubkey(detailLink.pubkey)}</h3>
              <p className="mt-1 text-xs text-fog/55 break-all">{detailLink.pubkey}</p>
              <div className="mt-3 flex flex-wrap items-center gap-2">
                <a
                  href={buildGraphExplorerPeerHash(detailLink.pubkey)}
                  className="inline-flex items-center rounded-full border border-sky-300/25 bg-sky-500/10 px-3 py-1 text-xs text-sky-100 transition hover:border-sky-200/40 hover:text-white"
                >
                  {t('nav.graphExplorer')}
                </a>
                <button
                  type="button"
                  className="atlas-control-pill"
                  onClick={() => handleOpenChannel(detailLink)}
                >
                  {t('nav.lightningOps')}
                </button>
              </div>
            </div>
            <div className="atlas-selected-chip">
              <span>{t('networkAtlas.connectionType')}</span>
              <strong>{detailLink.connection_kind === 'channel' ? `${t('networkAtlas.legendChannel')} · ${detailLink.active ? t('common.active') : t('common.inactive')}` : t('networkAtlas.legendPeer')}</strong>
            </div>
            <div className="atlas-selected-chip">
              <span>{t('networkAtlas.capacity')}</span>
              <strong>{detailLink.capacity_sat > 0 ? formatSats(detailLink.capacity_sat) : t('common.na')}</strong>
            </div>
            <div className="atlas-selected-chip">
              <span>{t('networkAtlas.location')}</span>
              <strong>{detailLink.city && detailLink.country ? `${detailLink.city}, ${detailLink.country}` : detailLink.country || t('common.na')}</strong>
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
