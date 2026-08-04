import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  applyAppearance,
  isDarkOnlyTheme,
  paletteOrder,
  visualThemeOrder,
  type Appearance,
  type PaletteKey,
  type ThemeMode,
  type VisualThemeKey
} from '../theme'

type AppearanceMenuProps = {
  open: boolean
  visualTheme: VisualThemeKey
  palette: PaletteKey
  mode: ThemeMode
  onApply: (value: Appearance) => void
  onClose: () => void
}

export default function AppearanceMenu({
  open,
  visualTheme,
  palette,
  mode,
  onApply,
  onClose
}: AppearanceMenuProps) {
  const { t } = useTranslation()
  const panelRef = useRef<HTMLElement>(null)
  const closeButtonRef = useRef<HTMLButtonElement>(null)
  const previousFocusRef = useRef<HTMLElement | null>(null)
  const [draft, setDraft] = useState<Appearance>({ visualTheme, palette, mode })
  const darkOnly = isDarkOnlyTheme(draft.visualTheme)

  const handleCancel = useCallback(() => {
    applyAppearance({ visualTheme, palette, mode })
    onClose()
  }, [mode, onClose, palette, visualTheme])

  useEffect(() => {
    if (!open) return
    setDraft({ visualTheme, palette, mode })
    applyAppearance({ visualTheme, palette, mode })
  }, [mode, open, palette, visualTheme])

  useEffect(() => {
    if (open) applyAppearance(draft)
  }, [draft, open])

  useEffect(() => {
    if (!open) return
    const previousOverflow = document.body.style.overflow
    previousFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
    document.body.style.overflow = 'hidden'
    closeButtonRef.current?.focus()
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        handleCancel()
        return
      }
      if (event.key !== 'Tab' || !panelRef.current) return
      const focusable = Array.from(panelRef.current.querySelectorAll<HTMLElement>('button:not(:disabled)'))
      if (!focusable.length) return
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault()
        last.focus()
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault()
        first.focus()
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => {
      document.body.style.overflow = previousOverflow
      window.removeEventListener('keydown', handleKeyDown)
      previousFocusRef.current?.focus()
    }
  }, [handleCancel, open])

  if (!open) return null

  return (
    <div className="appearance-dialog" role="presentation" onMouseDown={handleCancel}>
      <section
        ref={panelRef}
        className="appearance-dialog__panel"
        role="dialog"
        aria-modal="true"
        aria-labelledby="appearance-dialog-title"
        onMouseDown={(event) => event.stopPropagation()}
      >
        <div className="appearance-dialog__header">
          <div>
            <p className="appearance-dialog__eyebrow">{t('appearance.eyebrow')}</p>
            <h2 id="appearance-dialog-title">{t('appearance.title')}</h2>
            <p>{t('appearance.description')}</p>
          </div>
          <button
            ref={closeButtonRef}
            type="button"
            className="appearance-dialog__close"
            onClick={handleCancel}
            aria-label={t('common.close')}
          >
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" aria-hidden="true">
              <path d="M6 6l12 12M18 6l-12 12" />
            </svg>
          </button>
        </div>

        <div className="appearance-dialog__content">
          <fieldset className="appearance-section">
            <legend>{t('appearance.theme')}</legend>
            <p className="appearance-section__hint">{t('appearance.themeHint')}</p>
            <div className="appearance-theme-grid">
              {visualThemeOrder.map((item) => {
                const selected = item === draft.visualTheme
                return (
                  <button
                    key={item}
                    type="button"
                    className={`appearance-theme-card appearance-theme-card--${item}${selected ? ' is-selected' : ''}`}
                    onClick={() => setDraft((current) => ({
                      ...current,
                      visualTheme: item,
                      mode: isDarkOnlyTheme(item) ? 'dark' : current.mode
                    }))}
                    aria-pressed={selected}
                  >
                    <span className="appearance-theme-card__preview" aria-hidden="true">
                      <span />
                      <span />
                      <span />
                    </span>
                    <span className="appearance-theme-card__copy">
                      <strong>{t(`appearance.themes.${item}.name`)}</strong>
                      <small>{t(`appearance.themes.${item}.description`)}</small>
                    </span>
                    {isDarkOnlyTheme(item) && (
                      <span className="appearance-theme-card__badge">{t('appearance.darkOnly')}</span>
                    )}
                  </button>
                )
              })}
            </div>
          </fieldset>

          <fieldset className="appearance-section">
            <legend>{t('appearance.palette')}</legend>
            <p className="appearance-section__hint">{t('appearance.paletteHint')}</p>
            <div className="appearance-palette-grid">
              {paletteOrder.map((item) => {
                const selected = item === draft.palette
                return (
                  <button
                    key={item}
                    type="button"
                    data-palette={item}
                    className={`appearance-palette${selected ? ' is-selected' : ''}`}
                    onClick={() => setDraft((current) => ({ ...current, palette: item }))}
                    aria-pressed={selected}
                    title={t(`topbar.paletteNames.${item}`)}
                  >
                    <span className="appearance-palette__swatch" aria-hidden="true" />
                    <span>{t(`topbar.paletteNames.${item}`)}</span>
                  </button>
                )
              })}
            </div>
          </fieldset>

          <fieldset className="appearance-section appearance-section--mode">
            <legend>{t('appearance.mode')}</legend>
            <p className="appearance-section__hint">
              {darkOnly ? t('appearance.darkOnlyHint') : t('appearance.modeHint')}
            </p>
            <div className="appearance-mode-control">
              {(['dark', 'light'] as ThemeMode[]).map((item) => (
                <button
                  key={item}
                  type="button"
                  className={draft.mode === item ? 'is-selected' : ''}
                  onClick={() => setDraft((current) => ({ ...current, mode: item }))}
                  disabled={darkOnly && item === 'light'}
                  aria-pressed={draft.mode === item}
                >
                  <span aria-hidden="true">{item === 'dark' ? '◐' : '☀'}</span>
                  {t(`appearance.modes.${item}`)}
                </button>
              ))}
            </div>
          </fieldset>
        </div>
        <footer className="appearance-dialog__footer">
          <p>{t('appearance.previewNotice')}</p>
          <div>
            <button type="button" className="btn-secondary" onClick={handleCancel}>
              {t('common.cancel')}
            </button>
            <button
              type="button"
              className="btn-primary"
              onClick={() => {
                onApply(draft)
                onClose()
              }}
            >
              {t('appearance.apply')}
            </button>
          </div>
        </footer>
      </section>
    </div>
  )
}
