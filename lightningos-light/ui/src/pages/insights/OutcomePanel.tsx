import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getInsightOutcomes, type InsightOutcome, type InsightRuleScore, type InsightScorecard } from '../../api'
import StatusBadge from '../../components/dashboard/StatusBadge'
import { insightChannelHash } from './links'

type Props = {
  scorecard: InsightRuleScore[]
  teach: boolean
  formatNumber: (value: number) => string
  formatRelative: (iso: string) => string
  onRule: (rule: string) => void
}

const VERDICT_TONE: Record<string, 'ok' | 'warn' | 'danger' | 'muted' | 'info'> = {
  hit: 'ok',
  neutral: 'warn',
  miss: 'danger',
  confirmed: 'info',
  unneeded: 'muted',
}

const netOf = (m: { direct_fee_sat: number; assisted_fee_sat: number; rebal_cost_sat: number }) => m.direct_fee_sat + m.assisted_fee_sat - m.rebal_cost_sat

// Phase 2 of Insights: what happened a week after each decision, and how
// often each rule lands. Read-only; the numbers come from the same ledger the
// Reports use (forwards and settled rebalances).
export default function OutcomePanel({ scorecard, teach, formatNumber, formatRelative, onRule }: Props) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [card, setCard] = useState<InsightScorecard | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    if (!open || card) return
    setLoading(true)
    getInsightOutcomes()
      .then((data) => { setCard(data); setError('') })
      .catch((err: any) => setError(err?.message || t('insights.unavailable')))
      .finally(() => setLoading(false))
  }, [open, card, t])

  const measured = scorecard.reduce((sum, rule) => sum + rule.measured, 0)
  const pending = scorecard.reduce((sum, rule) => sum + rule.pending, 0)
  const hits = scorecard.reduce((sum, rule) => sum + rule.hits + rule.confirmed, 0)
  const accuracy = measured > 0 ? Math.round((hits / measured) * 100) : 0

  const barWidth = (value: number, max: number) => `${max > 0 ? Math.max(2, Math.round((Math.abs(value) / max) * 100)) : 0}%`

  const renderOutcome = (outcome: InsightOutcome) => {
    const alias = outcome.peer_alias || (outcome.peer_pubkey ? `${outcome.peer_pubkey.slice(0, 10)}…` : outcome.channel_point || '')
    const before = netOf(outcome.before)
    const after = outcome.after ? netOf(outcome.after) : null
    const max = Math.max(Math.abs(before), Math.abs(after ?? 0), 1)
    const verdict = outcome.status === 'pending' ? 'pending' : outcome.verdict || 'pending'
    return (
      <li key={outcome.id} className="grid gap-2 rounded-2xl border border-white/10 bg-white/[0.03] p-3 text-xs sm:grid-cols-[1fr_auto]">
        <div className="min-w-0 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="rounded-lg border border-white/10 bg-white/5 px-1.5 py-0.5 font-mono text-[10px] text-fog/80">{outcome.rule}</span>
            <a href={insightChannelHash({ channel_point: outcome.channel_point, peer_pubkey: outcome.peer_pubkey } as any)} className="truncate font-semibold text-fog hover:text-glow">
              {t(`insights.kind.${outcome.kind}.title`, { alias })}
            </a>
            <StatusBadge tone={outcome.decision === 'done' ? 'ok' : 'muted'} label={outcome.decision === 'done' ? t('insights.outcomes.decisionDone') : t('insights.outcomes.decisionDismissed', { reason: t(`insights.dismissReason.${outcome.decision_reason}` as any, { defaultValue: outcome.decision_reason || '' }) })} />
            <StatusBadge tone={VERDICT_TONE[verdict] || 'muted'} label={t(`insights.outcomes.verdict.${verdict}` as any, { defaultValue: verdict })} />
          </div>
          <p className="text-fog/60">
            {t('insights.outcomes.decided', { value: formatRelative(outcome.decided_at) })}
            {outcome.status === 'pending' ? ` · ${t('insights.outcomes.measures', { value: formatRelative(outcome.measure_at) })}` : ''}
            {outcome.measurement_error ? ` · ${t('insights.outcomes.measureError')}` : ''}
          </p>
          <div className="grid gap-1">
            <div className="flex items-center gap-2">
              <span className="w-14 shrink-0 text-[10px] uppercase tracking-wide text-fog/50">{t('insights.outcomes.before')}</span>
              <div className="h-2 flex-1 overflow-hidden rounded-full bg-white/10"><div className={`h-full rounded-full ${before >= 0 ? 'bg-fog/40' : 'bg-rose-400/60'}`} style={{ width: barWidth(before, max) }} /></div>
              <span className="w-24 shrink-0 text-right font-mono text-fog/75">{formatNumber(before)} sat</span>
            </div>
            <div className="flex items-center gap-2">
              <span className="w-14 shrink-0 text-[10px] uppercase tracking-wide text-fog/50">{t('insights.outcomes.after')}</span>
              <div className="h-2 flex-1 overflow-hidden rounded-full bg-white/10">{after !== null && <div className={`h-full rounded-full ${after >= before ? 'bg-emerald-400/70' : 'bg-rose-400/70'}`} style={{ width: barWidth(after, max) }} />}</div>
              <span className="w-24 shrink-0 text-right font-mono text-fog/75">{after !== null ? `${formatNumber(after)} sat` : '—'}</span>
            </div>
          </div>
          {teach && (
            <p className="text-[11px] text-fog/50">
              {t('insights.outcomes.windowHint', { days: outcome.window_days })}
              {outcome.after ? ` · ${t('insights.outcomes.detail', { direct: formatNumber(outcome.after.direct_fee_sat), assisted: formatNumber(outcome.after.assisted_fee_sat), rebal: formatNumber(outcome.after.rebal_cost_sat) })}` : ''}
            </p>
          )}
        </div>
        <div className="flex flex-row flex-wrap items-start gap-3 text-right sm:flex-col sm:items-end">
          <div>
            <p className="text-[10px] uppercase tracking-wide text-fog/50">{t('insights.outcomes.predicted')}</p>
            <p className="font-mono text-fog/85">{outcome.kind === 'parar_compra' ? '−' : '+'}{formatNumber(outcome.predicted_sat_day)} sat/d</p>
          </div>
          <div>
            <p className="text-[10px] uppercase tracking-wide text-fog/50">{t('insights.outcomes.realized')}</p>
            <p className={`font-mono ${outcome.realized_sat_day === undefined ? 'text-fog/50' : (outcome.realized_sat_day ?? 0) > 0 ? 'text-emerald-200' : 'text-rose-200'}`}>
              {outcome.realized_sat_day === undefined || outcome.realized_sat_day === null ? '—' : `${outcome.realized_sat_day > 0 ? '+' : ''}${formatNumber(outcome.realized_sat_day)} sat/d`}
            </p>
          </div>
          {outcome.error_pct !== undefined && outcome.error_pct !== null && (
            <div>
              <p className="text-[10px] uppercase tracking-wide text-fog/50">{t('insights.outcomes.error')}</p>
              <p className="font-mono text-fog/75">{formatNumber(outcome.error_pct)}%</p>
            </div>
          )}
        </div>
      </li>
    )
  }

  return (
    <section className="rounded-3xl border border-white/10 bg-ink/60 p-4 sm:p-5">
      <div className="flex flex-wrap items-center gap-3">
        <div className="min-w-0">
          <h3 className="text-base font-semibold text-fog">{t('insights.outcomes.title')}</h3>
          <p className="text-xs text-fog/60">
            {measured === 0 && pending === 0
              ? t('insights.outcomes.none')
              : t('insights.outcomes.summary', { measured, accuracy, pending })}
          </p>
        </div>
        <span className="flex-1" />
        <button type="button" className="btn-secondary px-3 py-1 text-xs" onClick={() => setOpen((v) => !v)}>
          {open ? t('common.hide') : t('insights.outcomes.open')}
        </button>
      </div>
      {teach && <p className="mt-2 max-w-3xl text-xs text-fog/55">{t('insights.outcomes.teach')}</p>}

      <div className="mt-4 overflow-x-auto">
        <table className="w-full min-w-[560px] text-left text-xs">
          <thead className="text-[10px] uppercase tracking-wide text-fog/50">
            <tr>
              <th className="py-1 pr-3 font-medium">{t('insights.outcomes.table.rule')}</th>
              <th className="py-1 pr-3 font-medium">{t('insights.outcomes.table.measured')}</th>
              <th className="py-1 pr-3 font-medium">{t('insights.outcomes.table.followed')}</th>
              <th className="py-1 pr-3 font-medium">{t('insights.outcomes.table.dismissed')}</th>
              <th className="py-1 pr-3 font-medium">{t('insights.outcomes.table.accuracy')}</th>
              <th className="py-1 pr-3 font-medium">{t('insights.outcomes.table.error')}</th>
              <th className="py-1 pr-3 font-medium">{t('insights.outcomes.table.weight')}</th>
            </tr>
          </thead>
          <tbody>
            {scorecard.map((rule) => (
              <tr key={rule.rule} className="border-t border-white/5">
                <td className="py-1.5 pr-3">
                  <button type="button" className="font-mono text-fog hover:text-glow" onClick={() => onRule(rule.rule)}>{rule.rule}</button>
                  <span className="ml-2 text-fog/55">{t(`insights.glossary.rules.${rule.rule}.name`)}</span>
                </td>
                <td className="py-1.5 pr-3 font-mono text-fog/80">{rule.measured}{rule.pending ? <span className="text-fog/45"> +{rule.pending}</span> : null}</td>
                <td className="py-1.5 pr-3 font-mono text-fog/80">{rule.followed} <span className="text-fog/45">({rule.hits}/{rule.neutral}/{rule.misses})</span></td>
                <td className="py-1.5 pr-3 font-mono text-fog/80">{rule.dismissed} <span className="text-fog/45">({rule.confirmed}/{rule.unneeded})</span></td>
                <td className="py-1.5 pr-3 font-mono text-fog/80">{rule.measured > 0 ? `${Math.round(rule.accuracy * 100)}%` : '—'}</td>
                <td className="py-1.5 pr-3 font-mono text-fog/80">{rule.followed > 0 && rule.mean_error_pct > 0 ? `${formatNumber(rule.mean_error_pct)}%` : '—'}</td>
                <td className="py-1.5 pr-3 font-mono text-fog/80">{rule.weight_applied ? `×${rule.weight.toFixed(2)}` : <span className="text-fog/45">{t('insights.outcomes.table.weightPending', { count: Math.max(0, 5 - rule.measured) })}</span>}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {teach && <p className="mt-2 text-[11px] text-fog/50">{t('insights.outcomes.table.legend')}</p>}
      </div>

      {open && (
        <div className="mt-4">
          {loading && <p className="text-xs text-fog/60">{t('insights.loading')}</p>}
          {error && <p className="text-xs text-amber-200">{error}</p>}
          {card && card.recent.length === 0 && <p className="text-xs text-fog/60">{t('insights.outcomes.none')}</p>}
          {card && card.recent.length > 0 && <ul className="grid gap-2">{card.recent.map(renderOutcome)}</ul>}
        </div>
      )}
    </section>
  )
}
