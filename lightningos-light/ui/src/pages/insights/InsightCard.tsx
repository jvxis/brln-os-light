import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getLoopQuote, getMempoolFees, type InsightAction, type InsightItem } from '../../api'
import StatusBadge from '../../components/dashboard/StatusBadge'
import EvidenceWidget from './EvidenceWidget'
import { insightActionHash, insightChannelHash } from './links'

// Funding transaction of a plain channel open: one input, two outputs.
const OPEN_CHANNEL_VBYTES = 154

type Props = {
  item: InsightItem
  teach: boolean
  busy: boolean
  formatNumber: (value: number) => string
  formatRelative: (iso: string) => string
  onStatus: (item: InsightItem, status: 'open' | 'dismissed' | 'done', reason?: string) => void
  onRule: (rule: string) => void
  // Error from the last decision on this card. Shown next to the buttons:
  // a banner at the top of the page is out of sight when the card is far down.
  actionError?: string
}

const KIND_ACCENT: Record<InsightItem['kind'], string> = {
  repor_vendedor: 'bg-sky-400',
  mais_capacidade: 'bg-emerald-400',
  parar_compra: 'bg-amber-400',
  capital_parado: 'bg-slate-400',
  abrir_canal: 'bg-violet-400',
  preco_fora: 'bg-rose-400',
  bloqueio_esperado: 'bg-fog/40',
}

const confidenceDots = (level: InsightItem['confidence']) => (level === 'high' ? '●●●' : level === 'medium' ? '●●○' : '●○○')

export default function InsightCard({ item, teach, busy, formatNumber, formatRelative, onStatus, onRule, actionError }: Props) {
  const { t } = useTranslation()
  const [expanded, setExpanded] = useState(teach)
  const [dismissOpen, setDismissOpen] = useState(false)
  const [quoteState, setQuoteState] = useState<{ loading: boolean; text?: string; verdict?: string; tooExpensive?: string; alternative?: string; error?: string }>({ loading: false })
  const [copied, setCopied] = useState(false)
  const open = teach || expanded
  const alias = item.peer_alias || (item.peer_pubkey ? `${item.peer_pubkey.slice(0, 10)}…` : item.channel_point || '')
  const title = t(`insights.kind.${item.kind}.title`, { alias })
  const hidden = item.status !== 'open'
  const sellPpm = item.evidence.find((e) => e.key === 'sell_ppm_7d')?.value || 0

  const valueLabel = () => {
    if (item.kind === 'bloqueio_esperado') return t('insights.card.valueNone')
    if (item.kind === 'parar_compra') return t('insights.card.valueAvoid', { value: formatNumber(item.value_sat_day) })
    return t('insights.card.value', { value: formatNumber(item.value_sat_day) })
  }

  const stepText = (action: InsightAction) => {
    const key = action.tool === 'rebalance_target' && action.params?.suggest === 'disable_target' ? 'rebalance_target_disable' : action.tool
    const limit = action.params?.amount_limit
    return t(`insights.tool.${key}.step` as any, {
      ratio: action.params?.ratio,
      cap: action.params?.cap_ppm ? formatNumber(Number(action.params.cap_ppm)) : '',
      margin: action.params?.margin_pct,
      reason: action.params?.reason,
      amount: action.params?.amount_sat ? formatNumber(Number(action.params.amount_sat)) : '',
      limit: limit ? t(`insights.card.amountLimit.${limit}` as any, { defaultValue: limit }) : '',
      defaultValue: action.tool,
    })
  }

  // The quote is judged against the same ceiling the rebalance pays (cap_ppm).
  // Above it the swap does not pay; the card then prices the alternative the
  // operator would reach for anyway: a new channel at today's chain fee.
  const runQuote = async (action: InsightAction) => {
    const amount = Number(action.params?.amount_sat || 0)
    if (!amount) return
    setQuoteState({ loading: true })
    try {
      const quote = await getLoopQuote({ direction: 'in', amount_sat: amount, last_hop_pubkey: action.params?.last_hop_pubkey })
      const fee = quote.estimated_all_in_fee_sat ?? quote.estimated_fee_sat
      const ppm = Math.round((fee / amount) * 1e6)
      const cap = Number(action.params?.cap_ppm || 0)
      const base = t('insights.card.quoteResult', { fee: formatNumber(fee), amount: formatNumber(amount), ppm: formatNumber(ppm), margin: formatNumber(sellPpm - ppm) })
      if (!cap || ppm <= cap) {
        setQuoteState({ loading: false, text: base, verdict: cap ? t('insights.card.quoteWithinCap', { cap: formatNumber(cap) }) : undefined })
        return
      }
      let alternative: string | undefined
      try {
        const fees = await getMempoolFees()
        const satPerVb = Number(fees?.economyFee || fees?.hourFee || 0)
        if (satPerVb > 0) {
          const openCost = Math.ceil(satPerVb * OPEN_CHANNEL_VBYTES)
          alternative = t('insights.card.quoteOpenAlternative', { cost: formatNumber(openCost), rate: satPerVb, ppm: formatNumber(Math.round((openCost / amount) * 1e6)) })
        }
      } catch {
        alternative = undefined
      }
      setQuoteState({ loading: false, text: base, tooExpensive: t('insights.card.quoteAboveCap', { cap: formatNumber(cap), sale: formatNumber(sellPpm) }), alternative })
    } catch (err: any) {
      setQuoteState({ loading: false, error: t('insights.card.quoteError', { value: err?.message || 'error' }) })
    }
  }

  const copyPubkey = async (value?: string) => {
    if (!value) return
    try {
      await navigator.clipboard.writeText(value)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1500)
    } catch {
      setCopied(false)
    }
  }

  return (
    <article className={`grid min-w-0 grid-cols-[6px_1fr] overflow-hidden rounded-3xl border border-white/10 bg-ink/60 ${hidden ? 'opacity-60' : ''}`}>
      <div className={KIND_ACCENT[item.kind]} aria-hidden />
      <div className="min-w-0 p-4 sm:p-5">
        <div className="flex flex-wrap items-center gap-2">
          <span className="rounded-lg border border-white/10 bg-white/5 px-2 py-0.5 font-mono text-[11px] text-fog/80">{item.rule}</span>
          <a href={insightChannelHash(item)} className="min-w-0 truncate text-base font-semibold text-fog hover:text-glow">{title}</a>
          <span className="flex-1" />
          <button type="button" className="text-xs text-fog/60 hover:text-fog" onClick={() => setExpanded((v) => !v)}>{open ? t('common.hide') : t('common.open')}</button>
        </div>
        <div className="mt-2 flex flex-wrap items-center gap-2 text-[11px]">
          <StatusBadge label={t(`insights.kind.${item.kind}.label`)} tone="info" />
          <span className="rounded-full border border-white/10 bg-white/5 px-2.5 py-1 text-fog/75">
            {t(`insights.confidence.${item.confidence}`)} <span className="tracking-[2px]">{confidenceDots(item.confidence)}</span>
          </span>
          <span className={`rounded-full border px-2.5 py-1 font-semibold ${item.value_sat_day > 0 ? 'border-emerald-400/30 bg-emerald-500/15 text-emerald-200' : 'border-white/10 bg-white/5 text-fog/70'}`}>{valueLabel()}</span>
          <span className="rounded-full border border-white/10 bg-white/5 px-2.5 py-1 text-fog/60">{t('insights.card.firstSeen', { value: formatRelative(item.first_seen_at) })}</span>
          {item.status === 'dismissed' && <StatusBadge tone="muted" label={t('insights.card.statusDismissed', { reason: t(`insights.dismissReason.${item.dismiss_reason}` as any, { defaultValue: item.dismiss_reason }) })} />}
          {item.status === 'done' && <StatusBadge tone="ok" label={t('insights.card.statusDone')} />}
          {item.status === 'expired' && <StatusBadge tone="muted" label={t('insights.card.statusExpired')} />}
        </div>
        {teach && item.confidence_reason && (
          <p className="mt-1 text-[11px] text-fog/55">
            {Object.entries(item.confidence_reason).map(([key, count]) => t(`insights.confidence.reason.${key}` as any, { count, defaultValue: `${key} ${count}` })).join(' · ')}
          </p>
        )}

        {open && (
          <div className="mt-4 grid gap-5 lg:grid-cols-2">
            <div className="min-w-0 space-y-3">
              <h4 className="text-[11px] uppercase tracking-[0.2em] text-fog/55">{t('insights.card.why')}</h4>
              {item.evidence.map((ev) => (
                <EvidenceWidget key={ev.key} evidence={ev} teach={teach} formatNumber={formatNumber} />
              ))}
            </div>
            <div className="min-w-0 space-y-4">
              <div>
                <h4 className="text-[11px] uppercase tracking-[0.2em] text-fog/55">{t('insights.card.what')}</h4>
                <ol className="mt-2 space-y-2 text-sm text-fog/85">
                  {item.actions.map((action) => (
                    <li key={action.step} className="flex gap-2">
                      <span className="mt-0.5 inline-flex h-5 w-5 flex-none items-center justify-center rounded-full border border-white/15 text-[11px] text-fog/70">{action.step}</span>
                      <div className="min-w-0 flex-1">
                        <p>{stepText(action)}</p>
                        <div className="mt-1 flex flex-wrap items-center gap-2">
                          {action.tool !== 'none' && (
                            <a href={insightActionHash(item, action)} className="text-xs text-emerald-200 hover:text-emerald-100">{t(`insights.tool.${action.tool}.label`)} →</a>
                          )}
                          {action.tool === 'loop_in' && (
                            <>
                              <button type="button" className="btn-secondary px-3 py-1 text-xs" disabled={quoteState.loading} onClick={() => void runQuote(action)}>
                                {quoteState.loading ? t('insights.card.quoting') : t('insights.card.quote')}
                              </button>
                              <button type="button" className="text-xs text-fog/60 hover:text-fog" onClick={() => void copyPubkey(action.params?.last_hop_pubkey)}>
                                {copied ? t('insights.card.copied') : t('insights.card.copyPubkey')}
                              </button>
                            </>
                          )}
                        </div>
                        {action.tool === 'loop_in' && quoteState.text && (
                          <div className={`mt-1 space-y-1 rounded-xl border border-dashed px-2 py-1 text-xs ${quoteState.tooExpensive ? 'border-rose-400/30 text-rose-200' : 'border-white/15 text-emerald-200'}`}>
                            <p>{quoteState.text}</p>
                            {quoteState.verdict && <p>{quoteState.verdict}</p>}
                            {quoteState.tooExpensive && <p>{quoteState.tooExpensive}</p>}
                            {quoteState.alternative && (
                              <p className="text-fog/80">
                                {quoteState.alternative}{' '}
                                <a href={insightActionHash(item, { step: action.step, tool: 'open_channel', params: { peer_pubkey: action.params?.last_hop_pubkey || '', amount_sat: action.params?.amount_sat || '' } })} className="text-emerald-200 hover:text-emerald-100">
                                  {t('insights.tool.open_channel.label')} →
                                </a>
                              </p>
                            )}
                          </div>
                        )}
                        {action.tool === 'loop_in' && quoteState.error && <p className="mt-1 text-xs text-rose-200">{quoteState.error}</p>}
                      </div>
                    </li>
                  ))}
                </ol>
              </div>
              <div>
                <h4 className="text-[11px] uppercase tracking-[0.2em] text-fog/55">{t('insights.card.gone')}</h4>
                <p className="mt-1 text-xs text-fog/70">{t(`insights.invalidate.${item.invalidate_when}` as any, { defaultValue: item.invalidate_when })}</p>
              </div>
              {item.guards && item.guards.length > 0 && (
                <div>
                  <h4 className="text-[11px] uppercase tracking-[0.2em] text-fog/55">{t('insights.card.guards')}</h4>
                  <ul className="mt-1 flex flex-wrap gap-2 text-[11px]">
                    {item.guards.map((guard) => {
                      // Advisory guards are caveats (a cost, a limitation): failed ones warn, they never block.
                      const warn = guard.advisory || guard.code === 'parallel_channel_leaves_fast_path'
                      const tone = guard.passed ? 'ok' : warn ? 'warn' : 'danger'
                      const mark = guard.passed ? '✓' : warn ? '⚠' : '✕'
                      const passedLabel = t(`insights.guard.${guard.code}` as any, { defaultValue: guard.code })
                      const label = guard.passed ? passedLabel : t(`insights.guardFailed.${guard.code}` as any, { defaultValue: passedLabel })
                      return (
                        <li key={guard.code}>
                          <StatusBadge tone={tone} label={`${mark} ${label}`} />
                        </li>
                      )
                    })}
                  </ul>
                </div>
              )}
            </div>
          </div>
        )}

        <div className="mt-4 flex flex-wrap items-center gap-2 text-xs">
          {item.status === 'open' ? (
            <>
              <div className="relative">
                <button type="button" className="btn-secondary px-3 py-1 text-xs" disabled={busy} onClick={() => setDismissOpen((v) => !v)}>{t('insights.card.dismiss')} ▾</button>
                {dismissOpen && (
                  <div className="absolute bottom-full left-0 z-10 mb-1 w-48 rounded-2xl border border-white/10 bg-slate/95 p-1 shadow-panel">
                    {(['later', 'done_manually', 'disagree'] as const).map((reason) => (
                      <button key={reason} type="button" className="block w-full rounded-xl px-3 py-1.5 text-left text-xs text-fog/80 hover:bg-white/5" onClick={() => { setDismissOpen(false); onStatus(item, 'dismissed', reason) }}>
                        {t(`insights.dismissReason.${reason}`)}
                      </button>
                    ))}
                  </div>
                )}
              </div>
              {item.kind !== 'bloqueio_esperado' && (
                <button type="button" className="btn-secondary px-3 py-1 text-xs" disabled={busy} onClick={() => onStatus(item, 'done')}>{t('insights.card.done')}</button>
              )}
            </>
          ) : item.status === 'expired' ? (
            // Resolved on its own: the operator may still record that they did it, which opens the measurement.
            item.kind !== 'bloqueio_esperado' && (
              <button type="button" className="btn-secondary px-3 py-1 text-xs" disabled={busy} onClick={() => onStatus(item, 'done')}>{t('insights.card.doneAfterExpired')}</button>
            )
          ) : (
            <button type="button" className="btn-secondary px-3 py-1 text-xs" disabled={busy} onClick={() => onStatus(item, 'open')}>{t('insights.card.reopen')}</button>
          )}
          <button type="button" className="text-xs text-emerald-200 hover:text-emerald-100" onClick={() => onRule(item.rule)}>{t('insights.card.rule')}</button>
        </div>
        {actionError && (
          <p role="alert" className="mt-2 rounded-xl border border-rose-400/25 bg-rose-400/10 px-3 py-2 text-xs text-rose-200">
            {actionError}
          </p>
        )}
      </div>
    </article>
  )
}
