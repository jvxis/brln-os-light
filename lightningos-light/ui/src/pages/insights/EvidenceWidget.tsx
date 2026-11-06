import { useTranslation } from 'react-i18next'
import type { InsightEvidence } from '../../api'
import HealthGauge from '../../components/rebalance/HealthGauge'

type Props = {
  evidence: InsightEvidence
  teach: boolean
  formatNumber: (value: number) => string
}

const compareKind = (key: string): 'target' | 'cap' | 'min' | 'max' => {
  switch (key) {
    case 'local_pct':
    case 'hours_to_drain':
      return 'target'
    case 'rebal_cost_ppm_7d':
    case 'sell_ppm_7d':
      return 'cap'
    case 'lot_sold_pct':
    case 'assisted_fee_30d_sat':
      return 'max'
    default:
      return 'min'
  }
}

// One widget per evidence item. The key decides the shape: percentages get
// a gauge with the target marked, prices and amounts with a reference get a
// bar on a shared scale, everything else is a plain labelled number.
export default function EvidenceWidget({ evidence, teach, formatNumber }: Props) {
  const { t } = useTranslation()
  const label = t(`insights.evidence.${evidence.key}` as any, { defaultValue: evidence.key })
  const unitSuffix = evidence.unit === 'pct' ? '%' : evidence.unit === 'ppm' ? ' ppm' : evidence.unit === 'sat' ? ' sat' : evidence.unit === 'h' ? ' h' : evidence.unit === 'x' ? '×' : ''
  const fmt = (value: number) => `${formatNumber(value)}${unitSuffix}`
  const hasCompare = typeof evidence.compare === 'number' && evidence.compare > 0
  const compareLabel = hasCompare ? t(`insights.evidence.compare.${compareKind(evidence.key)}`, { value: fmt(evidence.compare as number) }) : ''
  const source = teach ? <p className="text-[10px] text-fog/45">{t('insights.card.source', { value: t(`insights.sources.${evidence.source}` as any, { defaultValue: evidence.source }) })}</p> : null

  if (evidence.key === 'autopilot_reason') {
    return (
      <div className="min-w-0">
        <div className="flex items-baseline justify-between gap-2 text-[11px]">
          <span className="uppercase tracking-wide text-fog/55">{label}</span>
          <span className="truncate rounded-full border border-white/10 bg-white/5 px-2 py-0.5 font-mono text-[10px] text-fog/80">{evidence.extra || '—'}</span>
        </div>
        {source}
      </div>
    )
  }

  if (evidence.key === 'jobs_24h') {
    const [failed, partial] = (evidence.extra || '0/0').split('/')
    return (
      <div className="min-w-0">
        <div className="flex items-baseline justify-between gap-2 text-[11px]">
          <span className="uppercase tracking-wide text-fog/55">{label}</span>
          <span className="font-medium text-fog">{formatNumber(evidence.value)}</span>
        </div>
        <p className="text-[10px] text-rose-200">{t('insights.evidence.jobs_24h_extra', { failed, partial })}</p>
        {source}
      </div>
    )
  }

  if (evidence.unit === 'pct') {
    const tone = hasCompare && evidence.value < (evidence.compare as number) ? 'warn' : 'info'
    return (
      <div className="min-w-0">
        <HealthGauge label={label} value={evidence.value} target={hasCompare ? evidence.compare : undefined} targetLabel={compareLabel || undefined} valueLabel={fmt(evidence.value)} tone={tone} />
        {source}
      </div>
    )
  }

  if (hasCompare) {
    const scale = Math.max(evidence.value, evidence.compare as number, 1)
    const valuePct = Math.max(2, Math.min(100, (evidence.value / scale) * 100))
    const markerPct = Math.max(0, Math.min(100, ((evidence.compare as number) / scale) * 100))
    const over = evidence.value > (evidence.compare as number)
    const barClass = compareKind(evidence.key) === 'max' ? (over ? 'bg-rose-400/80' : 'bg-emerald-400/80') : over ? 'bg-emerald-400/80' : 'bg-amber-400/80'
    return (
      <div className="min-w-0 space-y-1">
        <div className="flex items-baseline justify-between gap-2 text-[11px]">
          <span className="uppercase tracking-wide text-fog/55">{label}</span>
          <span className="font-medium text-fog">{fmt(evidence.value)}</span>
        </div>
        <div className="relative h-2 overflow-hidden rounded-full bg-white/10">
          <div className={`h-full rounded-full ${barClass}`} style={{ width: `${valuePct}%` }} />
          <div className="absolute top-0 h-full w-0.5 bg-fog/80" style={{ left: `calc(${markerPct}% - 1px)` }} />
        </div>
        <p className="text-[10px] text-fog/45">{compareLabel}{evidence.extra ? ` · ${evidence.extra}` : ''}</p>
        {source}
      </div>
    )
  }

  return (
    <div className="min-w-0">
      <div className="flex items-baseline justify-between gap-2 text-[11px]">
        <span className="uppercase tracking-wide text-fog/55">{label}</span>
        <span className="font-medium text-fog">{fmt(evidence.value)}{evidence.extra ? <span className="text-fog/50"> · {evidence.extra}</span> : null}</span>
      </div>
      {source}
    </div>
  )
}
