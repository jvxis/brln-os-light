import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  acceptMagmaOrder,
  applyMagmaBackfill,
  getMagmaOpenPreview,
  getMagmaOverview,
  openMagmaChannel,
  previewMagmaBackfill,
  getMagmaEvents,
  getMagmaOffers,
  refreshMagma,
  rejectMagmaOrder,
  saveMagmaOffer,
  toggleMagmaOffer,
  updateMagmaPolicy,
  updateMagmaSettings
} from '../api'
import type {
  MagmaBackfillReport,
  MagmaOpenPreview,
  MagmaOffer,
  MagmaOfferCondition,
  MagmaOffersView,
  MagmaOrder,
  MagmaOrderEvent,
  MagmaOverview,
  MagmaPolicy
} from '../api'
import { getLocale } from '../i18n'

const BLOCKS_PER_DAY = 144

// Statuses where the seller is the one holding up the order.
const actionableStatuses = new Set(['WAITING_FOR_SELLER_APPROVAL', 'WAITING_FOR_CHANNEL_OPEN'])

// Failures Amboss records against the seller account.
const sellerFailureStatuses = new Set([
  'SELLER_FAILED_TO_REACT',
  'SELLER_FAILED_TO_OPEN_CHANNEL',
  'SELLER_FAILED_TO_SEND_SWAP',
  'INVALID_CHANNEL_OPENING'
])

const statusStyle = (status: string) => {
  if (actionableStatuses.has(status)) return 'bg-amber-500/15 text-amber-200 border border-amber-400/30'
  if (sellerFailureStatuses.has(status)) return 'bg-rose-500/15 text-rose-200 border border-rose-400/30'
  if (status === 'CHANNEL_MONITORING_FINISHED' || status === 'VALID_CHANNEL_OPENING') {
    return 'bg-emerald-500/15 text-emerald-200 border border-emerald-400/30'
  }
  return 'bg-white/10 text-fog/60 border border-white/10'
}

const formatSats = (value: number, locale: string) => `${value.toLocaleString(locale)} sat`

const formatDays = (blocks: number) => {
  if (!blocks || blocks <= 0) return '-'
  return `${Math.round(blocks / BLOCKS_PER_DAY)}d`
}

const shortPubkey = (pubkey: string) => {
  if (!pubkey || pubkey.length <= 16) return pubkey || '-'
  return `${pubkey.slice(0, 8)}…${pubkey.slice(-8)}`
}

// Deep link into the graph explorer, which reads the pubkey from the hash query.
const graphExplorerHref = (pubkey: string) =>
  `#graph-explorer?pubkey=${encodeURIComponent(pubkey)}`

// A pubkey says nothing about who the buyer is, so the alias leads when we have
// it and the short pubkey stays as the fallback and the tooltip.
function BuyerLink({ order }: { order: MagmaOrder }) {
  if (!order.buyer_pubkey) return <span className="text-fog/50">-</span>
  const label = order.buyer_alias?.trim() || shortPubkey(order.buyer_pubkey)
  return (
    <a
      className="text-glow hover:underline"
      href={graphExplorerHref(order.buyer_pubkey)}
      title={order.buyer_pubkey}
    >
      {label}
    </a>
  )
}

const formatDateTime = (value: string | undefined, locale: string) => {
  if (!value) return '-'
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return '-'
  return parsed.toLocaleString(locale)
}

// The four positions of the mode switch, ordered by how much the app is allowed
// to do. The active tint escalates with that: off, neutral, amber, red.
// "off" is not a stored mode — it maps onto the app's enabled flag, so the
// switch and the App Store card can never disagree about what is running.
const magmaModes = [
  { value: 'off' as const, labelKey: 'magma.modeOffShort', activeClass: 'bg-white/10 text-fog/70' },
  { value: 'monitor' as const, labelKey: 'magma.modeMonitorShort', activeClass: 'bg-white/15 text-fog' },
  { value: 'assisted' as const, labelKey: 'magma.modeAssistedShort', activeClass: 'bg-amber-500/25 text-amber-100' },
  { value: 'auto' as const, labelKey: 'magma.modeAutoShort', activeClass: 'bg-rose-500/25 text-rose-100' }
]

type MagmaSwitchPosition = 'off' | 'monitor' | 'assisted' | 'auto'

// Price alone hides the deal: the same ppm over 180 days is a very different
// trade than over 7. Commitment length is the most common size in real orders.
const pricePerDayPPM = (order: MagmaOrder) => {
  if (!order.commitment_blocks || order.commitment_blocks <= 0) return null
  const days = order.commitment_blocks / BLOCKS_PER_DAY
  if (days <= 0) return null
  return order.price_ppm / days
}

export default function MagmaSales() {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const [overview, setOverview] = useState<MagmaOverview | null>(null)
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState(false)
  const [expanded, setExpanded] = useState<string | null>(null)
  const [pendingMode, setPendingMode] = useState<'assisted' | 'auto' | null>(null)
  const [policyOpen, setPolicyOpen] = useState(false)
  const [backfillOpen, setBackfillOpen] = useState(false)
  const [events, setEvents] = useState<MagmaOrderEvent[]>([])
  const [offers, setOffers] = useState<MagmaOffersView | null>(null)
  const [offersError, setOffersError] = useState('')
  const [offerOpen, setOfferOpen] = useState(false)
  const [offerDraft, setOfferDraft] = useState<MagmaOffer | null>(null)
  const [offersOpen, setOffersOpen] = useState(false)

  const load = () => {
    getMagmaOverview()
      .then((data) => {
        setOverview(data)
        setMessage('')
      })
      .catch((err) => {
        setMessage(err instanceof Error ? err.message : t('magma.loadFailed'))
      })
    // Failing to load the timeline must not blank the page; it is context, not state.
    getMagmaEvents(60)
      .then((data) => setEvents(data.events ?? []))
      .catch(() => undefined)
    // Offers come from Amboss, so this is the one panel that can fail while the
    // rest of the page is fine. Its error is shown in place, not page-wide.
    getMagmaOffers()
      .then((data) => {
        setOffers(data)
        setOffersError('')
      })
      .catch((err) => setOffersError(err instanceof Error ? err.message : String(err)))
  }

  const handleToggleOffer = (offerID: string) => {
    if (!offerID) return
    setBusy(true)
    setOffersError('')
    toggleMagmaOffer(offerID)
      .then((view) => setOffers(view))
      .catch((err) => setOffersError(err instanceof Error ? err.message : String(err)))
      .finally(() => setBusy(false))
  }

  useEffect(() => {
    load()
    const timer = setInterval(load, 30000)
    return () => clearInterval(timer)
  }, [])

  const handleRefresh = () => {
    setBusy(true)
    setMessage('')
    refreshMagma()
      .then((data) => setOverview(data))
      .catch((err) => setMessage(err instanceof Error ? err.message : t('magma.refreshFailed')))
      .finally(() => setBusy(false))
  }

  const handleToggleTelegram = () => {
    if (!overview) return
    const next = !overview.settings.notify_telegram
    setBusy(true)
    updateMagmaSettings({ notify_telegram: next })
      .then((settings) => setOverview({ ...overview, settings }))
      .catch((err) => setMessage(err instanceof Error ? err.message : t('magma.settingsFailed')))
      .finally(() => setBusy(false))
  }

  // Leaving monitor mode is the point where this app becomes able to spend, so it
  // goes through a modal that states what changes and what the wallet can back.
  // Dropping straight back to monitor is always safe and needs no ceremony.
  const handleChangeMode = (next: MagmaSwitchPosition) => {
    if (!overview || next === position) return
    // Off and monitor only ever reduce what the app may do, so they apply
    // straight away. The two that can spend go through the confirmation.
    if (next === 'off' || next === 'monitor') {
      commitMode(next)
      return
    }
    setPendingMode(next)
  }

  const commitMode = (next: MagmaSwitchPosition) => {
    if (!overview) return
    setPendingMode(null)
    setBusy(true)
    const payload =
      next === 'off'
        ? { enabled: false }
        : { enabled: true, mode: next as 'monitor' | 'assisted' | 'auto' }
    updateMagmaSettings(payload)
      .then((settings) => setOverview({ ...overview, settings }))
      .catch((err) => setMessage(err instanceof Error ? err.message : t('magma.settingsFailed')))
      .finally(() => setBusy(false))
  }

  const orders = overview?.orders ?? []
  const actionNeeded = overview?.action_needed ?? []
  const mode = overview?.settings.mode ?? 'monitor'
  // The switch shows Off whenever the app is stopped, whatever mode is stored —
  // that stored mode is what it returns to when switched back on.
  const off = overview ? !overview.settings.enabled : false
  const position: MagmaSwitchPosition = off ? 'off' : (mode as MagmaSwitchPosition)
  const auto = !off && mode === 'auto'
  // Assisted is the only mode with per-order buttons: in auto the engine owns the
  // decisions, and a manual click racing it would fight the policy.
  const assisted = !off && mode === 'assisted'
  const canAct = assisted || auto

  const stats = useMemo(() => {
    const sold = orders.filter((order) => order.channel_point)
    const revenue = sold.reduce((total, order) => total + order.revenue_sat, 0)
    const capViolations = orders.filter((order) => (order.fee_above_cap_seconds ?? 0) > 0).length
    // Early closes are dominated by the buyer, not by our own automations.
    const closedEarly = orders.filter((order) => (order.closed_blocks_before_min ?? 0) > 0).length
    return { total: orders.length, sold: sold.length, revenue, capViolations, closedEarly }
  }, [orders])

  const offersActiveCount = (offers?.offers ?? []).filter((o) => o.status === 'ENABLED').length
  // Active first: they are the only ones that can produce an order.
  const sortedOffers = useMemo(
    () =>
      (offers?.offers ?? [])
        .slice()
        .sort((a, b) => Number(b.status === 'ENABLED') - Number(a.status === 'ENABLED')),
    [offers]
  )

  const token = overview?.token
  const settings = overview?.settings

  return (
    <div className="space-y-6">
      <section className="section-card space-y-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h2 className="text-lg font-semibold text-fog">{t('magma.title')}</h2>
            <p className="text-sm text-fog/60">{t('magma.subtitle')}</p>
          </div>
          <div className="flex flex-wrap items-center gap-3">
            {/* The switch is the mode indicator as well as the control: the active
                segment is tinted by how much the mode is allowed to do, which is
                why there is no separate badge repeating it. */}
            <div
              role="radiogroup"
              aria-label={t('magma.modeLabel')}
              className={`inline-flex items-center rounded-full border p-1 transition-colors ${
                auto
                  ? 'border-rose-400/30 bg-rose-500/10'
                  : assisted
                    ? 'border-amber-400/30 bg-amber-500/10'
                    : off
                      ? 'border-white/5 bg-white/[0.02]'
                      : 'border-white/10 bg-white/5'
              }`}
            >
              {magmaModes.map((option) => {
                const active = position === option.value
                return (
                  <button
                    key={option.value}
                    type="button"
                    role="radio"
                    aria-checked={active}
                    disabled={busy || !settings}
                    onClick={() => handleChangeMode(option.value)}
                    className={`rounded-full px-3 py-1.5 text-xs font-semibold uppercase tracking-wide transition-colors disabled:opacity-50 ${
                      active ? option.activeClass : 'text-fog/45 hover:text-fog/80'
                    }`}
                  >
                    {t(option.labelKey)}
                  </button>
                )
              })}
            </div>
            <button className="btn-secondary" onClick={handleRefresh} disabled={busy}>
              {busy ? t('magma.refreshing') : t('magma.refresh')}
            </button>
          </div>
        </div>

        <p className="text-sm text-fog/70">
          {off
            ? t('magma.offNotice')
            : auto
              ? t('magma.autoNotice')
              : assisted
                ? t('magma.assistedNotice')
                : t('magma.monitorNotice')}
        </p>
        {/* Shown in assisted mode too, so the policy can be reviewed and tuned
            before automatic mode is switched on rather than after it starts
            spending. */}
        {canAct && overview?.policy_summary && (
          <div className="flex flex-wrap items-center gap-3">
            <p className="text-xs text-fog/50">
              {auto ? t('magma.policyPrefix') : t('magma.policyPreviewPrefix')} {overview.policy_summary}
            </p>
            <button className="btn-secondary text-xs px-3 py-1.5" onClick={() => setPolicyOpen(true)}>
              {t('magma.editPolicy')}
            </button>
          </div>
        )}

        {token && !token.configured && (
          <div className="rounded-lg border border-amber-400/30 bg-amber-500/10 px-4 py-3 text-sm text-amber-100">
            {t('magma.tokenMissing')}
          </div>
        )}
        {token?.expired && (
          <div className="rounded-lg border border-rose-400/30 bg-rose-500/10 px-4 py-3 text-sm text-rose-100">
            {t('magma.tokenExpired')}
          </div>
        )}
        {token?.expiring_soon && !token.expired && (
          <div className="rounded-lg border border-amber-400/30 bg-amber-500/10 px-4 py-3 text-sm text-amber-100">
            {t('magma.tokenExpiringSoon', { days: token.days_to_expiry ?? 0 })}
          </div>
        )}
        {overview?.token_warning && !token?.expired && (
          <div className="rounded-lg border border-amber-400/30 bg-amber-500/10 px-4 py-3 text-sm text-amber-100">
            {overview.token_warning}
          </div>
        )}
        {canAct && overview?.capacity && (
          <div className="rounded-lg border border-white/10 bg-white/5 px-4 py-3 text-sm text-fog/70">
            <span className="font-semibold text-fog">{t('magma.capacityTitle')}</span>{' '}
            {t('magma.capacityBody', {
              available: overview.capacity.available_sat.toLocaleString(locale),
              confirmed: overview.capacity.confirmed_sat.toLocaleString(locale)
            })}
            {overview.capacity.committed_sat > 0 && (
              <span className="block text-xs text-amber-200">
                {t('magma.capacityCommitted', {
                  committed: overview.capacity.committed_sat.toLocaleString(locale),
                  count: overview.capacity.committed_orders
                })}
              </span>
            )}
          </div>
        )}
        {overview?.last_sync_error && (
          <div className="rounded-lg border border-rose-400/30 bg-rose-500/10 px-4 py-3 text-sm text-rose-100">
            {overview.last_sync_error}
          </div>
        )}
        {message && <p className="text-sm text-rose-200">{message}</p>}

        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <div className="rounded-lg border border-white/10 bg-white/5 px-4 py-3">
            <p className="text-xs uppercase tracking-wide text-fog/50">{t('magma.statOrders')}</p>
            <p className="text-xl font-semibold text-fog">{stats.total}</p>
          </div>
          <div className="rounded-lg border border-white/10 bg-white/5 px-4 py-3">
            <p className="text-xs uppercase tracking-wide text-fog/50">{t('magma.statSold')}</p>
            <p className="text-xl font-semibold text-fog">{stats.sold}</p>
          </div>
          <div className="rounded-lg border border-white/10 bg-white/5 px-4 py-3">
            <p className="text-xs uppercase tracking-wide text-fog/50">{t('magma.statRevenue')}</p>
            <p className="text-xl font-semibold text-fog">{formatSats(stats.revenue, locale)}</p>
          </div>
          <div className="rounded-lg border border-white/10 bg-white/5 px-4 py-3">
            <p className="text-xs uppercase tracking-wide text-fog/50">{t('magma.statCapViolations')}</p>
            <p className="text-xl font-semibold text-fog">{stats.capViolations}</p>
            <p className="text-xs text-fog/50">{t('magma.statCapViolationsHint')}</p>
          </div>
        </div>

        <div className="flex flex-wrap items-center justify-between gap-3 text-sm text-fog/70">
          <span>
            {t('magma.lastSync')}: {formatDateTime(overview?.last_sync_at, locale)}
            {settings ? ` · ${t('magma.pollInterval', { seconds: settings.poll_interval_sec })}` : ''}
          </span>
          <label className="flex items-center gap-2">
            <input
              type="checkbox"
              checked={Boolean(settings?.notify_telegram)}
              onChange={handleToggleTelegram}
              disabled={busy || !settings}
            />
            {t('magma.notifyTelegram')}
          </label>
        </div>
      </section>

      {overview?.pnl && (overview.pnl.sales_count > 0 || overview.pnl.pending_count > 0) && (
        <section className="section-card space-y-3">
          <div>
            <h3 className="text-base font-semibold text-fog">{t('magma.pnlTitle')}</h3>
            <p className="text-sm text-fog/60">{t('magma.pnlBody')}</p>
          </div>
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <div className="rounded-lg border border-white/10 bg-white/5 px-4 py-3">
              <p className="text-xs uppercase tracking-wide text-fog/50">{t('magma.pnlRevenue')}</p>
              <p className="text-xl font-semibold text-emerald-200">
                {formatSats(overview.pnl.revenue_sat, locale)}
              </p>
              <p className="text-xs text-fog/50">
                {t('magma.pnlSalesCount', { count: overview.pnl.sales_count })}
              </p>
            </div>
            <div className="rounded-lg border border-white/10 bg-white/5 px-4 py-3">
              <p className="text-xs uppercase tracking-wide text-fog/50">{t('magma.pnlOnchain')}</p>
              <p className="text-xl font-semibold text-amber-200">
                −{formatSats(overview.pnl.onchain_cost_sat, locale)}
              </p>
              {overview.pnl.onchain_cost_resolved < overview.pnl.sales_count && (
                <p className="text-xs text-fog/50">
                  {t('magma.pnlOnchainPartial', {
                    resolved: overview.pnl.onchain_cost_resolved,
                    total: overview.pnl.sales_count
                  })}
                </p>
              )}
            </div>
            <div className="rounded-lg border border-white/10 bg-white/5 px-4 py-3">
              <p className="text-xs uppercase tracking-wide text-fog/50">{t('magma.pnlNet')}</p>
              <p
                className={`text-xl font-semibold ${
                  overview.pnl.net_sat >= 0 ? 'text-emerald-200' : 'text-rose-200'
                }`}
              >
                {formatSats(overview.pnl.net_sat, locale)}
              </p>
            </div>
            <div className="rounded-lg border border-white/10 bg-white/5 px-4 py-3">
              <p className="text-xs uppercase tracking-wide text-fog/50">{t('magma.pnlPending')}</p>
              <p className="text-xl font-semibold text-fog">
                {formatSats(overview.pnl.pending_revenue_sat, locale)}
              </p>
              <p className="text-xs text-fog/50">
                {t('magma.pnlPendingCount', { count: overview.pnl.pending_count })}
              </p>
            </div>
          </div>
          {/* Stated explicitly because the same fee appears in both places and it
              would otherwise read as a discrepancy. */}
          <p className="text-xs text-fog/45">{t('magma.pnlReportsNote')}</p>
        </section>
      )}

      {/* Collapsed by default: an established seller has dozens of offers, almost
          all disabled, and expanding them by default buried the rest of the page.
          Shown in every mode because editing an offer spends nothing — it is
          configuration, not execution. */}
      <section className="section-card space-y-3">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <button
              className="flex items-center gap-2 text-base font-semibold text-fog hover:text-glow"
              onClick={() => setOffersOpen(!offersOpen)}
            >
              <span className={`transition-transform ${offersOpen ? 'rotate-90' : ''}`}>›</span>
              {t('magma.offersTitle')}
              <span className="text-xs font-normal text-fog/50">
                {t('magma.offersCount', { active: offersActiveCount, total: offers?.offers.length ?? 0 })}
              </span>
            </button>
            <p className="mt-1 text-sm text-fog/60">{t('magma.offersBody')}</p>
          </div>
          <button
            className="btn-secondary"
            onClick={() => {
              setOfferDraft(null)
              setOfferOpen(true)
            }}
          >
            {t('magma.offerCreate')}
          </button>
        </div>

        {offersError && <p className="text-sm text-rose-200">{offersError}</p>}
        {offers?.mode_warning && (
          <p className="rounded-lg border border-amber-400/30 bg-amber-500/10 px-3 py-2 text-sm text-amber-100">
            {offers.mode_warning}
          </p>
        )}

        {offersOpen && (
          <>
            {offers && offers.offers.length === 0 && (
              <p className="text-sm text-fog/50">{t('magma.offersNone')}</p>
            )}
            <div className="max-h-[30rem] space-y-2 overflow-y-auto overscroll-contain pr-1 [scrollbar-gutter:stable]">
              {sortedOffers.map((offer) => {
                const conflicts = offers?.conflicts?.[offer.id ?? ''] ?? []
                const enabled = offer.status === 'ENABLED'
                return (
                  <div
                    key={offer.id}
                    className={`rounded-lg border px-4 py-3 text-sm ${
                      enabled ? 'border-emerald-400/25 bg-emerald-500/5' : 'border-white/10 bg-white/5'
                    }`}
                  >
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <span
                        className={`rounded-full px-2 py-0.5 text-[10px] uppercase tracking-wide ${
                          enabled ? 'bg-emerald-500/20 text-emerald-200' : 'bg-white/10 text-fog/50'
                        }`}
                      >
                        {enabled ? t('magma.offerEnabled') : t('magma.offerDisabled')}
                      </span>
                      <div className="flex flex-wrap gap-2">
                        <button
                          className="btn-secondary text-xs px-2 py-1"
                          onClick={() => {
                            setOfferDraft(offer)
                            setOfferOpen(true)
                          }}
                        >
                          {t('magma.offerEdit')}
                        </button>
                        <button
                          className="btn-secondary text-xs px-2 py-1"
                          disabled={busy}
                          onClick={() => handleToggleOffer(offer.id ?? '')}
                        >
                          {enabled ? t('magma.offerDisable') : t('magma.offerEnable')}
                        </button>
                      </div>
                    </div>
                    <div className="mt-2 grid gap-1 text-fog/70 md:grid-cols-2">
                      <span>
                        {formatSats(offer.min_size_sat, locale)} –{' '}
                        {formatSats(offer.max_size_sat || offer.total_size_sat, locale)}
                        <span className="text-fog/45"> · {t('magma.offerTotalSize').toLowerCase()} {formatSats(offer.total_size_sat, locale)}</span>
                      </span>
                      <span>
                        {offer.fee_rate_ppm.toLocaleString(locale)} ppm +{' '}
                        {offer.fixed_fee_mode === 'automatic'
                          ? `${offer.onchain_priority ?? 'HIGH'} ${offer.onchain_multiplier ?? 2}x`
                          : formatSats(offer.base_fee_sat, locale)}
                        <span className="text-fog/45"> · {formatDays(offer.min_block_length)}</span>
                      </span>
                      <span className="text-fog/60">
                        {t('magma.offerFeeRateCap')}: {offer.fee_rate_cap_ppm.toLocaleString(locale)} ppm / base{' '}
                        {offer.base_fee_cap_sat}
                      </span>
                      {(offer.conditions?.length ?? 0) > 0 && (
                        <span className="text-fog/60">
                          {offer.conditions?.map((c) => `${c.condition} ${c.operator} ${c.value}`).join(' · ')}
                        </span>
                      )}
                    </div>
                    {conflicts.map((conflict) => (
                      <p
                        key={conflict.message}
                        className={`mt-2 text-xs ${conflict.blocking ? 'text-rose-200' : 'text-amber-200'}`}
                      >
                        {conflict.blocking ? '⛔ ' : '⚠️ '}
                        {conflict.message}
                      </p>
                    ))}
                  </div>
                )
              })}
            </div>
          </>
        )}
      </section>

      {actionNeeded.length > 0 && (
        <section className="section-card space-y-3">
          <h3 className="text-base font-semibold text-fog">
            {t('magma.actionNeeded', { count: actionNeeded.length })}
          </h3>
          <p className="text-sm text-fog/60">{t('magma.actionNeededHint')}</p>
          <div className="grid gap-3">
            {actionNeeded.map((order) => (
              <div
                key={order.id}
                className="rounded-lg border border-amber-400/30 bg-amber-500/10 px-4 py-3 text-sm"
              >
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <span className="font-semibold text-fog">{order.id}</span>
                  <span className={`rounded-full px-3 py-1 text-xs ${statusStyle(order.status)}`}>
                    {order.status}
                  </span>
                </div>
                <div className="mt-2 grid gap-1 text-fog/70 md:grid-cols-2">
                  <span>
                    {t('magma.colSize')}: {formatSats(order.size_sat, locale)}
                  </span>
                  <span>
                    {t('magma.colRevenue')}: {formatSats(order.revenue_sat, locale)}
                  </span>
                  <span>
                    {t('magma.colBuyer')}: <BuyerLink order={order} />
                  </span>
                  <span>
                    {t('magma.colCommitment')}: {formatDays(order.commitment_blocks)}
                  </span>
                </div>
                {order.last_error && (
                  <p className="mt-2 text-rose-200">{order.last_error}</p>
                )}
                {assisted && <MagmaOrderActions order={order} locale={locale} onDone={load} />}
              </div>
            ))}
          </div>
        </section>
      )}

      <section className="section-card space-y-3">
        <h3 className="text-base font-semibold text-fog">{t('magma.orders')}</h3>
        {orders.length === 0 ? (
          <p className="text-sm text-fog/60">{t('magma.noOrders')}</p>
        ) : (
          <div className="max-h-[32rem] overflow-x-auto overflow-y-auto overscroll-contain pr-1 [scrollbar-gutter:stable]">
            <table className="w-full min-w-[900px] text-left text-sm">
              <thead className="sticky top-0 z-10 bg-slate text-xs uppercase tracking-wide text-fog/50 shadow-[0_1px_0_rgba(255,255,255,.08)]">
                <tr>
                  <th className="py-2 pr-4">{t('magma.colDate')}</th>
                  <th className="py-2 pr-4">{t('magma.colStatus')}</th>
                  <th className="py-2 pr-4">{t('magma.colSize')}</th>
                  <th className="py-2 pr-4">{t('magma.colRevenue')}</th>
                  <th className="py-2 pr-4">{t('magma.colPrice')}</th>
                  <th className="py-2 pr-4">{t('magma.colCommitment')}</th>
                  <th className="py-2 pr-4">{t('magma.colFeeCap')}</th>
                  <th className="py-2 pr-4">{t('magma.colBuyer')}</th>
                </tr>
              </thead>
              <tbody className="text-fog/80">
                {orders.map((order) => {
                  const perDay = pricePerDayPPM(order)
                  const isOpen = expanded === order.id
                  return (
                    <tr
                      key={order.id}
                      className="cursor-pointer border-t border-white/5 hover:bg-white/5"
                      onClick={() => setExpanded(isOpen ? null : order.id)}
                    >
                      <td className="py-2 pr-4 whitespace-nowrap">
                        {formatDateTime(order.created_at, locale)}
                      </td>
                      <td className="py-2 pr-4">
                        <span className={`rounded-full px-2 py-1 text-xs ${statusStyle(order.status)}`}>
                          {order.status}
                        </span>
                      </td>
                      <td className="py-2 pr-4 whitespace-nowrap">{formatSats(order.size_sat, locale)}</td>
                      <td className="py-2 pr-4 whitespace-nowrap">{formatSats(order.revenue_sat, locale)}</td>
                      <td className="py-2 pr-4 whitespace-nowrap">
                        {order.price_ppm.toLocaleString(locale)} ppm
                        {perDay !== null && (
                          <span className="block text-xs text-fog/50">
                            {perDay.toFixed(1)} {t('magma.ppmPerDay')}
                          </span>
                        )}
                      </td>
                      <td className="py-2 pr-4 whitespace-nowrap">
                        {formatDays(order.commitment_blocks)}
                        {(order.closed_blocks_before_min ?? 0) > 0 && (
                          <span className="block text-xs text-amber-200">{t('magma.closedEarly')}</span>
                        )}
                      </td>
                      <td className="py-2 pr-4 whitespace-nowrap">
                        {order.fee_rate_cap_ppm.toLocaleString(locale)} ppm
                        <span className="block text-xs text-fog/50">
                          base {order.base_fee_cap_sat} sat
                        </span>
                      </td>
                      <td className="py-2 pr-4 whitespace-nowrap" onClick={(event) => event.stopPropagation()}>
                        <BuyerLink order={order} />
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
        {expanded && (
          <MagmaOrderDetail
            order={orders.find((order) => order.id === expanded)}
            locale={locale}
            onClose={() => setExpanded(null)}
          />
        )}
      </section>

      {offerOpen && offers && (
        <MagmaOfferDialog
          initial={offerDraft}
          options={{ conditions: offers.condition_options, operators: offers.operator_options }}
          locale={locale}
          onClose={() => setOfferOpen(false)}
          onSaved={(view) => {
            setOffers(view)
            setOfferOpen(false)
          }}
        />
      )}

      {/* Auto mode decides between page loads, and a deferral reason is cleared by
          the next transition. Without this timeline the operator opens the app and
          sees only the current state, with no record of how it got there. */}
      {events.length > 0 && (
        <section className="section-card space-y-3">
          <div>
            <h3 className="text-base font-semibold text-fog">{t('magma.activityTitle')}</h3>
            <p className="text-sm text-fog/60">{t('magma.activityBody')}</p>
          </div>
          <div className="max-h-[24rem] space-y-1 overflow-y-auto pr-1 [scrollbar-gutter:stable]">
            {events.map((event) => (
              <div
                key={event.id}
                className="flex flex-wrap items-baseline gap-x-3 gap-y-1 border-b border-white/5 py-1.5 text-sm last:border-0"
              >
                <span className="whitespace-nowrap font-mono text-xs text-fog/45">
                  {formatDateTime(event.created_at, locale)}
                </span>
                <span
                  className={`whitespace-nowrap rounded px-1.5 py-0.5 text-[10px] uppercase tracking-wide ${
                    event.level === 'error'
                      ? 'bg-rose-500/20 text-rose-200'
                      : event.level === 'warning'
                        ? 'bg-amber-500/20 text-amber-200'
                        : 'bg-white/10 text-fog/60'
                  }`}
                >
                  {event.kind}
                </span>
                <span className="min-w-0 flex-1 text-fog/80">{event.message}</span>
                <button
                  className="whitespace-nowrap font-mono text-xs text-fog/40 hover:text-fog/70"
                  onClick={() => setExpanded(event.order_id)}
                  title={event.order_id}
                >
                  {event.order_id.slice(0, 8)}
                </button>
              </div>
            ))}
          </div>
        </section>
      )}

      {/* Historical repair. Kept out of the way because it is a one-off: sales
          closed before this app existed carry no settle date, so their revenue is
          missing from past reports while their channel-open fee was always
          counted. */}
      {orders.length > 0 && (
        <section className="section-card space-y-3">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <h3 className="text-base font-semibold text-fog">{t('magma.backfillTitle')}</h3>
              <p className="text-sm text-fog/60">{t('magma.backfillBody')}</p>
            </div>
            <button className="btn-secondary" onClick={() => setBackfillOpen(true)}>
              {t('magma.backfillCheck')}
            </button>
          </div>
        </section>
      )}

      {backfillOpen && <MagmaBackfillDialog locale={locale} onClose={() => setBackfillOpen(false)} onApplied={load} />}

      {pendingMode && (
        <MagmaModeConfirm
          mode={pendingMode}
          overview={overview}
          locale={locale}
          onCancel={() => setPendingMode(null)}
          onConfirm={() => commitMode(pendingMode)}
        />
      )}

      {policyOpen && overview?.policy && (
        <MagmaPolicyDialog
          initial={overview.policy}
          onClose={() => setPolicyOpen(false)}
          onSaved={(policy) => {
            setOverview({ ...overview, policy })
            setPolicyOpen(false)
            load()
          }}
        />
      )}
    </div>
  )
}

// Channel durations Amboss offers, in blocks. Fixed list because the API accepts
// arbitrary block counts but the marketplace advertises these.
const magmaDurations = [
  { blocks: 1008, labelKey: 'magma.duration1w' },
  { blocks: 4320, labelKey: 'magma.duration1m' },
  { blocks: 8640, labelKey: 'magma.duration2m' },
  { blocks: 12960, labelKey: 'magma.duration3m' },
  { blocks: 25920, labelKey: 'magma.duration6m' }
]

// Automatic is the default because it is what protects the margin: the fixed fee
// tracks the mempool instead of the sale becoming unprofitable when fees spike.
const emptyOffer = (): MagmaOffer => ({
  total_size_sat: 0,
  min_size_sat: 0,
  max_size_sat: 0,
  fee_rate_ppm: 0,
  base_fee_sat: 0,
  fee_rate_cap_ppm: 0,
  base_fee_cap_sat: 0,
  min_block_length: 4320,
  conditions: [],
  fixed_fee_mode: 'automatic',
  onchain_priority: 'HIGH',
  onchain_multiplier: 2
})

// The multiplier is how many on-chain transactions the fixed fee should cover.
const magmaMultipliers = [
  { value: 1, labelKey: 'magma.offerMultiplier1' },
  { value: 2, labelKey: 'magma.offerMultiplier2' },
  { value: 3, labelKey: 'magma.offerMultiplier3' },
  { value: 4, labelKey: 'magma.offerMultiplier4' },
  { value: 5, labelKey: 'magma.offerMultiplier5' }
]

// MagmaOfferDialog mirrors the Amboss sell form. Its job beyond editing is to
// name the contradictions with the global policy: a policy tighter than the offer
// accepts nothing, and neither screen shows that on its own.
function MagmaOfferDialog({
  initial,
  options,
  locale,
  onClose,
  onSaved
}: {
  initial: MagmaOffer | null
  options: { conditions: string[]; operators: string[] }
  locale: string
  onClose: () => void
  onSaved: (view: MagmaOffersView) => void
}) {
  const { t } = useTranslation()
  const [draft, setDraft] = useState<MagmaOffer>(initial ?? emptyOffer())
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const setNum = (key: keyof MagmaOffer, raw: string) => {
    const value = raw === '' ? 0 : Number(raw)
    if (Number.isNaN(value) || value < 0) return
    setDraft({ ...draft, [key]: value })
  }

  const conditions = draft.conditions ?? []
  const setCondition = (index: number, patch: Partial<MagmaOfferCondition>) => {
    const next = conditions.map((item, position) =>
      position === index ? { ...item, ...patch } : item
    )
    setDraft({ ...draft, conditions: next })
  }

  const save = () => {
    setBusy(true)
    setError('')
    saveMagmaOffer(draft)
      .then((view) => onSaved(view))
      .catch((err) => setError(err instanceof Error ? err.message : String(err)))
      .finally(() => setBusy(false))
  }

  // Every cell has the same shape: label then input, with no per-field hint inside.
  // Hints used to live here, which made a cell with one three rows tall and a cell
  // without two; the grid stretched them to different heights and that is why the
  // inputs never lined up. The explanations moved to the section headers.
  const numberField = (key: keyof MagmaOffer, labelKey: string, suffix?: string) => (
    <label className="grid gap-1.5 text-sm">
      <span className="text-xs uppercase tracking-wide text-fog/50">{t(labelKey)}</span>
      <div className="relative">
        <input
          className="input-field w-full"
          type="number"
          min={0}
          value={draft[key] as number}
          onChange={(event) => setNum(key, event.target.value)}
        />
        {suffix && (
          <span className="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-xs text-fog/40">
            {suffix}
          </span>
        )}
      </div>
    </label>
  )

  const section = (titleKey: string, hintKey: string, children: JSX.Element) => (
    <div className="space-y-3 rounded-lg border border-white/10 bg-white/[0.02] px-4 py-3">
      <div>
        <p className="text-sm font-semibold text-fog">{t(titleKey)}</p>
        <p className="text-xs text-fog/50">{t(hintKey)}</p>
      </div>
      {children}
    </div>
  )

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 px-4 py-8">
      <div className="max-h-full w-full max-w-2xl overflow-y-auto rounded-lg border border-white/10 bg-ink p-5 shadow-xl">
        <div className="space-y-2">
          <h3 className="text-lg font-semibold text-fog">
            {draft.id ? t('magma.offerEditTitle') : t('magma.offerCreateTitle')}
          </h3>
          <p className="text-sm text-fog/60">{t('magma.offerBody')}</p>
        </div>

        <div className="mt-5 space-y-3">
          {section(
            'magma.offerSectionLiquidity',
            'magma.offerSectionLiquidityHint',
            <div className="grid items-start gap-4 sm:grid-cols-3">
              {numberField('total_size_sat', 'magma.offerTotalSize', 'sat')}
              {numberField('min_size_sat', 'magma.offerMinSize', 'sat')}
              {numberField('max_size_sat', 'magma.offerMaxSize', 'sat')}
            </div>
          )}

          {section(
            'magma.offerSectionPrice',
            'magma.offerSectionPriceHint',
            <div className="space-y-3">
              <div className="grid items-start gap-4 sm:grid-cols-2">
                {numberField('fee_rate_ppm', 'magma.offerFeeRate', 'ppm')}
                <label className="grid gap-1.5 text-sm">
                  <span className="text-xs uppercase tracking-wide text-fog/50">
                    {t('magma.offerFixedFeeMode')}
                  </span>
                  <select
                    className="input-field w-full"
                    value={draft.fixed_fee_mode}
                    onChange={(event) =>
                      setDraft({
                        ...draft,
                        fixed_fee_mode: event.target.value as 'manual' | 'automatic',
                        onchain_priority: draft.onchain_priority || 'HIGH',
                        onchain_multiplier: draft.onchain_multiplier || 2
                      })
                    }
                  >
                    <option value="automatic">{t('magma.offerFixedFeeAutomatic')}</option>
                    <option value="manual">{t('magma.offerFixedFeeManual')}</option>
                  </select>
                </label>
              </div>
              {draft.fixed_fee_mode === 'manual' ? (
                <div className="grid items-start gap-4 sm:grid-cols-2">
                  {numberField('base_fee_sat', 'magma.offerBaseFee', 'sat')}
                </div>
              ) : (
                <div className="grid items-start gap-4 sm:grid-cols-2">
                  <label className="grid gap-1.5 text-sm">
                    <span className="text-xs uppercase tracking-wide text-fog/50">
                      {t('magma.offerPriority')}
                    </span>
                    <select
                      className="input-field w-full"
                      value={draft.onchain_priority || 'HIGH'}
                      onChange={(event) => setDraft({ ...draft, onchain_priority: event.target.value })}
                    >
                      <option value="HIGH">{t('magma.offerPriorityHigh')}</option>
                      <option value="MEDIUM">{t('magma.offerPriorityMedium')}</option>
                      <option value="LOW">{t('magma.offerPriorityLow')}</option>
                    </select>
                  </label>
                  <label className="grid gap-1.5 text-sm">
                    <span className="text-xs uppercase tracking-wide text-fog/50">
                      {t('magma.offerMultiplier')}
                    </span>
                    <select
                      className="input-field w-full"
                      value={draft.onchain_multiplier || 2}
                      onChange={(event) =>
                        setDraft({ ...draft, onchain_multiplier: Number(event.target.value) })
                      }
                    >
                      {magmaMultipliers.map((option) => (
                        <option key={option.value} value={option.value}>
                          {t(option.labelKey)}
                        </option>
                      ))}
                    </select>
                  </label>
                </div>
              )}
              <p className="text-xs text-fog/45">
                {draft.fixed_fee_mode === 'manual'
                  ? t('magma.offerFixedFeeManualHint')
                  : t('magma.offerFixedFeeAutomaticHint')}
              </p>
            </div>
          )}

          {section(
            'magma.offerSectionPromises',
            'magma.offerSectionPromisesHint',
            <div className="grid items-start gap-4 sm:grid-cols-3">
              <label className="grid gap-1.5 text-sm">
                <span className="text-xs uppercase tracking-wide text-fog/50">
                  {t('magma.offerDuration')}
                </span>
                <select
                  className="input-field w-full"
                  value={draft.min_block_length}
                  onChange={(event) =>
                    setDraft({ ...draft, min_block_length: Number(event.target.value) })
                  }
                >
                  {magmaDurations.map((option) => (
                    <option key={option.blocks} value={option.blocks}>
                      {t(option.labelKey)}
                    </option>
                  ))}
                </select>
              </label>
              {numberField('fee_rate_cap_ppm', 'magma.offerFeeRateCap', 'ppm')}
              {numberField('base_fee_cap_sat', 'magma.offerBaseFeeCap', 'sat')}
            </div>
          )}

          {section(
            'magma.offerConditions',
            'magma.offerConditionsHint',
            <div className="space-y-2">
              {conditions.length === 0 && (
                <p className="text-sm text-fog/50">{t('magma.offerNoConditions')}</p>
              )}
              {conditions.map((condition, index) => (
                <div key={index} className="flex flex-wrap items-center gap-2">
                  <select
                    className="input-field min-w-[10rem] flex-1"
                    value={condition.condition}
                    onChange={(event) => setCondition(index, { condition: event.target.value })}
                  >
                    {options.conditions.map((value) => (
                      <option key={value} value={value}>
                        {value}
                      </option>
                    ))}
                  </select>
                  <select
                    className="input-field min-w-[9rem]"
                    value={condition.operator}
                    onChange={(event) => setCondition(index, { operator: event.target.value })}
                  >
                    {options.operators.map((value) => (
                      <option key={value} value={value}>
                        {value}
                      </option>
                    ))}
                  </select>
                  <input
                    className="input-field w-28"
                    value={condition.value}
                    placeholder={t('magma.offerConditionValue')}
                    onChange={(event) => setCondition(index, { value: event.target.value })}
                  />
                  <button
                    className="btn-secondary px-2 py-1 text-xs"
                    onClick={() =>
                      setDraft({
                        ...draft,
                        conditions: conditions.filter((_, position) => position !== index)
                      })
                    }
                  >
                    {t('magma.offerRemoveCondition')}
                  </button>
                </div>
              ))}
              <button
                className="btn-secondary px-2 py-1 text-xs"
                onClick={() =>
                  setDraft({
                    ...draft,
                    conditions: [
                      ...conditions,
                      { condition: options.conditions[0] ?? '', operator: 'GREATER_THAN', value: '' }
                    ]
                  })
                }
              >
                {t('magma.offerAddCondition')}
              </button>
            </div>
          )}
        </div>

        {error && <p className="mt-4 text-sm text-rose-200">{error}</p>}

        <div className="mt-5 flex flex-wrap items-center justify-between gap-3">
          <p className="text-xs text-fog/45">
            {t('magma.offerPublishHint', { size: draft.total_size_sat.toLocaleString(locale) })}
          </p>
          <div className="flex flex-wrap gap-3">
            <button className="btn-secondary" onClick={onClose}>
              {t('common.cancel')}
            </button>
            <button className="btn-primary" onClick={save} disabled={busy}>
              {busy ? t('magma.working') : t('common.save')}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}

// MagmaBackfillDialog always previews before it can write. The preview runs on
// open, and the apply button only exists once there is something to apply.
function MagmaBackfillDialog({
  locale,
  onClose,
  onApplied
}: {
  locale: string
  onClose: () => void
  onApplied: () => void
}) {
  const { t } = useTranslation()
  const [report, setReport] = useState<MagmaBackfillReport | null>(null)
  const [busy, setBusy] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    let active = true
    previewMagmaBackfill()
      .then((data) => {
        if (active) setReport(data)
      })
      .catch((err) => {
        if (active) setError(err instanceof Error ? err.message : String(err))
      })
      .finally(() => {
        if (active) setBusy(false)
      })
    // Guards against a result landing after the operator closed the dialog, which
    // would otherwise reopen state on an unmounted component.
    return () => {
      active = false
    }
  }, [])

  const apply = () => {
    setBusy(true)
    setError('')
    applyMagmaBackfill()
      .then((data) => {
        setReport(data)
        onApplied()
      })
      .catch((err) => setError(err instanceof Error ? err.message : String(err)))
      .finally(() => setBusy(false))
  }

  const canApply = Boolean(report && !report.applied && report.matched_orders > 0)

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 px-4 py-8">
      <div className="max-h-full w-full max-w-lg overflow-y-auto rounded-lg border border-white/10 bg-ink p-5 shadow-xl">
        <h3 className="text-lg font-semibold text-fog">{t('magma.backfillTitle')}</h3>

        {busy && !report && <p className="mt-4 text-sm text-fog/60">{t('magma.backfillScanning')}</p>}
        {error && <p className="mt-4 text-sm text-rose-200">{error}</p>}

        {report && (
          <div className="mt-4 space-y-3 text-sm">
            <div className="grid gap-2 text-fog/70">
              <span>
                {t('magma.backfillInvoicesFound')}: <strong className="text-fog">{report.invoices_found}</strong>
              </span>
              <span>
                {t('magma.backfillMatched')}: <strong className="text-fog">{report.matched_orders}</strong>
              </span>
              {report.already_stamped > 0 && (
                <span>
                  {t('magma.backfillAlready')}: {report.already_stamped}
                </span>
              )}
              {report.matched_orders > 0 && (
                <span>
                  {t('magma.backfillRevenue')}:{' '}
                  <strong className="text-emerald-200">{formatSats(report.revenue_sat, locale)}</strong>
                </span>
              )}
              {report.reports_rerun_from && (
                <span>
                  {t('magma.backfillWindow')}: {report.reports_rerun_from} → {report.reports_rerun_to}
                </span>
              )}
            </div>

            {report.notes?.map((note) => (
              <p key={note} className="text-xs text-amber-200">
                {note}
              </p>
            ))}

            {report.invoices_found === 0 && (
              <p className="rounded-lg border border-white/10 bg-white/5 px-3 py-2 text-fog/70">
                {t('magma.backfillNothingFound')}
              </p>
            )}

            {report.applied ? (
              <div className="rounded-lg border border-emerald-400/30 bg-emerald-500/10 px-3 py-2 text-emerald-100">
                <p>{t('magma.backfillApplied', { count: report.stamped })}</p>
                {report.reports_rerun_from && (
                  <>
                    <p className="mt-2 text-xs">{t('magma.backfillRerunHint')}</p>
                    <code className="mt-1 block break-all rounded bg-black/30 px-2 py-1 text-xs">
                      lightningos-manager reports-backfill --config /etc/lightningos/config.yaml --from{' '}
                      {report.reports_rerun_from} --to {report.reports_rerun_to}
                    </code>
                  </>
                )}
              </div>
            ) : (
              canApply && <p className="text-xs text-fog/50">{t('magma.backfillApplyHint')}</p>
            )}
          </div>
        )}

        <div className="mt-5 flex flex-wrap justify-end gap-3">
          {/* Never disabled: the scan walks every invoice on the node and can take
              a while, and a dialog with no way out is worse than a stale one. It
              aborts the request on the way out. */}
          <button className="btn-secondary" onClick={onClose}>
            {report?.applied ? t('common.close') : t('common.cancel')}
          </button>
          {canApply && (
            <button className="btn-primary" onClick={apply} disabled={busy}>
              {busy ? t('magma.working') : t('magma.backfillApply')}
            </button>
          )}
        </div>
      </div>
    </div>
  )
}

// MagmaModeConfirm replaces a browser confirm() so the consequences of leaving
// monitor mode can be laid out properly: what the mode may do, what the policy
// currently accepts, and whether the wallet and token can actually back it.
function MagmaModeConfirm({
  mode,
  overview,
  locale,
  onCancel,
  onConfirm
}: {
  mode: 'assisted' | 'auto'
  overview: MagmaOverview | null
  locale: string
  onCancel: () => void
  onConfirm: () => void
}) {
  const { t } = useTranslation()
  const isAuto = mode === 'auto'
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 px-4">
      <div className="w-full max-w-lg rounded-lg border border-white/10 bg-ink p-5 shadow-xl">
        <div className="space-y-2">
          <h3 className="text-lg font-semibold text-fog">
            {isAuto ? t('magma.modeAuto') : t('magma.modeAssisted')}
          </h3>
          <p className="text-sm text-fog/70">
            {isAuto ? t('magma.enableAutoConfirm') : t('magma.enableAssistedConfirm')}
          </p>
        </div>

        <div className="mt-4 space-y-3 text-sm">
          {isAuto && overview?.policy_summary && (
            <div className="rounded-lg border border-white/10 bg-white/5 px-3 py-2 text-fog/70">
              <span className="text-xs uppercase tracking-wide text-fog/45">
                {t('magma.policyPrefix')}
              </span>
              <p className="mt-1">{overview.policy_summary}</p>
            </div>
          )}
          {overview?.capacity && (
            <div className="rounded-lg border border-white/10 bg-white/5 px-3 py-2 text-fog/70">
              <span className="text-xs uppercase tracking-wide text-fog/45">
                {t('magma.capacityTitle')}
              </span>
              <p className="mt-1">
                {t('magma.capacityBody', {
                  available: overview.capacity.available_sat.toLocaleString(locale),
                  confirmed: overview.capacity.confirmed_sat.toLocaleString(locale)
                })}
              </p>
            </div>
          )}
          {overview?.token_warning && (
            <p className="rounded-lg border border-amber-400/30 bg-amber-500/10 px-3 py-2 text-amber-100">
              {overview.token_warning}
            </p>
          )}
        </div>

        <div className="mt-5 flex flex-wrap justify-end gap-3">
          <button className="btn-secondary" onClick={onCancel}>
            {t('common.cancel')}
          </button>
          <button className="btn-primary" onClick={onConfirm}>
            {isAuto ? t('magma.confirmEnableAuto') : t('magma.confirmEnableAssisted')}
          </button>
        </div>
      </div>
    </div>
  )
}

// magmaPolicyFields drives the editor. Keeping it declarative means a new policy
// knob is one entry here plus its label, instead of another hand-written input.
const magmaPolicyFields: { key: keyof MagmaPolicy; labelKey: string; hintKey?: string }[] = [
  { key: 'min_channel_size_sat', labelKey: 'magma.policyMinSize' },
  { key: 'max_channel_size_sat', labelKey: 'magma.policyMaxSize' },
  { key: 'min_revenue_sat', labelKey: 'magma.policyMinRevenue' },
  { key: 'min_price_ppm', labelKey: 'magma.policyMinPrice' },
  { key: 'min_price_ppm_per_day', labelKey: 'magma.policyMinPricePerDay', hintKey: 'magma.policyMinPricePerDayHint' },
  { key: 'min_fee_rate_cap_ppm', labelKey: 'magma.policyMinFeeCap', hintKey: 'magma.policyMinFeeCapHint' },
  { key: 'max_commitment_days', labelKey: 'magma.policyMaxCommitment' },
  { key: 'max_sat_per_vbyte', labelKey: 'magma.policyMaxFeeRate' },
  { key: 'max_onchain_cost_pct', labelKey: 'magma.policyMaxCostPct', hintKey: 'magma.policyMaxCostPctHint' },
  { key: 'min_onchain_reserve_sat', labelKey: 'magma.policyReserve', hintKey: 'magma.policyReserveHint' },
  { key: 'max_concurrent_opens', labelKey: 'magma.policyMaxConcurrent' },
  { key: 'max_daily_orders', labelKey: 'magma.policyMaxDailyOrders' },
  { key: 'max_daily_size_sat', labelKey: 'magma.policyMaxDailySize' }
]

function MagmaPolicyDialog({
  initial,
  onClose,
  onSaved
}: {
  initial: MagmaPolicy
  onClose: () => void
  onSaved: (policy: MagmaPolicy) => void
}) {
  const { t } = useTranslation()
  const [draft, setDraft] = useState<MagmaPolicy>(initial)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const setField = (key: keyof MagmaPolicy, raw: string) => {
    const value = raw === '' ? 0 : Number(raw)
    if (Number.isNaN(value) || value < 0) return
    setDraft({ ...draft, [key]: value })
  }

  const save = () => {
    setBusy(true)
    setError('')
    updateMagmaPolicy(draft)
      .then((policy) => onSaved(policy))
      .catch((err) => setError(err instanceof Error ? err.message : String(err)))
      .finally(() => setBusy(false))
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 px-4 py-8">
      <div className="max-h-full w-full max-w-2xl overflow-y-auto rounded-lg border border-white/10 bg-ink p-5 shadow-xl">
        <div className="space-y-2">
          <h3 className="text-lg font-semibold text-fog">{t('magma.policyTitle')}</h3>
          <p className="text-sm text-fog/60">{t('magma.policyBody')}</p>
        </div>

        <div className="mt-5 grid gap-4 sm:grid-cols-2">
          {magmaPolicyFields.map((field) => (
            <label key={field.key} className="grid gap-1 text-sm">
              <span className="text-xs uppercase tracking-wide text-fog/50">{t(field.labelKey)}</span>
              <input
                className="input-field"
                type="number"
                min={0}
                value={draft[field.key] as number}
                onChange={(event) => setField(field.key, event.target.value)}
              />
              {field.hintKey && <span className="text-xs text-fog/45">{t(field.hintKey)}</span>}
            </label>
          ))}
        </div>

        <label className="mt-4 flex items-center gap-2 text-sm text-fog/70">
          <input
            type="checkbox"
            checked={draft.auto_reject_declined}
            onChange={(event) => setDraft({ ...draft, auto_reject_declined: event.target.checked })}
          />
          {t('magma.policyAutoReject')}
        </label>
        <p className="mt-1 text-xs text-fog/45">{t('magma.policyAutoRejectHint')}</p>

        <p className="mt-4 text-xs text-fog/45">{t('magma.policyZeroHint')}</p>
        {error && <p className="mt-3 text-sm text-rose-200">{error}</p>}

        <div className="mt-5 flex flex-wrap justify-end gap-3">
          <button className="btn-secondary" onClick={onClose} disabled={busy}>
            {t('common.cancel')}
          </button>
          <button className="btn-primary" onClick={save} disabled={busy}>
            {busy ? t('magma.working') : t('common.save')}
          </button>
        </div>
      </div>
    </div>
  )
}

// Per-order actions, only rendered in assisted mode. Accepting costs nothing but
// an unpaid invoice; opening spends on-chain and is gated behind an explicit
// preview so the operator sees the fee before committing.
function MagmaOrderActions({
  order,
  locale,
  onDone
}: {
  order: MagmaOrder
  locale: string
  onDone: () => void
}) {
  const { t } = useTranslation()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [preview, setPreview] = useState<MagmaOpenPreview | null>(null)
  const [satPerVbyte, setSatPerVbyte] = useState<number | ''>('')
  const [confirmingOpen, setConfirmingOpen] = useState(false)

  const state = order.local_state || 'observed'
  const run = (action: () => Promise<unknown>) => {
    setBusy(true)
    setError('')
    action()
      .then(() => onDone())
      .catch((err) => setError(err instanceof Error ? err.message : String(err)))
      .finally(() => setBusy(false))
  }

  const loadPreview = (rate?: number) => {
    setBusy(true)
    setError('')
    getMagmaOpenPreview(order.id, rate)
      .then((data) => {
        setPreview(data)
        if (satPerVbyte === '') setSatPerVbyte(data.sat_per_vbyte)
        setConfirmingOpen(true)
      })
      .catch((err) => setError(err instanceof Error ? err.message : String(err)))
      .finally(() => setBusy(false))
  }

  if (order.status === 'WAITING_FOR_SELLER_APPROVAL' && state === 'observed') {
    return (
      <div className="mt-3 space-y-2">
        <div className="flex flex-wrap gap-2">
          <button className="btn-primary" disabled={busy} onClick={() => run(() => acceptMagmaOrder(order.id))}>
            {busy ? t('magma.working') : t('magma.acceptOrder')}
          </button>
          <button className="btn-secondary" disabled={busy} onClick={() => run(() => rejectMagmaOrder(order.id))}>
            {t('magma.rejectOrder')}
          </button>
        </div>
        <p className="text-xs text-fog/50">{t('magma.rejectHint')}</p>
        {error && <p className="text-xs text-rose-200">{error}</p>}
      </div>
    )
  }

  if (order.status === 'WAITING_FOR_CHANNEL_OPEN' && state === 'accepted') {
    return (
      <div className="mt-3 space-y-3">
        {!confirmingOpen ? (
          <button className="btn-primary" disabled={busy} onClick={() => loadPreview()}>
            {busy ? t('magma.working') : t('magma.reviewOpen')}
          </button>
        ) : (
          <div className="space-y-2 rounded-lg border border-white/10 bg-white/5 px-3 py-3">
            <div className="flex flex-wrap items-center gap-2 text-xs">
              <label className="text-fog/60">{t('magma.feeRate')}</label>
              <input
                className="input-field w-24"
                type="number"
                min={1}
                value={satPerVbyte}
                onChange={(event) => setSatPerVbyte(event.target.value === '' ? '' : Number(event.target.value))}
              />
              <span className="text-fog/50">sat/vB</span>
              <button
                className="btn-secondary"
                disabled={busy || satPerVbyte === ''}
                onClick={() => loadPreview(Number(satPerVbyte))}
              >
                {t('magma.recalculate')}
              </button>
              {preview?.fastest_sat_per_vb ? (
                <span className="text-fog/50">
                  {t('magma.mempoolHint', {
                    fastest: preview.fastest_sat_per_vb,
                    halfHour: preview.half_hour_sat_per_vb ?? 0,
                    hour: preview.hour_sat_per_vb ?? 0
                  })}
                </span>
              ) : null}
            </div>
            {preview && (
              <div className="grid gap-1 text-xs text-fog/70 md:grid-cols-2">
                <span>
                  {t('magma.previewFee')}: {formatSats(preview.estimated_fee_sat, locale)}
                </span>
                <span>
                  {t('magma.previewNet')}: {formatSats(preview.net_revenue_sat, locale)} (
                  {preview.fee_share_of_revenue_pct}% {t('magma.previewFeeShare')})
                </span>
                <span>
                  {t('magma.previewDebit')}: {formatSats(preview.total_debit_sat, locale)}
                </span>
                <span>
                  {t('magma.previewSpendable')}: {formatSats(preview.spendable_sat, locale)}
                </span>
              </div>
            )}
            {preview?.warnings?.map((warning) => (
              <p key={warning} className="text-xs text-amber-200">
                {warning}
              </p>
            ))}
            {preview?.blockers?.map((blocker) => (
              <p key={blocker} className="text-xs text-rose-200">
                {blocker}
              </p>
            ))}
            <div className="flex flex-wrap gap-2">
              <button
                className="btn-primary"
                disabled={busy || !preview?.can_open || satPerVbyte === ''}
                onClick={() => run(() => openMagmaChannel(order.id, Number(satPerVbyte)))}
              >
                {busy ? t('magma.working') : t('magma.confirmOpen')}
              </button>
              <button className="btn-secondary" disabled={busy} onClick={() => setConfirmingOpen(false)}>
                {t('common.cancel')}
              </button>
            </div>
            <p className="text-xs text-fog/50">{t('magma.openIrreversibleHint')}</p>
          </div>
        )}
        {error && <p className="text-xs text-rose-200">{error}</p>}
      </div>
    )
  }

  return (
    <p className="mt-3 text-xs text-fog/50">
      {t('magma.localState')}: {state}
    </p>
  )
}

function MagmaOrderDetail({
  order,
  locale,
  onClose
}: {
  order?: MagmaOrder
  locale: string
  onClose: () => void
}) {
  const { t } = useTranslation()
  if (!order) return null
  return (
    <div className="rounded-lg border border-white/10 bg-white/5 px-4 py-3 text-sm">
      <div className="flex items-center justify-between gap-4">
        <span className="font-semibold text-fog">{order.id}</span>
        <button className="btn-secondary" onClick={onClose}>
          {t('common.close')}
        </button>
      </div>
      <div className="mt-3 grid gap-2 text-fog/70 md:grid-cols-2">
        <span>
          {t('magma.detailBuyer')}:{' '}
          {order.buyer_alias?.trim() ? `${order.buyer_alias} · ` : ''}
          <a
            className="break-all text-glow hover:underline"
            href={graphExplorerHref(order.buyer_pubkey)}
          >
            {order.buyer_pubkey || '-'}
          </a>
        </span>
        <span>
          {t('magma.detailPaymentStatus')}: {order.payment_status || '-'}
        </span>
        <span>
          {t('magma.detailChannelPoint')}: <span className="break-all">{order.channel_point || '-'}</span>
        </span>
        <span>
          {t('magma.detailChannelScid')}: {order.channel_scid || '-'}
        </span>
        <span>
          {t('magma.detailPriceBreakdown')}: {order.price_variable_sat.toLocaleString(locale)} +{' '}
          {order.price_fixed_sat.toLocaleString(locale)} sat
        </span>
        <span>
          {t('magma.detailBuyerPays')}: {order.buyer_pays_sat.toLocaleString(locale)} sat (
          {t('magma.detailAmbossFee')} {order.amboss_fee_ppm} ppm)
        </span>
        {order.blocks_until_can_be_closed !== undefined && (
          <span>
            {t('magma.detailBlocksLeft')}: {order.blocks_until_can_be_closed.toLocaleString(locale)}
          </span>
        )}
        {(order.fee_above_cap_seconds ?? 0) > 0 && (
          <span className="text-amber-200">
            {t('magma.detailFeeAboveCap')}: {Math.round((order.fee_above_cap_seconds ?? 0) / 60)} min
          </span>
        )}
        {order.cancellation_reason && (
          <span>
            {t('magma.detailCancellation')}: {order.cancellation_reason}
          </span>
        )}
        {order.seller_close_side && (
          <span>
            {t('magma.detailCloseSide')}: {order.seller_close_side}
          </span>
        )}
      </div>
    </div>
  )
}
