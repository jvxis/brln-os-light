import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getApps, getMempoolFees, getOPReturnRecords, getOPReturnStatus, previewOPReturn, publishOPReturn, reauthAuth, type OPReturnPreview, type OPReturnRecord } from '../api'
import SensitiveActionModal from '../components/SensitiveActionModal'

export default function OPReturn() {
  const { i18n } = useTranslation()
  const pt = i18n.language.startsWith('pt')
  const label = (en: string, br: string) => pt ? br : en
  const [text, setText] = useState('')
  const [rate, setRate] = useState(1)
  const [mode, setMode] = useState('manual')
  const [fees, setFees] = useState<Record<string, number>>({})
  const [preview, setPreview] = useState<OPReturnPreview | null>(null)
  const [records, setRecords] = useState<OPReturnRecord[]>([])
  const [ready, setReady] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [confirm, setConfirm] = useState(false)
  const [password, setPassword] = useState('')
  const [modal, setModal] = useState(false)
  const [explorer, setExplorer] = useState('')
  const [now, setNow] = useState(Date.now())
  const inFlight = useRef(false)
  const pendingKey = useRef('')
  const bytes = new TextEncoder().encode(text)
  const hex = Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('')
  const valid = bytes.length > 0 && bytes.length <= 80 && !/[\p{C}\p{Zl}\p{Zp}]/u.test(text) && Number.isInteger(rate) && rate >= 1 && rate <= 1000
  const current = preview && preview.text === text && preview.sat_per_vbyte === rate && Date.parse(preview.expires_at) > now
  const unresolved = records.some(r => r.state === 'unknown' || r.state === 'preparing')
  const warning = label('Confirmed content is public, permanent and cannot be edited or deleted. This transaction spends on-chain funds. The message reaches the blockchain only after a miner confirms it.', 'O conteúdo confirmado é público, permanente e não pode ser editado ou apagado. Esta transação gasta fundos on-chain. A mensagem só entra na blockchain após a confirmação por um minerador.')
  const refresh = useCallback(async () => {
    const [status, history] = await Promise.all([getOPReturnStatus(), getOPReturnRecords()])
    setReady(status.ready); setRecords(history)
  }, [])
  useEffect(() => {
    void refresh().catch(e => { setReady(false); setError(String(e.message || e)) })
    const timer = window.setInterval(() => { void refresh().catch(() => setReady(false)) }, 15000)
    const tick = window.setInterval(() => setNow(Date.now()), 1000)
    void getMempoolFees().then(f => setFees({ economy: Number(f.economyFee || f.hourFee), normal: Number(f.halfHourFee || f.hourFee), fast: Number(f.fastestFee) })).catch(() => {})
    void getApps().then(apps => {
      const app = apps.find((a: { id: string; installed: boolean; status: string }) => a.id === 'mempool' && a.installed && a.status === 'running')
      if (app?.port) {
        const url = new URL(window.location.href); url.protocol = `${app.scheme || 'http'}:`; url.port = String(app.port); url.pathname = '/'; url.search = ''; url.hash = ''
        setExplorer(url.toString())
      }
    }).catch(() => {})
    return () => { clearInterval(timer); clearInterval(tick) }
  }, [refresh])
  function invalidate() { setPreview(null); setConfirm(false); pendingKey.current = ''; setNotice('') }
  async function doPreview() {
    if (inFlight.current || !valid) return
    inFlight.current = true; setBusy(true); setError(''); invalidate()
    try { setPreview(await previewOPReturn(text, rate)); pendingKey.current = crypto.randomUUID() }
    catch (e: any) { setError(e.message) }
    finally { inFlight.current = false; setBusy(false) }
  }
  async function publish() {
    if (inFlight.current || !preview || !current || !confirm || !password) return
    inFlight.current = true; setBusy(true); setError('')
    try {
      await reauthAuth({ password, scope: 'opreturn_publish' }); setPassword('')
      const record = await publishOPReturn(preview.preview_id, pendingKey.current)
      setRecords(old => [record, ...old.filter(r => r.id !== record.id)])
      setModal(false); setPreview(null); setConfirm(false)
      setNotice(label('Publication recorded. Check its state below; broadcast does not mean confirmed.', 'Publicação registrada. Confira o estado abaixo; transmissão não significa confirmação.'))
      void refresh().catch(() => {})
    } catch (e: any) {
      setError(e.message); setPassword('')
      // Keep the same idempotency key when the HTTP outcome is uncertain.
      void refresh().catch(() => {})
    } finally { inFlight.current = false; setBusy(false) }
  }
  const stateLabel = (state: string) => ({ preparing: label('Preparing', 'Preparando'), unknown: label('Unknown — reconciling TXID; do not resubmit', 'Incerto — verificando TXID; não republique'), broadcast: label('Broadcast — awaiting confirmation', 'Transmitida — aguardando confirmação'), confirmed: label('Confirmed', 'Confirmada'), failed: label('Failed before broadcast', 'Falhou antes da transmissão') }[state] || state)
  return <div className="space-y-6">
    <div><h1 className="text-2xl font-semibold">Bitcoin OP_RETURN</h1><p className="mt-2 text-fog/70">{label('Publish a short text message with your LND wallet.', 'Publique uma mensagem curta usando sua carteira LND.')}</p></div>
    <p className="section-card border border-amber-400/40 text-amber-200">{warning}</p>
    {!ready && <p role="status" className="text-amber-200">{label('Requires the running app, login, an unlocked synchronized mainnet wallet and confirmed spendable funds.', 'Requer o app em execução, login, carteira mainnet desbloqueada e sincronizada, com fundos confirmados disponíveis.')}</p>}
    {error && <p role="alert" className="text-rose-300">{error}</p>}
    {notice && <p role="status" className="text-emerald-300">{notice}</p>}
    <section className="section-card space-y-4">
      <label className="block">{label('UTF-8 message', 'Mensagem UTF-8')}<textarea className="input-field mt-2 w-full" rows={3} disabled={busy} value={text} onChange={e => { setText(e.target.value); invalidate() }} /></label>
      <p aria-live="polite" className={bytes.length > 80 ? 'text-rose-300' : 'text-fog/70'}>{bytes.length}/80 bytes</p>
      <p className="text-sm text-fog/60">{label('Printable text only; no line breaks or control characters.', 'Somente texto imprimível; sem quebras de linha ou caracteres de controle.')}</p>
      <label className="block">{label('Fee priority', 'Prioridade da taxa')}<select className="input-field mt-2" disabled={busy} value={mode} onChange={e => { setMode(e.target.value); if (e.target.value !== 'manual') setRate(Math.min(1000, Math.max(1, Math.ceil(fees[e.target.value])))); invalidate() }}>
        <option value="economy" disabled={!fees.economy}>{label('Economy', 'Econômica')}</option><option value="normal" disabled={!fees.normal}>Normal</option><option value="fast" disabled={!fees.fast}>{label('Fast', 'Rápida')}</option><option value="manual">Manual</option>
      </select></label>
      <label className="block">sat/vB (1–1000)<input type="number" className="input-field mt-2" min={1} max={1000} step={1} disabled={busy || mode !== 'manual'} value={rate} onChange={e => { setRate(Number(e.target.value)); invalidate() }} /></label>
      <details open><summary>{label('Exact text and hexadecimal payload', 'Texto exato e payload hexadecimal')}</summary><pre className="mt-3 whitespace-pre-wrap break-all rounded-xl bg-black/20 p-3">{text}</pre><code className="block break-all p-3 text-sm">{hex || '—'}</code></details>
      <button className="btn-secondary" disabled={!ready || !valid || busy || unresolved} onClick={() => void doPreview()}>{label('Preview transaction', 'Prévia da transação')}</button>
    </section>
    {preview && <section className="section-card space-y-4">
      <h2 className="text-xl">{label('Transaction preview', 'Prévia da transação')}</h2>
      <dl className="grid grid-cols-2 gap-3 text-sm">
        <dt>{label('Fee / maximum approved', 'Taxa / máximo aprovado')}</dt><dd>{preview.fee_sat} / {preview.max_fee_sat} sats</dd>
        <dt>{label('Total wallet debit', 'Débito total da carteira')}</dt><dd>{preview.total_debit_sat} sats</dd>
        <dt>{label('Selected inputs / amount', 'Entradas selecionadas / valor')}</dt><dd>{preview.selected_input_count} / {preview.selected_input_sat} sats</dd>
        <dt>{label('Estimated size', 'Tamanho estimado')}</dt><dd>{preview.estimated_vbytes} vB · {preview.sat_per_vbyte} sat/vB</dd>
        <dt>{label('Expires', 'Expira')}</dt><dd>{new Date(preview.expires_at).toLocaleTimeString()}</dd>
      </dl>
      {!current && <p className="text-amber-200">{label('Preview expired. Create a new preview.', 'Prévia expirada. Gere uma nova prévia.')}</p>}
      <label className="flex gap-3"><input type="checkbox" checked={confirm} disabled={busy} onChange={e => setConfirm(e.target.checked)} /><span>{label('I approve this fee and understand that confirmed content is public and permanent.', 'Aprovo esta taxa e entendo que o conteúdo confirmado é público e permanente.')}</span></label>
      <button className="btn-primary" disabled={!current || !confirm || busy || unresolved} onClick={() => { setError(''); setModal(true) }}>{label('Confirm password and publish', 'Confirmar senha e publicar')}</button>
    </section>}
    <section className="section-card space-y-4"><div className="flex items-center justify-between"><h2 className="text-xl">{label('Publication history', 'Histórico de publicações')}</h2><button className="btn-secondary" disabled={busy} onClick={() => void refresh().catch(e => setError(e.message))}>{label('Refresh', 'Atualizar')}</button></div>
      {records.length === 0 && <p className="text-fog/60">{label('No publications yet.', 'Nenhuma publicação ainda.')}</p>}
      {records.map(record => <article key={record.id} className="space-y-2 rounded-xl border border-white/10 p-4">
        <p>{stateLabel(record.state)}</p><pre className="whitespace-pre-wrap break-all">{record.quote.text}</pre>
        <p className="text-sm text-fog/70">{record.quote.fee_sat} sats · {record.confirmations} {label('confirmations', 'confirmações')} · {label('Block', 'Bloco')} {record.block_height || '—'} · {new Date(record.created_at).toLocaleString()}</p>
        {record.txid && <div className="space-y-2"><code className="block break-all text-xs">{record.txid}</code><button className="btn-secondary" onClick={() => void navigator.clipboard.writeText(record.txid).then(() => setNotice(label('TXID copied.', 'TXID copiado.'))).catch(() => setError(label('Could not copy TXID.', 'Não foi possível copiar o TXID.')))}>{label('Copy TXID', 'Copiar TXID')}</button>{explorer && <a className="ml-3 underline" href={`${explorer}tx/${encodeURIComponent(record.txid)}`} target="_blank" rel="noreferrer">Mempool local</a>}</div>}
      </article>)}
      <p className="text-xs text-fog/60">{label('Uninstalling preserves local history. Explorer links are shown only for your running local Mempool.', 'A desinstalação preserva o histórico local. Links de consulta aparecem apenas para seu Mempool local em execução.')}</p>
    </section>
    <SensitiveActionModal open={modal} title={label('Publish irreversible public data', 'Publicar dados públicos irreversíveis')} description={warning} password={password} busy={busy} error={error} confirmLabel={label('Publish transaction', 'Publicar transação')} onPasswordChange={setPassword} onConfirm={publish} onClose={() => { setModal(false); setPassword('') }} />
  </div>
}
