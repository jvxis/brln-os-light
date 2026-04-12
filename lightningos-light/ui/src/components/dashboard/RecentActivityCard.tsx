import { useTranslation } from 'react-i18next'
import { getLocale } from '../../i18n'
import { formatSats, formatTimeAgo, toneFromStatusText } from './formatters'
import StatusBadge from './StatusBadge'
import type { NotificationItem } from './types'

type RecentActivityCardProps = {
  notifications: NotificationItem[]
}

const arrowForDirection = (value?: string) => {
  if (value === 'in') return { label: '<-', tone: 'ok' as const }
  if (value === 'out') return { label: '->', tone: 'warn' as const }
  return { label: '.', tone: 'muted' as const }
}

const feeMsatTotal = (feeSat?: number, feeMsat?: number) => {
  if (typeof feeMsat === 'number' && feeMsat > 0) return feeMsat
  return Math.max(0, feeSat ?? 0) * 1000
}

export default function RecentActivityCard({ notifications }: RecentActivityCardProps) {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)

  return (
    <article className="section-card">
      <div className="flex items-start justify-between gap-3">
        <div>
          <p className="text-xs uppercase tracking-[0.24em] text-fog/45">{t('dashboard.activityKicker')}</p>
          <h3 className="mt-2 text-xl font-semibold">{t('notifications.recentActivity')}</h3>
          <p className="mt-2 text-sm text-fog/60">{t('dashboard.activitySubtitle')}</p>
        </div>
      </div>

      <div className="mt-6 space-y-3">
        {notifications.length > 0 ? notifications.map((item) => {
          const direction = arrowForDirection(item.direction)
          const tone = toneFromStatusText(item.status)
          const subject = item.peer_alias || item.channel_alias || item.memo || item.txid || item.payment_hash || t('common.na')
          const amount = `${formatSats(locale, item.amount_sat)} sats`
          const feeLabel = feeMsatTotal(item.fee_sat, item.fee_msat) > 0
            ? `${t('dashboard.feeLabel')}: ${formatSats(locale, feeMsatTotal(item.fee_sat, item.fee_msat) / 1000)} sats`
            : ''

          return (
            <div key={item.id} className="rounded-3xl border border-white/10 bg-white/5 p-4">
              <div className="flex items-start justify-between gap-3">
                <div className="flex items-start gap-3">
                  <span className={`mt-1 inline-flex h-8 w-8 items-center justify-center rounded-full border text-sm ${
                    direction.tone === 'ok'
                      ? 'border-emerald-400/30 bg-emerald-500/10 text-emerald-200'
                      : direction.tone === 'warn'
                        ? 'border-amber-400/30 bg-amber-500/10 text-amber-200'
                        : 'border-white/10 bg-white/8 text-fog/55'
                  }`}>
                    {direction.label}
                  </span>
                  <div>
                    <div className="flex flex-wrap items-center gap-2">
                      <p className="text-sm font-medium text-fog/90">
                        {t(`notifications.type.${item.type}`)} | {t(`notifications.action.${item.action}`)}
                      </p>
                      <StatusBadge label={item.status} tone={tone} />
                    </div>
                    <p className="mt-1 text-sm text-fog/65">{subject}</p>
                    <p className="mt-2 text-xs text-fog/50">{formatTimeAgo(locale, item.occurred_at)}</p>
                  </div>
                </div>
                <div className="text-right">
                  <p className="text-sm font-semibold text-fog">{amount}</p>
                  {feeLabel ? <p className="mt-1 text-xs text-fog/50">{feeLabel}</p> : null}
                </div>
              </div>
            </div>
          )
        }) : (
          <div className="rounded-3xl border border-white/10 bg-white/5 px-4 py-6 text-sm text-fog/60">
            {t('wallet.noRecentActivity')}
          </div>
        )}
      </div>
    </article>
  )
}
