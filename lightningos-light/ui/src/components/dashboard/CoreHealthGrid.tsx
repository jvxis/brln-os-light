import { useTranslation } from 'react-i18next'
import { getLocale } from '../../i18n'
import {
  compactValue,
  formatGB,
  formatMB,
  formatPercent,
  formatSats,
  formatTemp,
  formatTimeAgo,
  formatTimestamp,
  toneFromStatusText,
} from './formatters'
import HorizontalBarGauge from './HorizontalBarGauge'
import StackedRatioBar from './StackedRatioBar'
import StatusBadge from './StatusBadge'
import type { BitcoinStatus, DiskSmart, LndChannel, LndPeer, LndStatus, PostgresStatus, SystemStats } from './types'

type CoreHealthGridProps = {
  lnd: LndStatus | null
  lndPeers: LndPeer[]
  lndChannels: LndChannel[]
  bitcoin: BitcoinStatus | null
  postgres: PostgresStatus | null
  system: SystemStats | null
  disks: DiskSmart[]
}

const toneFromSmartStatus = (value?: string) => {
  const normalized = String(value || '').trim().toUpperCase()
  if (normalized === 'PASSED' || normalized === 'OK') return 'ok' as const
  if (normalized === 'UNKNOWN' || normalized === 'UNAVAILABLE') return 'warn' as const
  return 'danger' as const
}

export default function CoreHealthGrid({
  lnd,
  lndPeers,
  lndChannels,
  bitcoin,
  postgres,
  system,
  disks,
}: CoreHealthGridProps) {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)

  const lndUris = (() => {
    const raw: string[] = []
    if (Array.isArray(lnd?.uris)) {
      for (const item of lnd.uris) {
        if (typeof item === 'string') raw.push(item)
      }
    }
    if (typeof lnd?.uri === 'string') raw.push(lnd.uri)
    return Array.from(new Set(raw.map((item) => item.trim()).filter(Boolean)))
  })()

  const isLocalBitcoin = Boolean(bitcoin && (bitcoin.mode === 'local' || bitcoin.source === 'app' || bitcoin.source === 'external' || bitcoin.installed !== undefined))
  const cadenceBuckets = Array.isArray(bitcoin?.block_cadence) ? bitcoin.block_cadence : []
  const maxCadence = Math.max(1, ...cadenceBuckets.map((bucket) => bucket.count))
  const cadenceTotal = cadenceBuckets.reduce((sum, bucket) => sum + bucket.count, 0)
  const cadenceHours = ((bitcoin?.block_cadence_window_sec ?? 600) * Math.max(1, cadenceBuckets.length)) / 3600
  const cadenceAvg = cadenceHours > 0 ? cadenceTotal / cadenceHours : 0
  const cpuPercent = system?.cpu_percent_avg_30s ?? system?.cpu_percent ?? 0
  const peerAddressHost = (value?: string) => {
    const raw = String(value || '').trim().toLowerCase()
    if (!raw) return ''
    return raw.includes('@') ? raw.split('@').pop() || '' : raw
  }
  const isTorPeer = (value?: string) => peerAddressHost(value).includes('.onion')
  const connectedPeerCount = lndPeers.length
  const torPeerCount = lndPeers.filter((peer) => isTorPeer(peer.address)).length
  const clearnetPeerCount = lndPeers.filter((peer) => {
    const host = peerAddressHost(peer.address)
    return host !== '' && !host.includes('.onion')
  }).length
  const outboundLiquiditySat = lndChannels.reduce((sum, channel) => sum + Math.max(0, Number(channel.local_balance_sat || 0)), 0)
  const inboundLiquiditySat = lndChannels.reduce((sum, channel) => sum + Math.max(0, Number(channel.remote_balance_sat || 0)), 0)
  const totalLiquiditySat = outboundLiquiditySat + inboundLiquiditySat
  const outboundSharePct = totalLiquiditySat > 0 ? (outboundLiquiditySat / totalLiquiditySat) * 100 : 0

  const copyToClipboard = async (value: string) => {
    if (!value) return
    try {
      await navigator.clipboard.writeText(value)
    } catch {
      // ignore copy failures
    }
  }

  return (
    <div className="space-y-6">
      <div className="grid gap-6 xl:grid-cols-2">
        <article className="section-card">
          <div className="flex items-start justify-between gap-3">
            <div>
              <h3 className="text-lg font-semibold">{t('dashboard.lnd')}</h3>
              <p className="mt-1 text-sm text-fog/60">{lnd?.version ? `v${lnd.version}` : t('dashboard.loadingLndStatus')}</p>
            </div>
            <StatusBadge
              label={lnd?.wallet_state || t('common.na')}
              tone={lnd?.wallet_state === 'unlocked' ? 'ok' : 'warn'}
              size="md"
            />
          </div>
          {lnd ? (
            <div className="mt-5 space-y-4">
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="rounded-2xl border border-white/10 bg-white/5 p-3">
                  <p className="text-xs uppercase tracking-wide text-fog/45">{t('dashboard.synced')}</p>
                  <div className="mt-3 flex flex-wrap gap-2">
                    <StatusBadge label={t('dashboard.chain')} tone={lnd.synced_to_chain ? 'ok' : 'warn'} />
                    <StatusBadge label={t('dashboard.graph')} tone={lnd.synced_to_graph ? 'ok' : 'warn'} />
                  </div>
                </div>
                <div className="rounded-2xl border border-white/10 bg-white/5 p-3">
                  <p className="text-xs uppercase tracking-wide text-fog/45">{t('dashboard.connectedPeersLabel')}</p>
                  <div className="mt-3 flex items-start justify-between gap-3">
                    <p className="text-2xl font-semibold">{formatSats(locale, connectedPeerCount)}</p>
                    <div className="flex flex-wrap justify-end gap-2">
                      <StatusBadge label={t('dashboard.peerCountByType', { type: t('dashboard.clearnetLabel'), value: formatSats(locale, clearnetPeerCount) })} tone="ok" />
                      <StatusBadge label={t('dashboard.peerCountByType', { type: t('dashboard.torLabel'), value: formatSats(locale, torPeerCount) })} tone="muted" />
                    </div>
                  </div>
                </div>
              </div>

              <div>
                <div className="mb-2 flex items-center justify-between text-sm">
                  <span className="text-fog/70">{t('dashboard.channels')}</span>
                  <span className="text-fog">{t('dashboard.channelsSummary', {
                    active: lnd.channels?.active ?? 0,
                    inactive: lnd.channels?.inactive ?? 0,
                  })}</span>
                </div>
                <StackedRatioBar
                  compact
                  segments={[
                    { label: t('dashboard.activeCount', { count: lnd.channels?.active ?? 0 }), value: lnd.channels?.active ?? 0, tone: 'ok' },
                    { label: t('dashboard.inactiveCount', { count: lnd.channels?.inactive ?? 0 }), value: lnd.channels?.inactive ?? 0, tone: 'warn' },
                  ]}
                />
              </div>

              <div>
                <div className="mb-2 flex items-center justify-between text-sm">
                  <span className="text-fog/70">{t('dashboard.liquiditySplit')}</span>
                  <span className="text-fog">{t('dashboard.outboundShareLabel', { value: formatPercent(locale, outboundSharePct) })}</span>
                </div>
                <p className="mb-2 text-xs text-fog/55">{t('dashboard.outboundInboundSummary', {
                  outbound: formatSats(locale, outboundLiquiditySat),
                  inbound: formatSats(locale, inboundLiquiditySat),
                })}</p>
                <StackedRatioBar
                  compact
                  segments={[
                    { label: t('dashboard.outboundLabel'), value: outboundLiquiditySat, tone: 'ok' },
                    { label: t('dashboard.inboundLabel'), value: inboundLiquiditySat, tone: 'info' },
                  ]}
                />
              </div>

              {lnd.db_backend === 'bolt' && (
                <div className="flex items-center justify-between rounded-2xl border border-white/10 bg-white/5 px-4 py-3 text-sm">
                  <span className="text-fog/70">{t('dashboard.channelDbSize')}</span>
                  <span>{formatGB(locale, lnd.channel_db_size_gb, 2)}</span>
                </div>
              )}

              {(lnd.pubkey || lndUris.length > 0) && (
                <details className="rounded-2xl border border-white/10 bg-white/5 p-4">
                  <summary className="cursor-pointer text-sm text-fog/75">{t('dashboard.connectionDetails')}</summary>
                  <div className="mt-3 space-y-2 text-sm">
                    {lnd.pubkey && (
                      <div className="flex items-center justify-between gap-3">
                        <span className="text-fog/60">{t('dashboard.pubkey')}</span>
                        <div className="flex items-center gap-2">
                          <span className="max-w-[220px] truncate font-mono text-fog/75" title={lnd.pubkey}>{compactValue(lnd.pubkey)}</span>
                          <button className="text-fog/55 hover:text-fog" type="button" onClick={() => copyToClipboard(lnd.pubkey || '')}>
                            {t('common.copy')}
                          </button>
                        </div>
                      </div>
                    )}
                    {lndUris.map((uri, idx) => (
                      <div key={uri} className="flex items-center justify-between gap-3">
                        <span className="text-fog/60">{idx === 0 ? t('dashboard.uri') : ''}</span>
                        <div className="flex items-center gap-2">
                          <span className="max-w-[220px] truncate font-mono text-fog/75" title={uri}>{compactValue(uri)}</span>
                          <button className="text-fog/55 hover:text-fog" type="button" onClick={() => copyToClipboard(uri)}>
                            {t('common.copy')}
                          </button>
                        </div>
                      </div>
                    ))}
                  </div>
                </details>
              )}
            </div>
          ) : (
            <p className="mt-4 text-sm text-fog/60">{t('dashboard.loadingLndStatus')}</p>
          )}
        </article>

        <article className="section-card">
          <div className="flex items-start justify-between gap-3">
            <div>
              <h3 className="text-lg font-semibold">{isLocalBitcoin ? t('dashboard.bitcoinLocal') : t('dashboard.bitcoinRemote')}</h3>
              <p className="mt-1 text-sm text-fog/60">{bitcoin?.rpchost || bitcoin?.data_dir || t('dashboard.loadingBitcoinStatus')}</p>
            </div>
            <StatusBadge
              label={
                bitcoin?.rpc_ok
                  ? t('common.ok')
                  : isLocalBitcoin
                    ? (bitcoin?.status || t('common.na'))
                    : t('common.fail')
              }
              tone={bitcoin?.rpc_ok ? 'ok' : toneFromStatusText(bitcoin?.status)}
              size="md"
            />
          </div>
          {bitcoin ? (
            <div className="mt-5 space-y-4">
              <HorizontalBarGauge
                label={t('dashboard.sync')}
                value={(bitcoin.verification_progress ?? 0) * 100}
                max={100}
                valueLabel={`${formatPercent(locale, (bitcoin.verification_progress ?? 0) * 100, 2)}%`}
                detail={bitcoin.best_block_time ? `${t('dashboard.lastBlockSeen')}: ${formatTimeAgo(locale, bitcoin.best_block_time)}` : undefined}
                tone={bitcoin.rpc_ok ? 'ok' : 'warn'}
              />

              <div className="grid gap-3 sm:grid-cols-2">
                <div className="rounded-2xl border border-white/10 bg-white/5 p-3 text-sm">
                  <p className="text-fog/60">{t('dashboard.chain')}</p>
                  <p className="mt-2 text-lg font-semibold">{bitcoin.chain || t('common.na')}</p>
                </div>
                <div className="rounded-2xl border border-white/10 bg-white/5 p-3 text-sm">
                  <p className="text-fog/60">{t('dashboard.connections')}</p>
                  <p className="mt-2 text-lg font-semibold">{formatSats(locale, bitcoin.connections)}</p>
                </div>
                <div className="rounded-2xl border border-white/10 bg-white/5 p-3 text-sm">
                  <p className="text-fog/60">{t('dashboard.blocks')}</p>
                  <p className="mt-2 text-lg font-semibold">{formatSats(locale, bitcoin.blocks)}</p>
                </div>
                <div className="rounded-2xl border border-white/10 bg-white/5 p-3 text-sm">
                  <p className="text-fog/60">{t('dashboard.version')}</p>
                  <p className="mt-2 text-lg font-semibold">{bitcoin.subversion || bitcoin.version || t('common.na')}</p>
                </div>
              </div>

              {cadenceBuckets.length > 0 && (
                <div className="rounded-2xl border border-white/10 bg-white/5 p-4">
                  <div className="mb-3 flex items-center justify-between gap-3">
                    <p className="text-sm font-medium">{t('dashboard.blockCadence')}</p>
                    <span className="text-xs text-fog/55">{t('dashboard.avgBlocksPerHour', { avg: cadenceAvg.toFixed(1) })}</span>
                  </div>
                  <div className="block-cadence-chart">
                    {cadenceBuckets.map((bucket, idx) => {
                      const height = Math.max(8, Math.round((bucket.count / maxCadence) * 100))
                      return (
                        <div className="block-cadence-bar" key={`cadence-${idx}`} title={`${bucket.count} blocks`}>
                          <div className="block-cadence-fill" style={{ height: `${height}%` }} />
                        </div>
                      )
                    })}
                  </div>
                  <div className="mt-2 flex items-center justify-between text-xs text-fog/50">
                    <span>{t('dashboard.blocksPerWindow', { total: cadenceTotal, hours: cadenceHours.toFixed(1) })}</span>
                    <span>{bitcoin.best_block_time ? formatTimestamp(locale, bitcoin.best_block_time) : '-'}</span>
                  </div>
                </div>
              )}

              <details className="rounded-2xl border border-white/10 bg-white/5 p-4">
                <summary className="cursor-pointer text-sm text-fog/75">{t('dashboard.moreDetails')}</summary>
                <div className="mt-3 grid gap-2 text-sm sm:grid-cols-2">
                  <div className="flex items-center justify-between gap-3">
                    <span className="text-fog/60">{t('dashboard.rpc')}</span>
                    <StatusBadge label={bitcoin.rpc_ok ? t('common.ok') : t('common.fail')} tone={bitcoin.rpc_ok ? 'ok' : 'warn'} />
                  </div>
                  <div className="flex items-center justify-between gap-3">
                    <span className="text-fog/60">{t('dashboard.pruned')}</span>
                    <span>{bitcoin.pruned ? t('common.yes') : t('common.no')}</span>
                  </div>
                  <div className="flex items-center justify-between gap-3">
                    <span className="text-fog/60">{t('dashboard.sizeOnDisk')}</span>
                    <span>{typeof bitcoin.size_on_disk === 'number' ? formatGB(locale, bitcoin.size_on_disk / (1024 * 1024 * 1024), 1) : '-'}</span>
                  </div>
                  <div className="flex items-center justify-between gap-3">
                    <span className="text-fog/60">{t('dashboard.lastBlockSeen')}</span>
                    <span>{bitcoin.best_block_time ? formatTimeAgo(locale, bitcoin.best_block_time) : '-'}</span>
                  </div>
                </div>
              </details>
            </div>
          ) : (
            <p className="mt-4 text-sm text-fog/60">{t('dashboard.loadingBitcoinStatus')}</p>
          )}
        </article>
      </div>

      <div className="grid gap-6 xl:grid-cols-2">
        <article className="section-card">
          <div className="flex items-start justify-between gap-3">
            <div>
              <h3 className="text-lg font-semibold">{t('dashboard.postgres')}</h3>
              <p className="mt-1 text-sm text-fog/60">{postgres?.version || t('dashboard.loadingPostgresStatus')}</p>
            </div>
            <StatusBadge
              label={postgres?.service_active ? t('common.active') : t('common.inactive')}
              tone={postgres?.service_active ? 'ok' : 'warn'}
              size="md"
            />
          </div>
          {postgres ? (
            <div className="mt-5 grid gap-3 sm:grid-cols-2">
              {(Array.isArray(postgres.databases) && postgres.databases.length > 0 ? postgres.databases : [{
                name: postgres.db_name,
                size_mb: postgres.db_size_mb,
                connections: postgres.connections,
                available: postgres.service_active,
              }]).map((database) => (
                <div key={`${database.name}-${database.source || 'default'}`} className="rounded-2xl border border-white/10 bg-white/5 p-4">
                  <div className="flex items-center justify-between gap-3">
                    <p className="text-sm font-medium text-fog/85">{database.name || t('common.na')}</p>
                    {database.source ? <StatusBadge label={database.source.toUpperCase()} tone="muted" /> : null}
                  </div>
                  <div className="mt-4 space-y-2 text-sm">
                    <div className="flex items-center justify-between gap-3">
                      <span className="text-fog/60">{t('dashboard.dbSize')}</span>
                      <span>{formatMB(locale, database.size_mb)}</span>
                    </div>
                    <div className="flex items-center justify-between gap-3">
                      <span className="text-fog/60">{t('dashboard.connections')}</span>
                      <span>{formatSats(locale, database.connections)}</span>
                    </div>
                    <div className="flex items-center justify-between gap-3">
                      <span className="text-fog/60">{t('dashboard.service')}</span>
                      <StatusBadge label={database.available ? t('common.ok') : t('common.fail')} tone={database.available ? 'ok' : 'warn'} />
                    </div>
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <p className="mt-4 text-sm text-fog/60">{t('dashboard.loadingPostgresStatus')}</p>
          )}
        </article>

        <article className="section-card">
          <div className="flex items-start justify-between gap-3">
            <div>
              <h3 className="text-lg font-semibold">{t('dashboard.system')}</h3>
              <p className="mt-1 text-sm text-fog/60">{system ? t('dashboard.uptimeHours', { count: Math.round((system.uptime_sec ?? 0) / 3600) }) : t('dashboard.loadingSystemInfo')}</p>
            </div>
            <StatusBadge label={system ? t('common.ok') : t('common.na')} tone={system ? 'ok' : 'muted'} size="md" />
          </div>
          {system ? (
            <div className="mt-5 space-y-4">
              <HorizontalBarGauge
                label={t('dashboard.cpuLoad')}
                value={cpuPercent}
                max={100}
                valueLabel={`${formatPercent(locale, cpuPercent)}%`}
                detail={`${formatPercent(locale, system.cpu_load_1, 2)} load`}
                tone={cpuPercent >= 85 ? 'danger' : cpuPercent >= 65 ? 'warn' : 'ok'}
              />
              <HorizontalBarGauge
                label={t('dashboard.ramUsed')}
                value={system.ram_used_mb ?? 0}
                max={Math.max(1, system.ram_total_mb ?? 1)}
                valueLabel={`${formatMB(locale, system.ram_used_mb)} / ${formatMB(locale, system.ram_total_mb)}`}
                tone={((system.ram_used_mb ?? 0) / Math.max(1, system.ram_total_mb ?? 1)) >= 0.85 ? 'danger' : 'info'}
              />
              <HorizontalBarGauge
                label={t('dashboard.temp')}
                value={system.temperature_c ?? 0}
                max={100}
                valueLabel={formatTemp(locale, system.temperature_c)}
                tone={(system.temperature_c ?? 0) >= 75 ? 'danger' : (system.temperature_c ?? 0) >= 60 ? 'warn' : 'ok'}
              />
            </div>
          ) : (
            <p className="mt-4 text-sm text-fog/60">{t('dashboard.loadingSystemInfo')}</p>
          )}
        </article>
      </div>

      <article className="section-card">
        <div className="flex items-start justify-between gap-3">
          <div>
            <h3 className="text-lg font-semibold">{t('dashboard.disks')}</h3>
            <p className="mt-1 text-sm text-fog/60">{disks.length > 0 ? t('dashboard.diskHealthSummary', { count: disks.length }) : t('dashboard.noDiskData')}</p>
          </div>
        </div>
        {disks.length > 0 ? (
          <div className="mt-5 grid gap-4">
            {disks.map((disk) => {
              const usedPercent = disk.used_percent ?? 0
              const wearPercent = disk.wear_percent_used ?? 0
              const partitions = Array.isArray(disk.partitions) ? disk.partitions : []
              return (
                <div key={disk.device} className="rounded-3xl border border-white/10 bg-white/5 p-4">
                  <div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
                    <div>
                      <div className="flex flex-wrap items-center gap-2">
                        <h4 className="text-base font-semibold">{disk.device}</h4>
                        <StatusBadge label={(disk.type || 'disk').toUpperCase()} tone="muted" />
                        <StatusBadge label={disk.smart_status || t('common.na')} tone={toneFromSmartStatus(disk.smart_status)} />
                      </div>
                      <p className="mt-2 text-sm text-fog/60">{t('dashboard.diskUsageSummary', {
                        total: formatGB(locale, disk.total_gb),
                        used: formatGB(locale, disk.used_gb),
                        percent: formatPercent(locale, usedPercent),
                      })}</p>
                    </div>
                    <div className="grid gap-2 text-sm text-fog/65 sm:grid-cols-3">
                      <div>{t('dashboard.powerOnHours', { count: disk.power_on_hours ?? 0 })}</div>
                      <div>{t('dashboard.wearDaysLeft', { wear: wearPercent, days: disk.days_left_estimate ?? 0 })}</div>
                      <div>{t('disks.temp')}: {formatTemp(locale, disk.temperature_c)}</div>
                    </div>
                  </div>

                  <div className="mt-4 grid gap-4 lg:grid-cols-2">
                    <HorizontalBarGauge
                      label={t('dashboard.capacityUsed')}
                      value={usedPercent}
                      max={100}
                      valueLabel={`${formatPercent(locale, usedPercent)}%`}
                      tone={usedPercent >= 90 ? 'danger' : usedPercent >= 75 ? 'warn' : 'ok'}
                    />
                    <HorizontalBarGauge
                      label={t('dashboard.wear')}
                      value={wearPercent}
                      max={100}
                      valueLabel={`${formatPercent(locale, wearPercent)}%`}
                      tone={wearPercent >= 80 ? 'danger' : wearPercent >= 60 ? 'warn' : 'ok'}
                    />
                  </div>

                  {partitions.length > 0 && (
                    <details className="mt-4 rounded-2xl border border-white/10 bg-ink/30 p-4">
                      <summary className="cursor-pointer text-sm text-fog/75">{t('dashboard.partitionDetails')}</summary>
                      <div className="mt-3 space-y-2 text-sm">
                        {partitions.map((partition) => (
                          <div key={partition.device} className="flex flex-col gap-1 rounded-2xl border border-white/8 bg-white/5 px-3 py-2 sm:flex-row sm:items-center sm:justify-between">
                            <div className="flex flex-wrap items-center gap-2">
                              <span className="font-mono text-fog/85">{partition.device}</span>
                              {partition.mount ? <span className="text-fog/50">{partition.mount}</span> : null}
                            </div>
                            <div className="flex flex-wrap items-center gap-3 text-fog/60">
                              <span>{t('disks.size')}: {formatGB(locale, partition.total_gb)}</span>
                              <span>{t('disks.used')}: {formatGB(locale, partition.used_gb)} ({formatPercent(locale, partition.used_percent)}%)</span>
                            </div>
                          </div>
                        ))}
                      </div>
                    </details>
                  )}
                </div>
              )
            })}
          </div>
        ) : (
          <p className="mt-4 text-sm text-fog/60">{t('dashboard.noDiskData')}</p>
        )}
      </article>
    </div>
  )
}
