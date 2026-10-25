import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getDisk } from '../api'
import { getLocale } from '../i18n'

export default function Disks() {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const gbFormatter = new Intl.NumberFormat(locale, { maximumFractionDigits: 1 })
  const percentFormatter = new Intl.NumberFormat(locale, { maximumFractionDigits: 1 })
  const tempFormatter = new Intl.NumberFormat(locale, { maximumFractionDigits: 1 })
  const [disks, setDisks] = useState<any[]>([])

  const wearWarnThreshold = 75
  const tempWarnThreshold = 70

  useEffect(() => {
    getDisk()
      .then((data) => setDisks(Array.isArray(data) ? data : []))
      .catch(() => null)
  }, [])

  const formatGB = (value?: number) => {
    if (typeof value !== 'number' || Number.isNaN(value)) return '-'
    return `${gbFormatter.format(value)} GB`
  }

  const formatPercent = (value?: number) => {
    if (typeof value !== 'number' || Number.isNaN(value)) return '-'
    return percentFormatter.format(value)
  }

  const formatPercentValue = (value?: number) => {
    if (typeof value !== 'number' || Number.isNaN(value)) return '-'
    return `${percentFormatter.format(value)}%`
  }

  const formatTemp = (value?: number) => {
    if (typeof value !== 'number' || Number.isNaN(value)) return '-'
    return `${tempFormatter.format(value)} C`
  }

  const badgeClass = (tone: 'ok' | 'warn' | 'muted') => {
    if (tone === 'ok') {
      return 'bg-emerald-500/15 text-emerald-200 border border-emerald-400/30'
    }
    if (tone === 'warn') {
      return 'bg-amber-500/15 text-amber-200 border border-amber-400/30'
    }
    return 'bg-white/10 text-fog/60 border border-white/10'
  }

  const Badge = ({ label, tone }: { label: string; tone: 'ok' | 'warn' | 'muted' }) => (
    <span className={`text-[11px] uppercase tracking-wide px-2 py-0.5 rounded-full ${badgeClass(tone)}`}>
      {label}
    </span>
  )

  return (
    <section className="space-y-6">
      <div className="section-card">
        <h2 className="text-2xl font-semibold">{t('disks.title')}</h2>
        <p className="text-fog/60">{t('disks.subtitle')}</p>
      </div>

      <div className="section-card space-y-4">
        {disks.length ? (
          disks.map((disk) => {
            const totalLabel = formatGB(disk.total_gb)
            const usedLabel = formatGB(disk.used_gb)
            const percentLabel = formatPercent(disk.used_percent)
            const smartAvailable = String(disk.smart_status || '').toUpperCase() !== 'UNAVAILABLE'
            const wearLabel = smartAvailable ? formatPercentValue(disk.wear_percent_used) : '—'
            const tempLabel = formatTemp(disk.temperature_c)
            const wearWarn = smartAvailable && typeof disk.wear_percent_used === 'number' && disk.wear_percent_used >= wearWarnThreshold
            const tempWarn = typeof disk.temperature_c === 'number' && disk.temperature_c >= tempWarnThreshold
            const partitions = Array.isArray(disk.partitions) ? disk.partitions : []
            return (
            <div key={disk.device} className="border border-white/10 rounded-2xl p-4">
              <div className="flex items-center justify-between">
                <div>
                  <p className="text-sm text-fog/60">{disk.device}</p>
                  <p className="text-lg font-semibold">{disk.type}</p>
                </div>
                <div className="text-sm text-fog/70">{t('disks.smartLabel', { status: disk.smart_status })}</div>
              </div>
              <div className="mt-3 grid gap-3 lg:grid-cols-6 text-sm">
                <div>
                  <p className="text-fog/60">{t('disks.wear')}</p>
                  <div className="flex items-center gap-2">
                    <p>{wearLabel}</p>
                    {wearWarn && <Badge label={t('disks.wearWarn')} tone="warn" />}
                  </div>
                </div>
                <div>
                  <p className="text-fog/60">{t('disks.temp')}</p>
                  <div className="flex items-center gap-2">
                    <p>{tempLabel}</p>
                    {tempWarn && <Badge label={t('disks.tempWarn')} tone="warn" />}
                  </div>
                </div>
                <div>
                  <p className="text-fog/60">{t('disks.powerOnHours')}</p>
                  <p>{smartAvailable ? disk.power_on_hours : '—'}</p>
                </div>
                <div>
                  <p className="text-fog/60">{t('disks.daysLeft')}</p>
                  <p>{smartAvailable ? disk.days_left_estimate : '—'}</p>
                </div>
                <div>
                  <p className="text-fog/60">{t('disks.size')}</p>
                  <p>{totalLabel}</p>
                </div>
                <div>
                  <p className="text-fog/60">{t('disks.used')}</p>
                  <p>{t('disks.usedValue', { used: usedLabel, percent: percentLabel })}</p>
                </div>
              </div>
              {disk.alerts?.length ? (
                <p className="mt-2 text-xs text-ember">{t('disks.alerts', { alerts: disk.alerts.join(', ') })}</p>
              ) : (
                <p className="mt-2 text-xs text-fog/50">{t('disks.noAlerts')}</p>
              )}
              {partitions.length > 0 && (
                <div className="mt-3 border-t border-white/10 pt-3">
                  <p className="text-xs uppercase tracking-wide text-fog/50">{t('disks.partitions')}</p>
                  <div className="mt-2 space-y-2">
                    {partitions.map((part: any) => {
                      const partTotal = formatGB(part.total_gb)
                      const partUsed = formatGB(part.used_gb)
                      const partPercent = formatPercent(part.used_percent)
                      return (
                        <div key={part.device} className="flex flex-wrap items-center justify-between gap-2 text-xs text-fog/60">
                          <div className="flex flex-wrap items-center gap-2">
                            <span className="font-mono text-fog/70">{part.device}</span>
                            {part.mount && <span className="text-fog/50">{part.mount}</span>}
                          </div>
                          <div className="flex flex-wrap items-center gap-3">
                            <span>{t('disks.size')}: {partTotal}</span>
                            <span>{t('disks.used')}: {t('disks.usedValue', { used: partUsed, percent: partPercent })}</span>
                          </div>
                        </div>
                      )
                    })}
                  </div>
                </div>
              )}
            </div>
          )})
        ) : (
          <p className="text-fog/60">{t('disks.noData')}</p>
        )}
      </div>
    </section>
  )
}
