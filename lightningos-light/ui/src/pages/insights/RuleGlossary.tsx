import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'

const RULES = ['R1', 'R2', 'R3', 'R4', 'R5', 'R6', 'R7', 'R8']

type Props = { open: boolean; focusRule?: string; onClose: () => void }

// Side drawer that explains each rule in plain words with the same three
// boxes every time: condition, action, when it goes away.
export default function RuleGlossary({ open, focusRule, onClose }: Props) {
  const { t } = useTranslation()

  useEffect(() => {
    if (!open) return
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])

  useEffect(() => {
    if (!open || !focusRule) return
    const el = document.getElementById(`insight-rule-${focusRule}`)
    el?.scrollIntoView({ block: 'start', behavior: 'smooth' })
  }, [open, focusRule])

  if (!open) return null
  return (
    <div className="fixed inset-0 z-[70] flex justify-end">
      <button type="button" aria-label={t('common.close')} className="flex-1 bg-black/60 backdrop-blur-sm" onClick={onClose} />
      <aside className="h-full w-full max-w-md overflow-y-auto border-l border-white/10 bg-slate/95 p-5 shadow-panel">
        <div className="flex items-center justify-between gap-3">
          <h3 className="text-lg font-semibold text-fog">{t('insights.glossary.title')}</h3>
          <button type="button" className="btn-secondary px-3 py-1 text-xs" onClick={onClose}>{t('common.close')}</button>
        </div>
        <p className="mt-2 text-sm text-fog/70">{t('insights.glossary.intro')}</p>
        <div className="mt-4 space-y-4">
          {RULES.map((rule) => (
            <section key={rule} id={`insight-rule-${rule}`} className={`rounded-2xl border p-3 ${focusRule === rule ? 'border-sky-400/40 bg-sky-500/10' : 'border-white/10 bg-white/5'}`}>
              <h4 className="text-sm font-semibold text-fog">{rule} · {t(`insights.glossary.rules.${rule}.name`)}</h4>
              <div className="mt-2 grid gap-2 text-[11px] sm:grid-cols-3">
                {(['condition', 'action', 'gone'] as const).map((part) => (
                  <div key={part} className="rounded-xl border border-white/10 bg-ink/40 p-2">
                    <p className="text-[10px] uppercase tracking-wide text-fog/50">{t(`insights.glossary.${part}`)}</p>
                    <p className="mt-1 text-fog/80">{t(`insights.glossary.rules.${rule}.${part}`)}</p>
                  </div>
                ))}
              </div>
              <p className="mt-2 text-[11px] text-fog/60"><span className="uppercase tracking-wide text-fog/45">{t('insights.glossary.example')}:</span> {t(`insights.glossary.rules.${rule}.example`)}</p>
            </section>
          ))}
        </div>
        <section className="mt-5 space-y-3 text-sm">
          <div>
            <h4 className="font-semibold text-fog">{t('insights.glossary.valueTitle')}</h4>
            <p className="mt-1 text-fog/70">{t('insights.glossary.valueBody')}</p>
          </div>
          <div>
            <h4 className="font-semibold text-fog">{t('insights.glossary.scaleTitle')}</h4>
            <p className="mt-1 text-fog/70">{t('insights.glossary.scaleBody')}</p>
          </div>
          <div>
            <h4 className="font-semibold text-fog">{t('insights.glossary.confidenceTitle')}</h4>
            <p className="mt-1 text-fog/70">{t('insights.glossary.confidenceBody')}</p>
          </div>
        </section>
      </aside>
    </div>
  )
}
