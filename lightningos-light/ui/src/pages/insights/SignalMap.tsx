import { useTranslation } from 'react-i18next'
import type { InsightSource } from '../../api'
import { insightSourceHash } from './links'

const ORDER = ['autofee', 'interlock', 'rebalance', 'ranking', 'open_candidates']
const STALE_AFTER_SEC = 6 * 3600

type Props = { sources: InsightSource[]; teach: boolean; formatAge: (seconds: number) => string }

// Five boxes feeding one: a picture of where every number on the page
// comes from. Colours never carry the meaning alone; the dot and the word
// say the same thing.
export default function SignalMap({ sources, teach, formatAge }: Props) {
  const { t } = useTranslation()
  const byId = new Map(sources.map((s) => [s.id, s]))
  const boxes = ORDER.map((id) => {
    const src = byId.get(id)
    const ok = Boolean(src?.ok) && (src?.age_sec === undefined || src.age_sec < STALE_AFTER_SEC)
    const state = !src || !src.ok ? 'missing' : ok ? 'ok' : 'stale'
    return { id, src, state, label: t(`insights.sources.${id}` as any, { defaultValue: id }) }
  })

  return (
    <section className="rounded-3xl border border-white/10 bg-ink/60 p-4 sm:p-5">
      <h3 className="text-xs uppercase tracking-[0.2em] text-fog/55">{t('insights.sources.title')}</h3>
      <div className="mt-3 hidden md:block">
        <svg viewBox="0 0 900 110" width="100%" role="img" aria-label={t('insights.sources.title')}>
          <defs>
            <marker id="insight-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto">
              <path d="M0 0L10 5L0 10z" fill="rgb(var(--color-fog) / 0.5)" />
            </marker>
          </defs>
          {boxes.map((box, i) => {
            const x = 10 + i * 135
            const dotFill = box.state === 'ok' ? '#34d399' : box.state === 'stale' ? '#f59e0b' : 'none'
            const dotStroke = box.state === 'ok' ? '#34d399' : box.state === 'stale' ? '#f59e0b' : 'rgb(var(--color-fog) / 0.5)'
            return (
              <a key={box.id} href={insightSourceHash(box.id)}>
                <rect x={x} y={30} width={120} height={52} rx={12} fill="rgb(var(--color-fog) / 0.04)" stroke="rgb(var(--color-fog) / 0.2)" />
                <circle cx={x + 14} cy={48} r={5} fill={dotFill} stroke={dotStroke} strokeWidth={2} />
                <text x={x + 26} y={52} fontSize={11} fontWeight={600} fill="rgb(var(--color-fog))">{box.label}</text>
                <text x={x + 14} y={71} fontSize={10} fill="rgb(var(--color-fog) / 0.55)">
                  {box.state === 'missing' ? t('insights.sources.missing') : box.src?.age_sec !== undefined ? t('insights.sources.age', { value: formatAge(box.src.age_sec) }) : t('insights.sources.ok')}
                </text>
                <line x1={x + 120} y1={56} x2={i === ORDER.length - 1 ? 690 : x + 135} y2={56} stroke="rgb(var(--color-fog) / 0.35)" strokeWidth={1.2} markerEnd={i === ORDER.length - 1 ? 'url(#insight-arrow)' : undefined} />
              </a>
            )
          })}
          <rect x={690} y={30} width={190} height={52} rx={14} fill="rgba(56,189,248,0.14)" stroke="#38bdf8" />
          <text x={785} y={52} textAnchor="middle" fontSize={12} fontWeight={700} fill="rgb(var(--color-fog))">{t('insights.sources.center')}</text>
          <text x={785} y={70} textAnchor="middle" fontSize={10} fill="rgb(var(--color-fog) / 0.6)">{t('insights.sources.centerHint')}</text>
        </svg>
      </div>
      <ul className="mt-3 grid gap-2 md:hidden">
        {boxes.map((box) => (
          <li key={box.id} className="flex items-center justify-between rounded-2xl border border-white/10 bg-white/5 px-3 py-2 text-xs">
            <a href={insightSourceHash(box.id)} className="flex items-center gap-2 text-fog">
              <span className={`inline-block h-2.5 w-2.5 rounded-full border-2 ${box.state === 'ok' ? 'border-emerald-400 bg-emerald-400' : box.state === 'stale' ? 'border-amber-400 bg-amber-400' : 'border-fog/50'}`} />
              {box.label}
            </a>
            <span className="text-fog/60">{box.state === 'missing' ? t('insights.sources.missing') : box.src?.age_sec !== undefined ? t('insights.sources.age', { value: formatAge(box.src.age_sec) }) : t('insights.sources.ok')}</span>
          </li>
        ))}
      </ul>
      {teach && <p className="mt-3 text-xs text-fog/60">{t('insights.sources.hint')}</p>}
    </section>
  )
}
