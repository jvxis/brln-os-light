import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { reauthAuth } from '../api'
import { subscribeFinancialConfirmation, type FinancialConfirmation } from '../financialIntents'
import SensitiveActionModal from './SensitiveActionModal'
import PendingFinancialOperations from './PendingFinancialOperations'

export default function FinancialIntentConfirmation() {
  const { i18n } = useTranslation()
  const pt = i18n.language.startsWith('pt')
  const [task, setTask] = useState<FinancialConfirmation | null>(null)
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [now, setNow] = useState(Date.now())
  useEffect(() => subscribeFinancialConfirmation(value => {
    setTask(value)
    setPassword('')
    setError('')
    setBusy(false)
    setNow(Date.now())
  }), [])
  useEffect(() => {
    if (!task) return
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [task])
  if (!task) return <PendingFinancialOperations confirming={false} />
  const intent = task.intent
  const closing = intent.summary.operation === 'lnops/channel/close'
  const summary = intent.summary
  const expired = now >= Date.parse(intent.expires_at)
  const number = (value: number | string) => (typeof value === 'string' ? BigInt(value) : value).toLocaleString(i18n.language)
  const approve = async () => {
    if (busy || expired || !password) return
    setBusy(true)
    setError('')
    const submitted = password
    setPassword('')
    try {
      await reauthAuth({ password: submitted, scope: intent.scope })
      task.approve()
    } catch (failure: any) {
      setError(failure?.message || (pt ? 'Não foi possível confirmar.' : 'Confirmation failed.'))
    } finally { setBusy(false) }
  }
  const labels: Record<string, string> = pt
    ? { onchain_send: 'Envio on-chain', chat_keysend: 'Mensagem com pagamento', channel_open: 'Abrir canal', channel_open_batch: 'Abrir canais', 'wallet/pay': 'Pagamento Lightning', 'wallet/pay/mpp': 'Pagamento Lightning MPP', 'wallet/pay/validated-route': 'Pagamento por rota validada' }
    : { onchain_send: 'On-chain transfer', chat_keysend: 'Message with payment', channel_open: 'Open channel', channel_open_batch: 'Open channels', 'wallet/pay': 'Lightning payment', 'wallet/pay/mpp': 'Lightning MPP payment', 'wallet/pay/validated-route': 'Validated route payment' }
  return <SensitiveActionModal
    open title={pt ? 'Confirmar operação' : 'Confirm operation'}
    description={pt ? 'Confira os dados. Sua senha autoriza uma execução desta operação.' : 'Review these details. Your password authorizes one execution of this operation.'}
    password={password} busy={busy} confirmDisabled={expired}
    error={expired ? (pt ? 'A confirmação expirou. Cancele e revise a operação novamente.' : 'Confirmation expired. Cancel and review the operation again.') : error}
    confirmLabel={pt ? 'Confirmar esta operação' : 'Confirm this operation'}
    onPasswordChange={setPassword} onConfirm={approve} onClose={() => task.cancel()}
  >
    <dl className="mt-4 space-y-2 text-sm break-words" data-testid="financial-intent-summary">
      <div><dt className="text-fog/55">{pt ? 'Operação' : 'Operation'}</dt><dd>{closing ? (summary.fee_mode === 'existing_commitment' ? (pt ? 'Forçar fechamento de canal' : 'Force-close channel') : (pt ? 'Fechar canal cooperativamente' : 'Cooperatively close channel')) : (labels[summary.operation] || summary.operation)}</dd></div>
      {summary.destination && <div><dt className="text-fog/55">{pt ? 'Destino' : 'Destination'}</dt><dd className="font-mono">{summary.destination}</dd></div>}
      {summary.amount_mode === 'selected_maximum'
        ? <div><dt className="text-fog/55">{pt ? 'Valor' : 'Amount'}</dt><dd>{pt ? 'Máximo disponível nas entradas selecionadas, respeitando a reserva do LND' : 'Maximum available from selected inputs, preserving the LND reserve'}</dd></div>
        : summary.amount_sat != null && <div><dt className="text-fog/55">{pt ? 'Valor' : 'Amount'}</dt><dd>{summary.amount_msat != null && BigInt(summary.amount_msat) % 1000n !== 0n ? `${number(summary.amount_msat)} msat` : `${number(summary.amount_sat)} sats`}</dd></div>}
      {summary.max_fee_sat != null && <div><dt className="text-fog/55">{pt ? 'Limite de taxa' : 'Fee limit'}</dt><dd>{summary.max_fee_msat != null && BigInt(summary.max_fee_msat) % 1000n !== 0n ? `${number(summary.max_fee_msat)} msat` : `${number(summary.max_fee_sat)} sats`}</dd></div>}
      {summary.sat_per_vbyte != null && <div><dt className="text-fog/55">{pt ? 'Taxa por tamanho' : 'Fee rate'}</dt><dd>{number(summary.sat_per_vbyte)} sat/vB</dd></div>}
      {summary.max_fee_rate_sat_vbyte != null && <div><dt className="text-fog/55">{pt ? 'Limite da negociação de fechamento' : 'Closing negotiation ceiling'}</dt><dd>{number(summary.max_fee_rate_sat_vbyte)} sat/vB</dd></div>}
      {summary.fee_mode === 'existing_commitment' && <div><dt className="text-fog/55">{pt ? 'Taxa de fechamento forçado' : 'Force-close fee'}</dt><dd>{pt ? 'Usa a transação de compromisso existente do canal.' : 'Uses the channel’s existing commitment transaction.'}</dd></div>}
      {summary.selected_input_count != null && <div><dt className="text-fog/55">{pt ? 'Entradas selecionadas' : 'Selected inputs'}</dt><dd>{number(summary.selected_input_count)} · {number(summary.selected_input_sat || 0)} sats</dd></div>}
      {summary.payment_hash && <div><dt className="text-fog/55">Payment hash</dt><dd className="font-mono">{summary.payment_hash}</dd></div>}
    </dl>
    {summary.details != null && <details className="mt-3 text-xs"><summary>{pt ? 'Dados da operação' : 'Operation details'}</summary><pre className="mt-2 max-h-40 overflow-auto whitespace-pre-wrap break-all">{JSON.stringify(summary.details, null, 2)}</pre></details>}
  </SensitiveActionModal>
}
