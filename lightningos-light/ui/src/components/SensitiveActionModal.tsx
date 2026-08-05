import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'

type SensitiveActionModalProps = {
  open: boolean
  title: string
  description: string
  password: string
  busy?: boolean
  error?: string
  confirmLabel: string
  onPasswordChange: (value: string) => void
  onConfirm: () => void | Promise<void>
  onClose: () => void
}

export default function SensitiveActionModal({
  open,
  title,
  description,
  password,
  busy = false,
  error = '',
  confirmLabel,
  onPasswordChange,
  onConfirm,
  onClose,
}: SensitiveActionModalProps) {
  const { t } = useTranslation()

  useEffect(() => {
    if (!open) return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !busy) onClose()
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [open, busy, onClose])

  if (!open) return null

  return (
    <div className="fixed inset-0 z-[70] flex items-center justify-center px-4">
      <button
        type="button"
        className="absolute inset-0 bg-black/70 backdrop-blur-sm"
        aria-label={t('common.close')}
        onClick={() => { if (!busy) onClose() }}
      />
      <div role="dialog" aria-modal="true" className="relative z-10 w-full max-w-md rounded-3xl border border-white/10 bg-slate/95 p-6 shadow-panel">
        <h3 className="text-xl font-semibold">{title}</h3>
        <p className="mt-2 text-sm text-fog/65">{description}</p>
        <div className="mt-5 space-y-2">
          <label className="text-xs uppercase tracking-[0.2em] text-fog/55">{t('nodeRetirement.reauthPasswordLabel')}</label>
          <input
            autoFocus
            className="input-field"
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(event) => onPasswordChange(event.target.value)}
            placeholder={t('nodeRetirement.reauthPasswordPlaceholder')}
            onKeyDown={(event) => {
              if (event.key === 'Enter' && !busy) void onConfirm()
            }}
          />
        </div>
        {error && <p className="mt-3 text-sm text-rose-300">{error}</p>}
        <div className="mt-6 flex justify-end gap-3">
          <button className="btn-secondary" type="button" disabled={busy} onClick={onClose}>{t('common.cancel')}</button>
          <button className="btn-primary" type="button" disabled={busy} onClick={() => void onConfirm()}>
            {busy ? t('common.saving') : confirmLabel}
          </button>
        </div>
      </div>
    </div>
  )
}
