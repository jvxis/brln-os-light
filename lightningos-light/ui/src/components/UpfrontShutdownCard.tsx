import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getUpfrontShutdown, type UpfrontShutdownMode, type UpfrontShutdownStatus, type UpfrontShutdownUpdate } from '../api'

export default function UpfrontShutdownCard({ disabled, onSave }: { disabled: boolean; onSave: (value: UpfrontShutdownUpdate) => void }) {
  const { t } = useTranslation()
  const [status, setStatus] = useState<UpfrontShutdownStatus | null>(null)
  const [mode, setMode] = useState<UpfrontShutdownMode>('off')
  const [address, setAddress] = useState('')
  const [confirmed, setConfirmed] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const load = async () => {
    if (loading) return
    setLoading(true); setError(''); setStatus(null); setConfirmed(false)
    try {
      const result = await getUpfrontShutdown()
      setStatus(result); setMode(result.current.mode); setAddress(result.current.address)
    } catch (e) { setError(e instanceof Error ? e.message : t('upfront.loadFailed')) }
    finally { setLoading(false) }
  }
  const editable = status?.editable && !disabled && !loading
  const changed = status && (mode !== status.current.mode || (mode === 'external' ? address.trim() : '') !== status.current.address)
  return <details className="rounded-xl border border-white/10 p-4" onToggle={e => { if (e.currentTarget.open && !status && !loading && !error) void load() }}>
    <summary className="cursor-pointer text-sm font-medium">{t('upfront.title')} <span className="text-xs text-fog/50">— {t('upfront.optional')}</span></summary>
    <div className="mt-4 space-y-3">
      <p className="text-sm text-fog/65">{t('upfront.intro')}</p>
      {loading && <p role="status">{t('upfront.loading')}</p>}
      {error && <p role="alert" className="text-sm text-rose-300">{error}</p>}
      {status && <>
        {!status.editable && <p role="status" className="text-sm text-brass">{t(`upfront.${status.reason}`)}</p>}
        <label className="block text-sm" htmlFor="upfront-mode">{t('upfront.destination')}</label>
        <select id="upfront-mode" className="input-field" disabled={!editable} value={mode} onChange={e => { setMode(e.target.value as UpfrontShutdownMode); setConfirmed(false) }}>
          <option value="off">{t('upfront.off')}</option><option value="wallet">{t('upfront.wallet')}</option><option value="external">{t('upfront.external')}</option>
        </select>
        <p className="text-xs text-fog/65">{t(`upfront.${mode}Hint`)}</p>
        {mode === 'external' && <>
          <label className="block text-sm" htmlFor="upfront-address">{t('upfront.address')}</label>
          <input id="upfront-address" className="input-field font-mono" value={address} maxLength={100} disabled={!editable} autoComplete="off" spellCheck={false} placeholder="bc1…" onChange={e => { setAddress(e.target.value); setConfirmed(false) }} />
          <label className="flex items-start gap-2 text-xs text-brass"><input type="checkbox" checked={confirmed} disabled={!editable} onChange={e => setConfirmed(e.target.checked)} />{t('upfront.confirmExternal')}</label>
        </>}
        <p className="text-xs text-fog/60">{t('upfront.limits')}</p>
        <div className="flex flex-wrap gap-3">
          <button type="button" className="btn-primary" disabled={!editable || !changed || (mode === 'external' && (!address.trim() || !confirmed))} onClick={() => {
            if (!editable || !changed || !status || (mode === 'external' && (!address.trim() || !confirmed))) return
            onSave({ mode, address: mode === 'external' ? address.trim() : '', config_revision: status.config_revision, confirm_external: confirmed })
          }}>{t('lndConfig.saveRestart')}</button>
          <button type="button" className="btn-secondary" disabled={disabled || loading} onClick={() => void load()}>{t('upfront.reload')}</button>
        </div>
      </>}
      {error && <button type="button" className="btn-secondary" disabled={disabled || loading} onClick={() => void load()}>{t('upfront.reload')}</button>}
    </div>
  </details>
}
