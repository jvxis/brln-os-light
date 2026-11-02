import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getOnchainSpendingGuard, updateOnchainSpendingGuard, type OnchainSpendingGuardStatus } from '../api'
import StatusBadge from './dashboard/StatusBadge'

export default function OnchainSpendingGuard() {
  const { t, i18n } = useTranslation()
  const [status, setStatus] = useState<OnchainSpendingGuardStatus | null>(null)
  const [expanded, setExpanded] = useState(false)
  const [enabled, setEnabled] = useState(false)
  const [perSend, setPerSend] = useState('')
  const [rolling, setRolling] = useState('')
  const [dirty, setDirty] = useState(false)
  const dirtyRef = useRef(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const mounted = useRef(false)
  const savingRef = useRef(false)
  const generation = useRef(0)
  const update = useCallback((data: OnchainSpendingGuardStatus, replaceDraft = false) => {
    if (!mounted.current) return
    setStatus(data)
    if (replaceDraft || !dirtyRef.current) {
      setEnabled(data.enabled)
      setPerSend(data.max_send_sat > 0 ? String(data.max_send_sat) : '')
      setRolling(data.rolling_24h_limit_sat > 0 ? String(data.rolling_24h_limit_sat) : '')
      dirtyRef.current = false
      setDirty(false)
    }
  }, [])
  useEffect(() => {
    mounted.current = true
    let inFlight = false
    const load = async () => {
      if (inFlight || savingRef.current) return
      inFlight = true
      const version = generation.current
      try {
        const data = await getOnchainSpendingGuard()
        if (mounted.current && version === generation.current) { update(data); setError('') }
      } catch {
        if (mounted.current && version === generation.current) setError(t('onchainGuard.unavailable'))
      } finally { inFlight = false }
    }
    void load()
    const timer = setInterval(() => void load(), 30000)
    return () => { mounted.current = false; generation.current++; clearInterval(timer) }
  }, [t, update])
  const edit = () => { dirtyRef.current = true; setDirty(true); setNotice('') }
  const save = async () => {
    const max = perSend.trim() ? Number(perSend) : 0
    const total = rolling.trim() ? Number(rolling) : 0
    if (![max, total].every(value => Number.isSafeInteger(value) && value >= 0 && value <= 2100000000000000) || (enabled && max === 0 && total === 0)) {
      setNotice(t('onchainGuard.invalid')); return
    }
    setSaving(true); setNotice('')
    savingRef.current = true
    generation.current++
    // Prevent a background response overwriting the draft while the exact
    // policy relaxation is being confirmed by the financial-intent dialog.
    dirtyRef.current = true
    try {
      const data = await updateOnchainSpendingGuard({ enabled, max_send_sat: max, rolling_24h_limit_sat: total })
      if (mounted.current) { update(data, true); setError(''); setNotice(t('onchainGuard.saved')) }
    } catch (err: any) {
      if (mounted.current) setNotice(err?.message || t('onchainGuard.saveFailed'))
    } finally { savingRef.current = false; if (mounted.current) setSaving(false) }
  }
  const sats = (value: number) => value.toLocaleString(i18n.language)
  const consumed = (status?.used_sat || 0) + (status?.reserved_sat || 0)
  const limit = status?.rolling_24h_limit_sat || 0
  return (
    <div className="section-card overflow-hidden p-0">
      <button type="button" className="flex w-full flex-col gap-3 px-5 py-4 text-left sm:flex-row sm:items-center sm:justify-between" onClick={() => setExpanded(value => !value)} aria-expanded={expanded}>
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <h3 className="text-base font-semibold">{t('onchainGuard.title')}</h3>
          <StatusBadge tone={error || !status ? 'warn' : status.enabled ? 'ok' : 'muted'} label={error || !status ? t('onchainGuard.unverified') : status.enabled ? t('wallet.spendingGuardActive') : t('wallet.spendingGuardDisabled')} />
          <span className="text-xs text-fog/60">{status?.enabled ? t('onchainGuard.summary', { perSend: status.max_send_sat > 0 ? sats(status.max_send_sat) : t('wallet.spendingGuardUnlimited'), used: sats(consumed), limit: limit > 0 ? sats(limit) : t('wallet.spendingGuardUnlimited') }) : t('onchainGuard.feesExcluded')}</span>
        </div>
        <span className="shrink-0 text-xs text-fog/60">{expanded ? t('wallet.spendingGuardHideSettings') : t('wallet.spendingGuardShowSettings')}</span>
      </button>
      {expanded && <div className="space-y-4 border-t border-white/10 px-5 py-5">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <p className="max-w-3xl text-sm text-fog/60">{t('onchainGuard.description')}</p>
          <label className="flex items-center gap-2 rounded-xl border border-white/10 bg-ink/50 px-3 py-2 text-sm">
            <input type="checkbox" checked={enabled} disabled={!status || saving || !!error} onChange={event => { setEnabled(event.target.checked); edit() }} />{t('wallet.spendingGuardEnable')}
          </label>
        </div>
        {status?.enabled && limit > 0 && <div className="rounded-2xl border border-white/10 bg-ink/50 p-4">
          <div className="flex flex-wrap justify-between gap-2 text-sm"><span>{t('onchainGuard.usage')}</span><span className="font-mono">{sats(consumed)} / {sats(limit)} sats</span></div>
          <div className="mt-3 h-2 overflow-hidden rounded-full bg-white/10"><div className="h-full rounded-full bg-glow" style={{ width: `${Math.min(100, consumed / limit * 100)}%` }} /></div>
          <div className="mt-2 flex flex-wrap justify-between gap-2 text-xs text-fog/55">
            <span>{t('onchainGuard.broadcast', { amount: sats(status.used_sat) })}</span><span>{t('wallet.spendingGuardReserved', { amount: sats(status.reserved_sat) })}</span><span>{t('wallet.spendingGuardRemaining', { amount: sats(status.remaining_sat) })}</span>
          </div>
        </div>}
        <div className="grid gap-4 md:grid-cols-2">
          <label className="space-y-2 text-sm"><span>{t('onchainGuard.perSend')}</span><input className="input-field" type="number" min={0} step={1} value={perSend} disabled={!status || saving || !!error} placeholder={t('wallet.spendingGuardUnlimited')} onChange={event => { setPerSend(event.target.value); edit() }} /></label>
          <label className="space-y-2 text-sm"><span>{t('onchainGuard.rolling')}</span><input className="input-field" type="number" min={0} step={1} value={rolling} disabled={!status || saving || !!error} placeholder={t('wallet.spendingGuardUnlimited')} onChange={event => { setRolling(event.target.value); edit() }} /></label>
        </div>
        <div className="rounded-2xl border border-brass/25 bg-brass/5 p-4 text-xs leading-relaxed text-fog/65">
          <p>{t('onchainGuard.scope')}</p><p className="mt-2">{t('onchainGuard.sweep')}</p><p className="mt-2 text-brass">{t('onchainGuard.exclusions')}</p>
        </div>
        <div className="flex flex-wrap items-center gap-3"><button type="button" className="btn-primary" disabled={saving || !dirty || !status || !!error} onClick={() => void save()}>{saving ? t('common.saving') : t('onchainGuard.save')}</button>{notice && <p role="status" className="text-sm text-brass">{notice}</p>}{error && <p role="alert" className="text-sm text-ember">{error}</p>}</div>
      </div>}
    </div>
  )
}
