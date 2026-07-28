import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getMagmaOverview, refreshMagma, updateMagmaSettings } from '../api'
import type { MagmaOrder, MagmaOverview } from '../api'
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

  const orders = overview?.orders ?? []
  const actionNeeded = overview?.action_needed ?? []

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
            <span className="rounded-full border border-white/10 bg-white/5 px-3 py-1 text-xs uppercase tracking-wide text-fog/70">
              {t('magma.modeMonitor')}
            </span>
            <button className="btn-secondary" onClick={handleRefresh} disabled={busy}>
              {busy ? t('magma.refreshing') : t('magma.refresh')}
            </button>
          </div>
        </div>

        <p className="text-sm text-fog/70">{t('magma.monitorNotice')}</p>

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
          <div className="overflow-x-auto">
            <table className="w-full min-w-[900px] text-left text-sm">
              <thead className="text-xs uppercase tracking-wide text-fog/50">
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
