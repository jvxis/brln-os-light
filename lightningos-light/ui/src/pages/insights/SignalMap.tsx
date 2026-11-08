import { Fragment } from 'react'
import { useTranslation } from 'react-i18next'
import type { InsightSource } from '../../api'
import { insightSourceHash } from './links'

const ORDER = ['autofee', 'interlock', 'rebalance', 'ranking', 'open_candidates']
const STALE_AFTER_SEC = 6 * 3600

type Props = { sources: InsightSource[]; teach: boolean; formatAge: (seconds: number) => string }

// Five boxes feeding one: a picture of where every number on the page
// comes from. Colours never carry the meaning alone; the dot and the word
// say the same thing. Boxes are plain flex items so long labels
// ("Ranking de Canais") and long hints wrap instead of spilling out.
export default function SignalMap({ sources, teach, formatAge }: Props) {
  const { t } = useTranslation()
  const byId = new Map(sources.map((s) => [s.id, s]))
  const boxes = ORDER.map((id) => {
    const src = byId.get(id)
    const ok = Boolean(src?.ok) && (src?.age_sec === undefined || src.age_sec < STALE_AFTER_SEC)
    const state = !src || !src.ok ? 'missing' : ok ? 'ok' : 'stale'
    const detail = state === 'missing' ? t('insights.sources.missing') : src?.age_sec !== undefined ? t('insights.sources.age', { value: formatAge(src.age_sec) }) : t('insights.sources.ok')
    return { id, state, detail, label: t(`insights.sources.${id}` as any, { defaultValue: id }) }
  })
  const dotClass = (state: string) =>
    `inline-block h-2.5 w-2.5 shrink-0 rounded-full border-2 ${state === 'ok' ? 'border-emerald-400 bg-emerald-400' : state === 'stale' ? 'border-amber-400 bg-amber-400' : 'border-fog/50'}`

  return (
    <section className="rounded-3xl border border-white/10 bg-ink/60 p-4 sm:p-5">
      <h3 className="text-xs uppercase tracking-[0.2em] text-fog/55">{t('insights.sources.title')}</h3>
      <div className="mt-3 hidden items-stretch md:flex" role="img" aria-label={t('insights.sources.title')}>
        {boxes.map((box) => (
          <Fragment key={box.id}>
            <a
              href={insightSourceHash(box.id)}
              className="flex min-w-0 flex-1 flex-col justify-center rounded-2xl border border-white/15 bg-white/5 px-3 py-2 transition hover:border-white/30"
            >
              <span className="flex items-start gap-2 text-xs font-semibold leading-tight text-fog">
                <span className={`${dotClass(box.state)} mt-0.5`} />
                <span className="min-w-0 break-words">{box.label}</span>
              </span>
              <span className="mt-1 pl-[18px] text-[11px] leading-tight text-fog/55">{box.detail}</span>
            </a>
            <span aria-hidden className="h-px w-3 shrink-0 self-center bg-fog/35" />
          </Fragment>
        ))}
        <svg aria-hidden viewBox="0 0 10 10" className="h-2.5 w-2.5 shrink-0 self-center fill-fog/50">
          <path d="M0 0L10 5L0 10z" />
        </svg>
        <div className="ml-2 flex min-w-0 flex-[1.4] flex-col items-center justify-center rounded-2xl border border-sky-400 bg-sky-400/15 px-3 py-2 text-center">
          <span className="text-sm font-bold text-fog">{t('insights.sources.center')}</span>
          <span className="mt-1 text-[11px] leading-tight text-fog/60">{t('insights.sources.centerHint')}</span>
        </div>
      </div>
      <ul className="mt-3 grid gap-2 md:hidden">
        {boxes.map((box) => (
          <li key={box.id} className="flex items-center justify-between rounded-2xl border border-white/10 bg-white/5 px-3 py-2 text-xs">
            <a href={insightSourceHash(box.id)} className="flex items-center gap-2 text-fog">
              <span className={dotClass(box.state)} />
              {box.label}
            </a>
            <span className="text-fog/60">{box.detail}</span>
          </li>
        ))}
      </ul>
      {teach && <p className="mt-3 text-xs text-fog/60">{t('insights.sources.hint')}</p>}
    </section>
  )
}
