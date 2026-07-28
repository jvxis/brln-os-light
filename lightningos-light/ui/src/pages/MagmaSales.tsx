import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  acceptMagmaOrder,
  getMagmaOpenPreview,
  getMagmaOverview,
  openMagmaChannel,
  refreshMagma,
  rejectMagmaOrder,
  updateMagmaSettings
} from '../api'
import type { MagmaOpenPreview, MagmaOrder, MagmaOverview } from '../api'
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

  const handleChangeMode = (next: 'monitor' | 'assisted' | 'auto') => {
    if (!overview || next === overview.settings.mode) return
    // Leaving monitor mode is the point where this app becomes able to spend, so
    // the things that decide whether a sale can actually be honoured — token
    // validity, free on-chain balance, and in auto mode the policy itself — are
    // stated up front rather than discovered later.
    if (next !== 'monitor') {
      const notes = [next === 'auto' ? t('magma.enableAutoConfirm') : t('magma.enableAssistedConfirm')]
      if (next === 'auto' && overview.policy_summary) {
        notes.push(t('magma.policyPrefix') + ' ' + overview.policy_summary)
      }
      if (overview.token_warning) notes.push(overview.token_warning)
      if (overview.capacity) {
        notes.push(
          t('magma.capacityBody', {
            available: overview.capacity.available_sat.toLocaleString(locale),
            confirmed: overview.capacity.confirmed_sat.toLocaleString(locale)
          })
        )
      }
      if (!window.confirm(notes.join('\n\n'))) return
    }
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
            <span
              className={`rounded-full px-3 py-1 text-xs uppercase tracking-wide ${
                auto
                  ? 'border border-rose-400/30 bg-rose-500/15 text-rose-200'
                  : assisted
                    ? 'border border-amber-400/30 bg-amber-500/15 text-amber-200'
                    : 'border border-white/10 bg-white/5 text-fog/70'
              }`}
            >
              {auto ? t('magma.modeAuto') : assisted ? t('magma.modeAssisted') : t('magma.modeMonitor')}
            </span>
            <select
              className="input-field"
              value={mode}
              disabled={busy || !settings}
              onChange={(event) => handleChangeMode(event.target.value as 'monitor' | 'assisted' | 'auto')}
            >
              <option value="monitor">{t('magma.modeMonitor')}</option>
              <option value="assisted">{t('magma.modeAssisted')}</option>
              <option value="auto">{t('magma.modeAuto')}</option>
            </select>
            <button className="btn-secondary" onClick={handleRefresh} disabled={busy}>
              {busy ? t('magma.refreshing') : t('magma.refresh')}
            </button>
          </div>
        </div>

        <p className="text-sm text-fog/70">
          {auto ? t('magma.autoNotice') : assisted ? t('magma.assistedNotice') : t('magma.monitorNotice')}
        </p>
        {auto && overview?.policy_summary && (
          <p className="text-xs text-fog/50">
            {t('magma.policyPrefix')} {overview.policy_summary}
          </p>
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
