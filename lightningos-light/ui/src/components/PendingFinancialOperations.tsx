import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { cancelFinancialIntent, getFinancialIntent, reconcileFinancialIntent } from '../api'
import { dismissFinancialReference, subscribeFinancialReferences, type FinancialReference } from '../financialIntents'

export default function PendingFinancialOperations({ confirming }: { confirming: boolean }) {
  const { i18n } = useTranslation()
  const pt = i18n.language.startsWith('pt')
  const [references, setReferences] = useState<FinancialReference[]>([])
  const [open, setOpen] = useState(false)
  const [states, setStates] = useState<Record<string, any>>({})
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => subscribeFinancialReferences(setReferences), [])
  const refresh = async () => {
    setBusy(true)
    setError('')
    try {
      const entries = await Promise.all(references.map(async item => {
        try { return [item.id, await getFinancialIntent(item.id)] as const }
        catch (failure: any) { return [item.id, { lookup_error: failure?.message || 'Unable to read operation status.' }] as const }
      }))
      setStates(Object.fromEntries(entries))
    } catch (failure: any) { setError(failure?.message || 'Unable to read operation status.') }
    finally { setBusy(false) }
  }
  const check = async (id: string) => {
    setBusy(true)
    setError('')
    try {
      await reconcileFinancialIntent(id)
      const state = await getFinancialIntent(id)
      setStates(current => ({ ...current, [id]: state }))
    } catch (failure: any) { setError(failure?.message || 'Outcome remains uncertain. No new operation was submitted.') }
    finally { setBusy(false) }
  }
  const cancel = async (id: string) => {
    setBusy(true)
    setError('')
    try { await cancelFinancialIntent(id); dismissFinancialReference(id) }
    catch (failure: any) { setError(failure?.message || 'Unable to cancel confirmation.') }
    finally { setBusy(false) }
  }
  if (!references.length || confirming) return null
  return <>
    <button type="button" className="fixed bottom-5 right-5 z-40 btn-secondary shadow-panel" onClick={() => { setOpen(true); void refresh() }}>
      {pt ? 'Consultar operações' : 'Review operations'} ({references.length})
    </button>
    {open && <div className="fixed inset-0 z-[75] flex items-center justify-center bg-black/70 p-4">
      <section role="dialog" aria-modal="true" aria-label={pt ? 'Status das operações' : 'Operation status'} className="section-card w-full max-w-xl max-h-[90vh] overflow-y-auto">
        <div className="flex items-center justify-between gap-3"><h2 className="text-xl font-semibold">{pt ? 'Status das operações' : 'Operation status'}</h2><button type="button" className="btn-secondary" onClick={() => setOpen(false)}>{pt ? 'Fechar' : 'Close'}</button></div>
        <p className="mt-3 text-sm text-fog/65">{pt ? 'Consultar o resultado não repete o envio. Enquanto o resultado estiver incerto, não crie outra transferência.' : 'Checking the result never repeats a transfer. While the outcome is uncertain, do not create another transfer.'}</p>
        {error && <p role="alert" className="mt-3 text-sm text-brass">{error}</p>}
        <div className="mt-4 space-y-3">{references.map(reference => {
          const state = states[reference.id]
          const terminal = state && ['completed', 'rejected', 'cancelled'].includes(state.state)
          return <article key={reference.id} className="rounded-xl border border-white/10 p-3 space-y-2 text-sm break-words">
            <p>{state?.summary?.operation || state?.operation || (pt ? 'Carregando operação…' : 'Loading operation…')}</p>
            {state?.lookup_error && <p role="alert" className="text-brass">{state.lookup_error}</p>}
            {state?.summary?.destination && <p className="font-mono text-xs">{state.summary.destination}</p>}
            {state?.summary?.amount_sat != null && <p>{state.summary.amount_msat != null && BigInt(state.summary.amount_msat) % 1000n !== 0n ? `${BigInt(state.summary.amount_msat).toLocaleString(i18n.language)} msat` : `${Number(state.summary.amount_sat).toLocaleString(i18n.language)} sats`}</p>}
            <p className="text-fog/65">{state?.state === 'completed' ? (pt ? 'Operação concluída' : 'Operation completed') : state?.state === 'rejected' ? (pt ? 'Operação rejeitada' : 'Operation rejected') : state?.state === 'cancelled' ? (pt ? 'Confirmação cancelada' : 'Confirmation cancelled') : state?.state === 'draft' ? (pt ? 'Confirmação ainda não executada' : 'Confirmation has not been executed') : (pt ? 'Aguardando confirmação do resultado' : 'Awaiting a confirmed result')}</p>
            {state?.result?.financial_intent_needs_review && <p className="text-brass">{pt ? 'O financiamento existente foi localizado. Revise uma nova confirmação para continuar este fluxo; a consulta não repete a transferência.' : 'Existing funding was located. Review a new confirmation to continue this workflow; checking never repeats the transfer.'}</p>}
            {(state?.result?.txid || state?.result?.closing_txid) && <p className="font-mono text-xs">Txid: {state.result.txid || state.result.closing_txid}</p>}
            <div className="flex gap-2 flex-wrap">
              {terminal && <button type="button" className="btn-secondary" disabled={busy} onClick={() => { try { dismissFinancialReference(reference.id) } catch (failure: any) { setError(failure.message) } }}>{pt ? 'Resultado conferido' : 'Result reviewed'}</button>}
              {state?.state === 'draft' && <button type="button" className="btn-secondary" disabled={busy} onClick={() => void cancel(reference.id)}>{pt ? 'Cancelar confirmação' : 'Cancel confirmation'}</button>}
              {state && !state.lookup_error && !terminal && state.state !== 'draft' && <button type="button" className="btn-secondary" disabled={busy} onClick={() => void check(reference.id)}>{pt ? 'Consultar resultado' : 'Check result'}</button>}
            </div>
          </article>
        })}</div>
      </section>
    </div>}
  </>
}
