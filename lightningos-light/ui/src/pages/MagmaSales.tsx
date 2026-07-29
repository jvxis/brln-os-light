import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  acceptMagmaOrder,
  applyMagmaBackfill,
  getMagmaOpenPreview,
  getMagmaOverview,
  openMagmaChannel,
  previewMagmaBackfill,
  refreshMagma,
  rejectMagmaOrder,
  updateMagmaPolicy,
  updateMagmaSettings
} from '../api'
import type {
  MagmaBackfillReport,
  MagmaOpenPreview,
  MagmaOrder,
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

const formatDateTime = (value: string | undefined, locale: string) => {
  if (!value) return '-'
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return '-'
  return parsed.toLocaleString(locale)
}

// The three positions of the mode switch, ordered by how much the app is allowed
// to do. The active tint escalates with that: neutral, amber, red.
const magmaModes = [
  { value: 'monitor' as const, labelKey: 'magma.modeMonitorShort', activeClass: 'bg-white/15 text-fog' },
  { value: 'assisted' as const, labelKey: 'magma.modeAssistedShort', activeClass: 'bg-amber-500/25 text-amber-100' },
  { value: 'auto' as const, labelKey: 'magma.modeAutoShort', activeClass: 'bg-rose-500/25 text-rose-100' }
]

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

  const load = () => {
    getMagmaOverview()
      .then((data) => {
        setOverview(data)
        setMessage('')
      })
      .catch((err) => {
        setMessage(err instanceof Error ? err.message : t('magma.loadFailed'))
      })
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
  const handleChangeMode = (next: 'monitor' | 'assisted' | 'auto') => {
    if (!overview || next === overview.settings.mode) return
    if (next === 'monitor') {
      commitMode(next)
      return
    }
    setPendingMode(next)
  }

  const commitMode = (next: 'monitor' | 'assisted' | 'auto') => {
    if (!overview) return
    setPendingMode(null)
    setBusy(true)
    updateMagmaSettings({ mode: next })
      .then((settings) => setOverview({ ...overview, settings }))
      .catch((err) => setMessage(err instanceof Error ? err.message : t('magma.settingsFailed')))
      .finally(() => setBusy(false))
  }

  const orders = overview?.orders ?? []
  const actionNeeded = overview?.action_needed ?? []
  const mode = overview?.settings.mode ?? 'monitor'
  const auto = mode === 'auto'
  // Assisted is the only mode with per-order buttons: in auto the engine owns the
  // decisions, and a manual click racing it would fight the policy.
  const assisted = mode === 'assisted'
  const canAct = assisted || auto

  const stats = useMemo(() => {
    const sold = orders.filter((order) => order.channel_point)
    const revenue = sold.reduce((total, order) => total + order.revenue_sat, 0)
    const capViolations = orders.filter((order) => (order.fee_above_cap_seconds ?? 0) > 0).length
    // Early closes are dominated by the buyer, not by our own automations.
    const closedEarly = orders.filter((order) => (order.closed_blocks_before_min ?? 0) > 0).length
    return { total: orders.length, sold: sold.length, revenue, capViolations, closedEarly }
  }, [orders])

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
                    : 'border-white/10 bg-white/5'
              }`}
            >
              {magmaModes.map((option) => {
                const active = mode === option.value
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
          {auto ? t('magma.autoNotice') : assisted ? t('magma.assistedNotice') : t('magma.monitorNotice')}
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
                    {t('magma.colBuyer')}: {shortPubkey(order.buyer_pubkey)}
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
                      <td className="py-2 pr-4 whitespace-nowrap">{shortPubkey(order.buyer_pubkey)}</td>
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
    previewMagmaBackfill()
      .then((data) => setReport(data))
      .catch((err) => setError(err instanceof Error ? err.message : String(err)))
      .finally(() => setBusy(false))
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
          <button className="btn-secondary" onClick={onClose} disabled={busy}>
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
          {t('magma.detailBuyer')}: <span className="break-all">{order.buyer_pubkey || '-'}</span>
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
