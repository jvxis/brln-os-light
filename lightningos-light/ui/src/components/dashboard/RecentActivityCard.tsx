import { useMemo } from 'react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { getLocale } from '../../i18n'
import type { NotificationItem } from './types'

type RecentActivityCardProps = {
  notifications: NotificationItem[]
}

const trimMemo = (value: string, max = 48) => {
  if (value.length <= max) return value
  return `${value.slice(0, max)}...`
}

const arrowForDirection = (value: string) => {
  if (value === 'in') return { label: '<-', tone: 'text-glow' }
  if (value === 'out') return { label: '->', tone: 'text-ember' }
  return { label: '.', tone: 'text-fog/50' }
}

const feeMsatTotal = (feeSat: number, feeMsat?: number) => {
  if (feeMsat && feeMsat > 0) {
    return feeMsat
  }
  return Math.max(0, feeSat) * 1000
}

const formatFeeDisplay = (locale: string, feeSat: number, feeMsat?: number) => {
  const msat = feeMsatTotal(feeSat, feeMsat)
  if (msat <= 0) return ''
  const sats = msat / 1000
  const formatter = new Intl.NumberFormat(locale, {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  })
  return `${formatter.format(sats)} sats`
}

const formatFeeRate = (amount: number, feeSat: number, feeMsat?: number) => {
  if (!amount || amount <= 0) return ''
  const msat = feeMsatTotal(feeSat, feeMsat)
  if (msat <= 0) return ''
  const feeSatTotal = msat / 1000
  const ratio = feeSatTotal / amount
  const percentRaw = ratio * 100
  const percent = percentRaw.toFixed(3).replace(/\.?0+$/, '')
  const ppm = Math.round(ratio * 1_000_000)
  return `${percent}% ${ppm}ppm`
}

const mempoolLinkFromChannelPoint = (channelPoint?: string) => {
  if (!channelPoint) return ''
  const parts = channelPoint.split(':')
  if (parts.length !== 2) return ''
  return `https://mempool.space/pt/tx/${parts[0]}#vout=${parts[1]}`
}

const mempoolTxLink = (txid?: string) => {
  if (!txid) return ''
  return `https://mempool.space/tx/${txid}`
}

export default function RecentActivityCard({ notifications }: RecentActivityCardProps) {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)

  const formatTimestamp = (value: string) => {
    if (!value) return t('common.unknownTime')
    const parsed = new Date(value)
    if (Number.isNaN(parsed.getTime())) return t('common.unknownTime')
    return parsed.toLocaleString(locale, {
      year: 'numeric',
      month: 'short',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
    })
  }

  const labelForType = (value: string) => {
    switch (value) {
      case 'onchain':
        return t('notifications.type.onchain')
      case 'lightning':
        return t('notifications.type.lightning')
      case 'channel':
        return t('notifications.type.channel')
      case 'forward':
        return t('notifications.type.forward')
      case 'rebalance':
        return t('notifications.type.rebalance')
      case 'keysend':
        return t('notifications.type.keysend')
      default:
        if (!value) return ''
        return value.charAt(0).toUpperCase() + value.slice(1)
    }
  }

  const labelForAction = (value: string) => {
    switch (value) {
      case 'receive':
        return t('notifications.action.received')
      case 'send':
        return t('notifications.action.sent')
      case 'open':
        return t('notifications.action.opened')
      case 'close':
        return t('notifications.action.closed')
      case 'opening':
        return t('notifications.action.opening')
      case 'closing':
        return t('notifications.action.closing')
      case 'forwarded':
        return t('notifications.action.forwarded')
      case 'rebalanced':
        return t('notifications.action.rebalanced')
      default:
        return value
    }
  }

  const normalizeStatus = (value: string) => {
    if (!value) return t('common.unknown').toUpperCase()
    return value.replace(/_/g, ' ').toUpperCase()
  }

  const rebalanceHashes = useMemo(() => {
    return new Set(
      notifications
        .filter((item) => item.type === 'rebalance' && item.payment_hash)
        .map((item) => item.payment_hash as string),
    )
  }, [notifications])

  const filtered = useMemo(() => {
    return notifications.filter((item) => {
      if (item.type === 'rebalance') return true
      if (!item.payment_hash) return true
      return !rebalanceHashes.has(item.payment_hash)
    })
  }, [notifications, rebalanceHashes])

  return (
    <article className="section-card flex h-full min-h-0 flex-col overflow-hidden">
      <div className="flex items-start justify-between gap-3">
        <div>
          <p className="text-xs uppercase tracking-[0.24em] text-fog/45">{t('dashboard.activityKicker')}</p>
          <h3 className="mt-2 text-xl font-semibold">{t('notifications.recentActivity')}</h3>
          <p className="mt-2 text-sm text-fog/60">{t('dashboard.activitySubtitle')}</p>
        </div>
      </div>

      {filtered.length > 0 ? (
        <div className="mt-6 min-h-0 flex-1 overflow-y-auto pr-2 [scrollbar-gutter:stable]">
          <div className="space-y-2 text-sm">
            {filtered.map((item) => {
              const arrow = arrowForDirection(item.direction)
              const title = `${labelForType(item.type)} ${labelForAction(item.action)}`
              const statusLabel = normalizeStatus(item.status)
              const peer = item.peer_alias || (item.peer_pubkey ? item.peer_pubkey.slice(0, 16) : '')
              const peerLabel = peer
                ? item.type === 'rebalance'
                  ? t('notifications.routeLabel', { peer })
                  : item.type === 'keysend'
                    ? item.direction === 'in'
                      ? t('notifications.peerFrom', { peer })
                      : item.direction === 'out'
                        ? t('notifications.peerTo', { peer })
                        : t('notifications.peerLabel', { peer })
                    : t('notifications.peerLabel', { peer })
                : ''
              const feeRate = formatFeeRate(item.amount_sat, item.fee_sat, item.fee_msat)
              let feeDetail = ''
              if (feeRate) {
                if (item.type === 'forward') {
                  feeDetail = t('notifications.feeEarned', {
                    fee: formatFeeDisplay(locale, item.fee_sat, item.fee_msat),
                    rate: feeRate,
                  })
                } else if (item.type === 'rebalance') {
                  feeDetail = t('notifications.feeDetail', {
                    fee: formatFeeDisplay(locale, item.fee_sat, item.fee_msat),
                    rate: feeRate,
                  })
                }
              }
              const memo = typeof item.memo === 'string' ? item.memo.trim() : ''
              const memoLabel = memo && (item.type === 'lightning' || item.type === 'keysend')
                ? t('notifications.memoLabel', { memo: trimMemo(memo) })
                : ''
              const detailParts: ReactNode[] = [
                peerLabel,
                memoLabel,
              ].filter(Boolean)
              if ((item.type === 'lightning' || item.type === 'keysend') && item.channel_alias) {
                detailParts.push(t('notifications.viaChannel', { channel: item.channel_alias }))
              }
              if (item.channel_point) {
                if (item.type === 'channel') {
                  const link = mempoolLinkFromChannelPoint(item.channel_point)
                  detailParts.push(
                    <a
                      key={`${item.id}-channel`}
                      className="text-emerald-200 hover:text-emerald-100"
                      href={link}
                      target="_blank"
                      rel="noopener noreferrer"
                    >
                      {t('notifications.channelLabel', { value: item.channel_point.slice(0, 16) })}
                    </a>,
                  )
                } else {
                  detailParts.push(t('notifications.channelLabel', { value: item.channel_point.slice(0, 16) }))
                }
              }
              if (item.txid) {
                if (item.type === 'channel' || item.type === 'onchain') {
                  const link = mempoolTxLink(item.txid)
                  detailParts.push(
                    <a
                      key={`${item.id}-tx`}
                      className="text-emerald-200 hover:text-emerald-100"
                      href={link}
                      target="_blank"
                      rel="noopener noreferrer"
                    >
                      {t('notifications.txLabel', { value: item.txid.slice(0, 16) })}
                    </a>,
                  )
                } else {
                  detailParts.push(t('notifications.txLabel', { value: item.txid.slice(0, 16) }))
                }
              }
              if (feeDetail) {
                detailParts.push(feeDetail)
              }
              if (item.type === 'rebalance' && item.memo) {
                detailParts.push(item.memo)
              }
              const feeDisplay = formatFeeDisplay(locale, item.fee_sat, item.fee_msat)

              return (
                <div key={item.id} className="grid gap-2 border-b border-white/10 pb-3 last:border-b-0 last:pb-0 xl:grid-cols-[148px_1fr_auto_auto] xl:items-center">
                  <span className="text-xs text-fog/50">{formatTimestamp(item.occurred_at)}</span>
                  <div className="min-w-0">
                    <div className="text-sm font-medium text-fog">{title}</div>
                    <div className="text-xs text-fog/50">
                      {statusLabel}
                      {detailParts.length > 0 && (
                        <>
                          {' - '}
                          {detailParts.map((part, idx) => (
                            <span key={`${item.id}-detail-${idx}`}>
                              {idx > 0 ? ' - ' : ''}
                              {part}
                            </span>
                          ))}
                        </>
                      )}
                    </div>
                  </div>
                  <span className={`hidden text-xs font-mono xl:block ${arrow.tone}`}>{arrow.label}</span>
                  <div className="text-left xl:text-right">
                    <div className="text-sm font-semibold text-fog">{item.amount_sat} sats</div>
                    {feeDisplay ? (
                      <div
                        className={`text-xs ${
                          item.type === 'forward'
                            ? 'text-emerald-200'
                            : item.type === 'rebalance'
                              ? 'text-rose-200'
                              : 'text-fog/50'
                        }`}
                      >
                        {t('notifications.feeLabel', { fee: feeDisplay })}
                      </div>
                    ) : null}
                  </div>
                </div>
              )
            })}
          </div>
        </div>
      ) : (
        <div className="mt-6 rounded-3xl border border-white/10 bg-white/5 px-4 py-6 text-sm text-fog/60">
          {t('notifications.noNotifications')}
        </div>
      )}
    </article>
  )
}
