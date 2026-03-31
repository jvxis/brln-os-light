import { useDeferredValue, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  APIError,
  getGraphExplorerNodeGeneral,
  getGraphExplorerStatus,
  recomputeGraphExplorer,
  searchGraphExplorerNodes
} from '../api'
import { getLocale } from '../i18n'
import clsx from '../utils/clsx'

type GraphExplorerStatus = {
  available?: boolean
  running?: boolean
  first_native_coverage_at?: string
  last_sync_at?: string
  last_error?: string
  node_count?: number
  open_channel_count?: number
  closed_channel_count?: number
}

type GraphExplorerSearchResult = {
  pubkey: string
  alias?: string
  color?: string
  channel_count: number
  total_capacity_sat: number
  last_seen_at?: string
}

type GraphExplorerSearchResponse = {
  query?: string
  coverage_since?: string
  items?: GraphExplorerSearchResult[]
}

type GraphExplorerNodeAddress = {
  network?: string
  addr?: string
}

type GraphExplorerNodeProfile = {
  pubkey: string
  alias?: string
  color?: string
  addresses?: GraphExplorerNodeAddress[]
  address_count: number
  clearnet_address_count: number
  onion_address_count: number
  channel_count: number
  open_channel_count: number
  peer_count: number
  total_capacity_sat: number
  smallest_channel_sat: number
  largest_channel_sat: number
  average_channel_size_sat: number
  oldest_channel_block: number
  youngest_channel_block: number
  first_seen_at?: string
  last_seen_at?: string
  last_graph_update_at?: string
  last_policy_update_at?: string
}

type GraphExplorerNodeGeneral = {
  coverage_since?: string
  source?: string
  node?: GraphExplorerNodeProfile
}

const GRAPH_EXPLORER_ROUTE_KEY = 'graph-explorer'
const GRAPH_EXPLORER_PUBKEY_PARAM = 'pubkey'

const shortPubkey = (value: string) => {
  const trimmed = String(value || '').trim()
  if (trimmed.length <= 18) return trimmed
  return `${trimmed.slice(0, 9)}...${trimmed.slice(-6)}`
}

const readGraphExplorerPubkey = () => {
  if (typeof window === 'undefined') return ''
  const rawHash = window.location.hash.startsWith('#')
    ? window.location.hash.slice(1)
    : window.location.hash
  if (!rawHash) return ''
  const queryIndex = rawHash.indexOf('?')
  if (queryIndex < 0) return ''
  if (rawHash.slice(0, queryIndex) !== GRAPH_EXPLORER_ROUTE_KEY) return ''
  const params = new URLSearchParams(rawHash.slice(queryIndex + 1))
  return (params.get(GRAPH_EXPLORER_PUBKEY_PARAM) || '').trim()
}

const buildGraphExplorerHash = (pubkey: string) =>
  `#${GRAPH_EXPLORER_ROUTE_KEY}?${GRAPH_EXPLORER_PUBKEY_PARAM}=${encodeURIComponent(pubkey)}`

const normalizeNodeColor = (value?: string) => {
  const trimmed = String(value || '').trim()
  return /^#[0-9a-fA-F]{6}$/.test(trimmed) ? trimmed : '#7dd3fc'
}

export default function GraphExplorer() {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const numberFormatter = useMemo(() => new Intl.NumberFormat(locale), [locale])
  const dateTimeFormatter = useMemo(
    () => new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }),
    [locale]
  )

  const [status, setStatus] = useState<GraphExplorerStatus | null>(null)
  const [statusError, setStatusError] = useState('')
  const [query, setQuery] = useState('')
  const [searchResults, setSearchResults] = useState<GraphExplorerSearchResult[]>([])
  const [searchLoading, setSearchLoading] = useState(false)
  const [searchError, setSearchError] = useState('')
  const [selectedPubkey, setSelectedPubkey] = useState(readGraphExplorerPubkey)
  const [general, setGeneral] = useState<GraphExplorerNodeGeneral | null>(null)
  const [generalLoading, setGeneralLoading] = useState(false)
  const [generalError, setGeneralError] = useState('')
  const [refreshing, setRefreshing] = useState(false)
  const deferredQuery = useDeferredValue(query)

  const formatSats = (value?: number) =>
    `${numberFormatter.format(Math.max(0, Math.round(Number(value || 0))))} sats`

  const formatInteger = (value?: number) =>
    numberFormatter.format(Math.max(0, Math.round(Number(value || 0))))

  const formatTimestamp = (value?: string) => {
    if (!value) return t('common.na')
    const parsed = new Date(value)
    if (Number.isNaN(parsed.getTime())) return value
    return dateTimeFormatter.format(parsed)
  }

  const formatBlock = (value?: number) => {
    const normalized = Math.max(0, Math.round(Number(value || 0)))
    if (!normalized) return t('common.na')
    return numberFormatter.format(normalized)
  }

  useEffect(() => {
    let active = true

    const loadStatus = async () => {
      try {
        const nextStatus = await getGraphExplorerStatus() as GraphExplorerStatus
        if (!active) return
        setStatus(nextStatus)
        setStatusError('')
      } catch (err: any) {
        if (!active) return
        setStatusError(err?.message || t('graphExplorer.statusLoadFailed'))
      }
    }

    const handleHashChange = () => {
      setSelectedPubkey(readGraphExplorerPubkey())
    }

    void loadStatus()
    window.addEventListener('hashchange', handleHashChange)
    const timer = window.setInterval(() => void loadStatus(), 60000)
    return () => {
      active = false
      window.clearInterval(timer)
      window.removeEventListener('hashchange', handleHashChange)
    }
  }, [t])

  useEffect(() => {
    const normalizedQuery = deferredQuery.trim()
    if (normalizedQuery.length < 2) {
      setSearchResults([])
      setSearchError('')
      setSearchLoading(false)
      return
    }

    let active = true
    setSearchLoading(true)
    setSearchError('')

    const timer = window.setTimeout(async () => {
      try {
        const response = await searchGraphExplorerNodes({ q: normalizedQuery, limit: 10 }) as GraphExplorerSearchResponse
        if (!active) return
        setSearchResults(Array.isArray(response?.items) ? response.items : [])
      } catch (err: any) {
        if (!active) return
        setSearchResults([])
        setSearchError(err?.message || t('graphExplorer.searchFailed'))
      } finally {
        if (!active) return
        setSearchLoading(false)
      }
    }, 220)

    return () => {
      active = false
      window.clearTimeout(timer)
    }
  }, [deferredQuery, t])

  useEffect(() => {
    if (!selectedPubkey) {
      setGeneral(null)
      setGeneralError('')
      setGeneralLoading(false)
      return
    }

    let active = true
    setGeneralLoading(true)
    setGeneralError('')

    void getGraphExplorerNodeGeneral(selectedPubkey)
      .then((response) => {
        if (!active) return
        const payload = response as GraphExplorerNodeGeneral
        setGeneral(payload)
        const aliasOrPubkey = String(payload?.node?.alias || payload?.node?.pubkey || selectedPubkey).trim()
        setQuery((current) => (current.trim() ? current : aliasOrPubkey))
      })
      .catch((err: any) => {
        if (!active) return
        setGeneral(null)
        if (err instanceof APIError && err.status === 404) {
          setGeneralError(t('graphExplorer.nodeNotFound'))
          return
        }
        setGeneralError(err?.message || t('graphExplorer.nodeLoadFailed'))
      })
      .finally(() => {
        if (!active) return
        setGeneralLoading(false)
      })

    return () => {
      active = false
    }
  }, [selectedPubkey, t])

  const handleSelectNode = (item: GraphExplorerSearchResult) => {
    const pubkey = String(item?.pubkey || '').trim()
    if (!pubkey) return
    setSelectedPubkey(pubkey)
    setQuery(String(item.alias || item.pubkey || '').trim())
    window.location.hash = buildGraphExplorerHash(pubkey)
  }

  const handleRefresh = async () => {
    setRefreshing(true)
    setStatusError('')
    try {
      const response: any = await recomputeGraphExplorer()
      const nextStatus = response?.status as GraphExplorerStatus | undefined
      if (nextStatus) {
        setStatus(nextStatus)
      } else {
        const fallbackStatus = await getGraphExplorerStatus() as GraphExplorerStatus
        setStatus(fallbackStatus)
      }
      if (selectedPubkey) {
        const payload = await getGraphExplorerNodeGeneral(selectedPubkey) as GraphExplorerNodeGeneral
        setGeneral(payload)
        setGeneralError('')
      }
      if (query.trim().length >= 2) {
        const response = await searchGraphExplorerNodes({ q: query.trim(), limit: 10 }) as GraphExplorerSearchResponse
        setSearchResults(Array.isArray(response?.items) ? response.items : [])
      }
    } catch (err: any) {
      setStatusError(err?.message || t('graphExplorer.refreshFailed'))
    } finally {
      setRefreshing(false)
    }
  }

  const node = general?.node || null
  const selectedColor = normalizeNodeColor(node?.color)
  const coverageSince = general?.coverage_since || status?.first_native_coverage_at || ''
  const statusBadgeClass = status?.running
    ? 'border-emerald-400/30 bg-emerald-500/10 text-emerald-200'
    : 'border-white/10 bg-white/[0.04] text-fog/75'
  const selectedResultKey = String(node?.pubkey || selectedPubkey || '').trim()
  const shouldShowSearchHint = deferredQuery.trim().length < 2
  const addressList = Array.isArray(node?.addresses) ? node.addresses : []

  const metricCards = node
    ? [
        { key: 'channelCount', label: t('graphExplorer.metrics.publicChannels'), value: formatInteger(node.channel_count) },
        { key: 'peerCount', label: t('graphExplorer.metrics.peerCount'), value: formatInteger(node.peer_count) },
        { key: 'totalCapacity', label: t('graphExplorer.metrics.totalCapacity'), value: formatSats(node.total_capacity_sat) },
        { key: 'smallestChannel', label: t('graphExplorer.metrics.smallestChannel'), value: formatSats(node.smallest_channel_sat) },
        { key: 'largestChannel', label: t('graphExplorer.metrics.largestChannel'), value: formatSats(node.largest_channel_sat) },
        { key: 'averageChannel', label: t('graphExplorer.metrics.averageChannel'), value: formatSats(node.average_channel_size_sat) },
        { key: 'lastPolicyUpdate', label: t('graphExplorer.metrics.lastPolicyUpdate'), value: formatTimestamp(node.last_policy_update_at) },
        { key: 'lastGraphUpdate', label: t('graphExplorer.metrics.lastGraphUpdate'), value: formatTimestamp(node.last_graph_update_at) }
      ]
    : []

  return (
    <div className="space-y-6">
      <section className="section-card space-y-6">
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div className="space-y-2">
            <p className="text-xs uppercase tracking-[0.22em] text-fog/50">{t('graphExplorer.kicker')}</p>
            <div>
              <h1 className="text-3xl font-semibold">{t('graphExplorer.title')}</h1>
              <p className="mt-2 max-w-3xl text-sm text-fog/70">{t('graphExplorer.subtitle')}</p>
            </div>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <span className="rounded-full border border-sky-400/25 bg-sky-500/10 px-3 py-1 text-xs font-medium text-sky-100">
              {t('graphExplorer.badges.nativeSource')}
            </span>
            <span className="rounded-full border border-white/10 bg-white/[0.04] px-3 py-1 text-xs font-medium text-fog/75">
              {t('graphExplorer.badges.coverageSince', { value: formatTimestamp(coverageSince) })}
            </span>
            <span className={clsx('rounded-full border px-3 py-1 text-xs font-medium', statusBadgeClass)}>
              {status?.running ? t('graphExplorer.badges.streaming') : t('graphExplorer.badges.idle')}
            </span>
            <button
              type="button"
              className={clsx('btn-primary', refreshing && 'opacity-70 cursor-wait')}
              onClick={handleRefresh}
              disabled={refreshing}
            >
              {refreshing ? t('graphExplorer.refreshing') : t('graphExplorer.refresh')}
            </button>
          </div>
        </div>

        <div className="grid gap-4 xl:grid-cols-[minmax(0,1.2fr)_minmax(18rem,0.8fr)]">
          <div className="rounded-[1.5rem] border border-white/10 bg-black/10 p-4">
            <label htmlFor="graph-explorer-search" className="text-sm font-medium text-fog/80">
              {t('graphExplorer.searchLabel')}
            </label>
            <div className="mt-3 flex flex-col gap-3 md:flex-row">
              <input
                id="graph-explorer-search"
                type="search"
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder={t('graphExplorer.searchPlaceholder')}
                className="min-w-0 flex-1 rounded-2xl border border-white/10 bg-white/[0.04] px-4 py-3 text-sm text-fog outline-none transition placeholder:text-fog/35 focus:border-sky-300/40 focus:bg-white/[0.06]"
                autoComplete="off"
                spellCheck={false}
              />
              {selectedPubkey && (
                <button
                  type="button"
                  className="rounded-2xl border border-white/10 bg-white/[0.04] px-4 py-3 text-sm text-fog/80 transition hover:border-white/20 hover:text-white"
                  onClick={() => {
                    setSelectedPubkey('')
                    setGeneral(null)
                    setGeneralError('')
                    window.location.hash = `#${GRAPH_EXPLORER_ROUTE_KEY}`
                  }}
                >
                  {t('graphExplorer.clearSelection')}
                </button>
              )}
            </div>
            <p className="mt-3 text-sm text-fog/60">
              {shouldShowSearchHint ? t('graphExplorer.searchHint') : t('graphExplorer.searchLiveHint')}
            </p>
            {searchError && (
              <p className="mt-3 text-sm text-amber-200">{searchError}</p>
            )}
            <div className="mt-4 space-y-2">
              {searchLoading && (
                <p className="text-sm text-fog/60">{t('graphExplorer.searching')}</p>
              )}
              {!searchLoading && !searchError && !shouldShowSearchHint && searchResults.length === 0 && (
                <p className="text-sm text-fog/60">{t('graphExplorer.searchEmpty')}</p>
              )}
              {searchResults.map((item) => {
                const isSelected = String(item.pubkey || '').trim() === selectedResultKey
                return (
                  <button
                    key={item.pubkey}
                    type="button"
                    onClick={() => handleSelectNode(item)}
                    className={clsx(
                      'w-full rounded-[1.35rem] border px-4 py-3 text-left transition',
                      isSelected
                        ? 'border-sky-300/35 bg-sky-500/10 shadow-[0_0_0_1px_rgba(125,211,252,0.12)]'
                        : 'border-white/10 bg-white/[0.03] hover:border-white/20 hover:bg-white/[0.05]'
                    )}
                  >
                    <div className="flex flex-wrap items-start justify-between gap-3">
                      <div className="min-w-0">
                        <div className="flex items-center gap-2">
                          <span
                            className="h-3 w-3 rounded-full border border-white/10"
                            style={{ backgroundColor: normalizeNodeColor(item.color) }}
                          />
                          <span className="truncate font-medium text-fog">
                            {item.alias || t('graphExplorer.aliasFallback')}
                          </span>
                        </div>
                        <p className="mt-1 truncate text-xs text-fog/55">{item.pubkey}</p>
                      </div>
                      <div className="text-right text-xs text-fog/65">
                        <p>{t('graphExplorer.searchResultChannels', { count: item.channel_count })}</p>
                        <p className="mt-1">{formatSats(item.total_capacity_sat)}</p>
                      </div>
                    </div>
                  </button>
                )
              })}
            </div>
          </div>

          <div className="rounded-[1.5rem] border border-white/10 bg-black/10 p-4">
            <p className="text-sm font-medium text-fog/80">{t('graphExplorer.indexStatus')}</p>
            <div className="mt-4 grid gap-3 sm:grid-cols-3 xl:grid-cols-1">
              <div className="rounded-2xl border border-white/10 bg-white/[0.03] p-4">
                <p className="text-xs uppercase tracking-[0.18em] text-fog/45">{t('graphExplorer.statusCards.nodes')}</p>
                <p className="mt-2 text-2xl font-semibold">{formatInteger(status?.node_count)}</p>
              </div>
              <div className="rounded-2xl border border-white/10 bg-white/[0.03] p-4">
                <p className="text-xs uppercase tracking-[0.18em] text-fog/45">{t('graphExplorer.statusCards.openChannels')}</p>
                <p className="mt-2 text-2xl font-semibold">{formatInteger(status?.open_channel_count)}</p>
              </div>
              <div className="rounded-2xl border border-white/10 bg-white/[0.03] p-4">
                <p className="text-xs uppercase tracking-[0.18em] text-fog/45">{t('graphExplorer.statusCards.lastSync')}</p>
                <p className="mt-2 text-sm font-medium text-fog">{formatTimestamp(status?.last_sync_at)}</p>
              </div>
            </div>
            {statusError && (
              <p className="mt-4 text-sm text-amber-200">{statusError}</p>
            )}
            {!statusError && status?.last_error && (
              <p className="mt-4 text-sm text-amber-200">{status.last_error}</p>
            )}
            {!statusError && Number(status?.node_count || 0) === 0 && (
              <p className="mt-4 text-sm text-fog/60">{t('graphExplorer.emptyIndex')}</p>
            )}
          </div>
        </div>
      </section>

      <section className="section-card space-y-5">
        <div className="flex flex-wrap items-center gap-2">
          <span className="rounded-full border border-sky-400/30 bg-sky-500/12 px-4 py-2 text-sm font-medium text-sky-100">
            {t('graphExplorer.tabs.general')}
          </span>
          <span className="rounded-full border border-white/10 bg-white/[0.03] px-4 py-2 text-sm text-fog/50">
            {t('graphExplorer.tabs.channels')}
          </span>
          <span className="rounded-full border border-white/10 bg-white/[0.03] px-4 py-2 text-sm text-fog/50">
            {t('graphExplorer.tabs.closed')}
          </span>
          <span className="rounded-full border border-white/10 bg-white/[0.03] px-4 py-2 text-sm text-fog/50">
            {t('graphExplorer.tabs.fees')}
          </span>
        </div>

        {generalLoading ? (
          <div className="rounded-[1.5rem] border border-white/10 bg-white/[0.03] p-6 text-fog/70">
            {t('graphExplorer.loadingNode')}
          </div>
        ) : generalError ? (
          <div className="rounded-[1.5rem] border border-amber-400/25 bg-amber-500/10 p-6 text-amber-100">
            {generalError}
          </div>
        ) : !node ? (
          <div className="rounded-[1.5rem] border border-white/10 bg-white/[0.03] p-6">
            <h2 className="text-lg font-semibold">{t('graphExplorer.emptySelectionTitle')}</h2>
            <p className="mt-2 max-w-2xl text-sm text-fog/65">{t('graphExplorer.emptySelectionBody')}</p>
          </div>
        ) : (
          <>
            <div className="rounded-[1.75rem] border border-white/10 bg-[radial-gradient(circle_at_top_left,rgba(56,189,248,0.14),transparent_38%),linear-gradient(180deg,rgba(255,255,255,0.03),rgba(255,255,255,0.015))] p-6">
              <div className="flex flex-col gap-6 xl:flex-row xl:items-start xl:justify-between">
                <div className="flex min-w-0 gap-4">
                  <div
                    className="grid h-16 w-16 flex-none place-items-center rounded-2xl border border-white/10 text-xl font-semibold text-slate-950"
                    style={{ backgroundColor: selectedColor }}
                  >
                    {String(node.alias || node.pubkey || '?').trim().slice(0, 1).toUpperCase()}
                  </div>
                  <div className="min-w-0 space-y-2">
                    <div>
                      <h2 className="truncate text-2xl font-semibold text-fog">
                        {node.alias || t('graphExplorer.aliasFallback')}
                      </h2>
                      <p className="mt-2 break-all font-mono text-xs text-fog/62">{node.pubkey}</p>
                    </div>
                    <div className="flex flex-wrap gap-2">
                      <span className="rounded-full border border-white/10 bg-white/[0.04] px-3 py-1 text-xs text-fog/75">
                        {t('graphExplorer.header.source', {
                          value: general?.source === 'native'
                            ? t('graphExplorer.badges.nativeSource')
                            : general?.source || t('common.unknown')
                        })}
                      </span>
                      <span className="rounded-full border border-white/10 bg-white/[0.04] px-3 py-1 text-xs text-fog/75">
                        {t('graphExplorer.header.coverage', { value: formatTimestamp(coverageSince) })}
                      </span>
                      <span className="rounded-full border border-white/10 bg-white/[0.04] px-3 py-1 text-xs text-fog/75">
                        {t('graphExplorer.header.lastSeen', { value: formatTimestamp(node.last_seen_at) })}
                      </span>
                    </div>
                  </div>
                </div>

                <div className="grid gap-3 sm:grid-cols-2 xl:min-w-[28rem]">
                  <div className="rounded-2xl border border-white/10 bg-black/15 p-4">
                    <p className="text-xs uppercase tracking-[0.18em] text-fog/45">{t('graphExplorer.header.publicChannels')}</p>
                    <p className="mt-2 text-2xl font-semibold">{formatInteger(node.channel_count)}</p>
                  </div>
                  <div className="rounded-2xl border border-white/10 bg-black/15 p-4">
                    <p className="text-xs uppercase tracking-[0.18em] text-fog/45">{t('graphExplorer.header.totalCapacity')}</p>
                    <p className="mt-2 text-2xl font-semibold">{formatSats(node.total_capacity_sat)}</p>
                  </div>
                  <div className="rounded-2xl border border-white/10 bg-black/15 p-4">
                    <p className="text-xs uppercase tracking-[0.18em] text-fog/45">{t('graphExplorer.header.addresses')}</p>
                    <p className="mt-2 text-2xl font-semibold">{formatInteger(node.address_count)}</p>
                  </div>
                  <div className="rounded-2xl border border-white/10 bg-black/15 p-4">
                    <p className="text-xs uppercase tracking-[0.18em] text-fog/45">{t('graphExplorer.header.lastPolicyUpdate')}</p>
                    <p className="mt-2 text-sm font-medium text-fog">{formatTimestamp(node.last_policy_update_at)}</p>
                  </div>
                </div>
              </div>

              <div className="mt-6 flex flex-wrap gap-2">
                {addressList.length === 0 ? (
                  <span className="rounded-full border border-white/10 bg-white/[0.04] px-3 py-1 text-xs text-fog/60">
                    {t('graphExplorer.noAddresses')}
                  </span>
                ) : (
                  addressList.map((address, index) => (
                    <span
                      key={`${address.network || 'addr'}-${address.addr || index}`}
                      className="rounded-full border border-white/10 bg-white/[0.04] px-3 py-1 text-xs text-fog/75"
                    >
                      {String(address.network || '').trim() ? `${address.network}: ` : ''}
                      {address.addr || shortPubkey(node.pubkey)}
                    </span>
                  ))
                )}
              </div>
            </div>

            <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
              {metricCards.map((item) => (
                <div key={item.key} className="rounded-[1.35rem] border border-white/10 bg-white/[0.03] p-4">
                  <p className="text-xs uppercase tracking-[0.18em] text-fog/45">{item.label}</p>
                  <p className="mt-3 text-lg font-semibold text-fog">{item.value}</p>
                </div>
              ))}
            </div>

            <div className="grid gap-4 lg:grid-cols-2">
              <div className="rounded-[1.35rem] border border-white/10 bg-white/[0.03] p-5">
                <p className="text-xs uppercase tracking-[0.18em] text-fog/45">{t('graphExplorer.sections.presence')}</p>
                <dl className="mt-4 space-y-4">
                  <div className="flex items-center justify-between gap-4">
                    <dt className="text-sm text-fog/65">{t('graphExplorer.fields.firstSeen')}</dt>
                    <dd className="text-sm font-medium text-fog">{formatTimestamp(node.first_seen_at)}</dd>
                  </div>
                  <div className="flex items-center justify-between gap-4">
                    <dt className="text-sm text-fog/65">{t('graphExplorer.fields.lastSeen')}</dt>
                    <dd className="text-sm font-medium text-fog">{formatTimestamp(node.last_seen_at)}</dd>
                  </div>
                  <div className="flex items-center justify-between gap-4">
                    <dt className="text-sm text-fog/65">{t('graphExplorer.fields.oldestChannelBlock')}</dt>
                    <dd className="text-sm font-medium text-fog">{formatBlock(node.oldest_channel_block)}</dd>
                  </div>
                  <div className="flex items-center justify-between gap-4">
                    <dt className="text-sm text-fog/65">{t('graphExplorer.fields.youngestChannelBlock')}</dt>
                    <dd className="text-sm font-medium text-fog">{formatBlock(node.youngest_channel_block)}</dd>
                  </div>
                </dl>
              </div>

              <div className="rounded-[1.35rem] border border-white/10 bg-white/[0.03] p-5">
                <p className="text-xs uppercase tracking-[0.18em] text-fog/45">{t('graphExplorer.sections.addressSummary')}</p>
                <dl className="mt-4 space-y-4">
                  <div className="flex items-center justify-between gap-4">
                    <dt className="text-sm text-fog/65">{t('graphExplorer.fields.addressCount')}</dt>
                    <dd className="text-sm font-medium text-fog">{formatInteger(node.address_count)}</dd>
                  </div>
                  <div className="flex items-center justify-between gap-4">
                    <dt className="text-sm text-fog/65">{t('graphExplorer.fields.clearnetAddresses')}</dt>
                    <dd className="text-sm font-medium text-fog">{formatInteger(node.clearnet_address_count)}</dd>
                  </div>
                  <div className="flex items-center justify-between gap-4">
                    <dt className="text-sm text-fog/65">{t('graphExplorer.fields.onionAddresses')}</dt>
                    <dd className="text-sm font-medium text-fog">{formatInteger(node.onion_address_count)}</dd>
                  </div>
                  <div className="flex items-center justify-between gap-4">
                    <dt className="text-sm text-fog/65">{t('graphExplorer.fields.selectedPubkey')}</dt>
                    <dd className="truncate text-sm font-medium text-fog">{shortPubkey(node.pubkey)}</dd>
                  </div>
                </dl>
              </div>
            </div>
          </>
        )}
      </section>
    </div>
  )
}
