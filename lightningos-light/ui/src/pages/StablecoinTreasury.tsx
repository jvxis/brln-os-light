import { FormEvent, ReactNode, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  broadcastTreasuryBase, configureTreasury, getTreasuryOrders, getTreasuryStatus,
  payTreasury, prepareTreasuryBase, quoteTreasury, reconcileTreasury,
  type TreasuryDirection, type TreasuryOrder, type TreasurySigningRequest, type TreasuryStatus
} from '../api'
import { getLocale } from '../i18n'

const requestBreezKey = 'https://breez.technology/request-api-key/#contact-us-form-sdk'
const signerURL = '/treasury-signer.html'
const terminal = new Set(['completed', 'refunded', 'failed', 'expired'])
const card = 'space-y-4 rounded-2xl border border-white/10 p-5'

function Field({ label, children }: {label: string; children: ReactNode}) {
  return <label className="grid gap-2"><span className="text-sm text-fog/75">{label}</span>{children}</label>
}

function fromUSDC(value: string) {
  const [fractionalBase, fraction = ''] = value.trim().replace(',', '.').split('.')
  return Number(fractionalBase) * 1_000_000 + Number(fraction.padEnd(6, '0'))
}

function formatETH(value: string) {
  const digits = value.padStart(19, '0')
  return `${digits.slice(0, -18)}.${digits.slice(-18).replace(/0+$/, '') || '0'} ETH`
}

function downloadSigningRequest(value: TreasurySigningRequest) {
  const url = URL.createObjectURL(new Blob([JSON.stringify(value)], { type: 'application/json' }))
  const link = document.createElement('a')
  link.href = url
  link.download = 'lightningos-base-order.json'
  link.click()
  window.setTimeout(() => URL.revokeObjectURL(url), 1000)
}

export default function StablecoinTreasury() {
  const { i18n, t } = useTranslation()
  const pt = i18n.language === 'pt-BR'
  const label = (english: string, portuguese: string) => pt ? portuguese : english
  const amountFmt = (value: number, decimals: number) => new Intl.NumberFormat(getLocale(i18n.language), {
    maximumFractionDigits: decimals,
  }).format(value / 10 ** decimals)
  const [status, setStatus] = useState<TreasuryStatus>()
  const [orders, setOrders] = useState<TreasuryOrder[]>([])
  const [order, setOrder] = useState<TreasuryOrder>()
  const [signing, setSigning] = useState<TreasurySigningRequest>()
  const [key, setKey] = useState('')
  const [lightning, setLightning] = useState('')
  const [configPassword, setConfigPassword] = useState('')
  const [password, setPassword] = useState('')
  const [signed, setSigned] = useState<File>()
  const [wallet, setWallet] = useState('')
  const [value, setValue] = useState('1')
  const [fee, setFee] = useState('100')
  const [direction, setDirection] = useState<TreasuryDirection>('to_base')
  const [now, setNow] = useState(Date.now())
  const [working, setWorking] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    let alive = true
    Promise.all([getTreasuryStatus(), getTreasuryOrders()]).then(([app, history]) => {
      if (!alive) return
      setStatus(app)
      setLightning(app.lightning_address)
      setOrders(history)
    }).catch((failure: Error) => { if (alive) setError(failure.message) })
    return () => { alive = false }
  }, [])

  useEffect(() => {
    const clock = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(clock)
  }, [])

  const pendingIDs = orders.filter(item => item.state !== 'quoted' && !terminal.has(item.state))
    .slice(0, 3).map(item => item.id).join(',')
  useEffect(() => {
    if (!pendingIDs) return
    let alive = true
    let timeout: number
    const poll = async () => {
      for (const id of pendingIDs.split(',')) {
        if (!alive) break
        try {
          const fresh = await reconcileTreasury(id)
          if (alive) {
            setOrders(history => history.map(item => item.id === id ? fresh : item))
            setOrder(current => current?.id === id ? fresh : current)
          }
        } catch {
          // History and manual refresh remain available during API downtime.
        }
      }
      if (alive) timeout = window.setTimeout(poll, 20000)
    }
    timeout = window.setTimeout(poll, 5000)
    return () => { alive = false; window.clearTimeout(timeout) }
  }, [pendingIDs])

  const update = (fresh: TreasuryOrder) => {
    setOrder(current => !current || current.id === fresh.id ? fresh : current)
    setOrders(history => [fresh, ...history.filter(item => item.id !== fresh.id)].sort((a, b) =>
      b.created_at.localeCompare(a.created_at)))
  }

  const busy = async (action: () => Promise<void>) => {
    setWorking(true)
    setError('')
    try {
      await action()
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : label('Operation failed.', 'A operação falhou.'))
    } finally {
      setWorking(false)
    }
  }

  const save = (event: FormEvent) => {
    event.preventDefault()
    const confirmation = configPassword
    setConfigPassword('')
    void busy(async () => {
      await configureTreasury(key, lightning, confirmation)
      setKey('')
      setStatus(await getTreasuryStatus())
    })
  }

  const quote = (event: FormEvent) => {
    event.preventDefault()
    setOrder(undefined)
    setSigning(undefined)
    setSigned(undefined)
    setPassword('')
    void busy(async () => {
      const fresh = await quoteTreasury(direction, wallet.trim(), fromUSDC(value), direction === 'to_base' ? Number(fee) : 0)
      setOrder(fresh)
      update(fresh)
      if (fresh.direction === 'to_lightning') setSigning(await prepareTreasuryBase(fresh.id))
    })
  }

  const confirm = (event: FormEvent) => {
    event.preventDefault()
    if (!order || !password) return
    const confirmation = password
    setPassword('')
    void busy(async () => {
      if (order.direction === 'to_base') {
        update(await payTreasury(order.id, confirmation))
      } else {
        if (!signed || signed.size > 4096) throw new Error(label('Select the signed transfer file.', 'Selecione o arquivo da transferência assinada.'))
        const prepared = JSON.parse(await signed.text())
        if (prepared?.version !== 1 || prepared.id !== order.id || typeof prepared.raw !== 'string') {
          throw new Error(label('The signed transfer belongs to a different order.', 'A transferência assinada pertence a outra ordem.'))
        }
        await broadcastTreasuryBase(order.id, prepared.raw, confirmation)
        update({ ...order, state: 'broadcasting' })
        setSigned(undefined)
      }
    })
  }

  const mayQuote = status?.installed && status.enabled && status.configured && status.provider_available
  const quoteLive = order?.state === 'quoted' && Date.parse(order.expires_at) > now && mayQuote
  const enoughGas = !!signing && BigInt(signing.availableEthWei) >= BigInt(signing.estimatedTotalWei)

  return <main className="mx-auto max-w-4xl space-y-6">
    <h1 className="text-3xl font-semibold">{t('nav.stablecoinTreasury')}</h1>
    <p className="text-fog/75">{label('Convert sats and native USDC on Base using your own wallet.', 'Converta sats e USDC nativo na Base com sua própria carteira.')}</p>
    {error && <p role="alert" className="rounded-xl border border-rose-500/40 p-3 text-rose-200">{error}</p>}
    <section className={card}>
      <h2 className="text-lg font-semibold">{label('Wallet', 'Carteira')}</h2>
      <p>{label('Create or recover a wallet in the isolated signer. Keep the encrypted file and its password separately.', 'Crie ou recupere uma carteira no assinador isolado. Guarde o arquivo criptografado e a senha separadamente.')}</p>
      <a className="btn-secondary inline-block" href={signerURL} target="_blank" rel="noopener noreferrer">{label('Create or recover wallet', 'Criar ou recuperar carteira')}</a>
    </section>
    {status?.installed && <form className={card} onSubmit={save}>
      <h2 className="text-lg font-semibold">{label('Breez configuration', 'Configuração do Breez')}</h2>
      <a className="text-brass underline" href={requestBreezKey} target="_blank" rel="noopener noreferrer">{label('Request a Breez SDK API key', 'Solicitar chave de API do SDK Breez')}</a>
      <Field label={status.configured ? label('New API key (optional)', 'Nova chave de API (opcional)') : label('Breez API key', 'Chave de API do Breez')}>
        <input className="input-field" type="password" autoComplete="off" maxLength={4096} required={!status.configured} value={key} onChange={e => setKey(e.target.value)} />
      </Field>
      <Field label={label('Lightning Address linked to this LND (for receiving sats)', 'Lightning Address vinculada a este LND (para receber sats)')}>
        <input className="input-field" maxLength={320} placeholder="name@example.org" value={lightning} onChange={e => setLightning(e.target.value)} />
      </Field>
      <Field label={label('LightningOS password', 'Senha do LightningOS')}>
        <input className="input-field" type="password" autoComplete="current-password" required value={configPassword} onChange={e => setConfigPassword(e.target.value)} />
      </Field>
      <button className="btn-primary" disabled={working}>{label('Save settings', 'Salvar configuração')}</button>
    </form>}
    {mayQuote && <form className={card} onSubmit={quote}>
      <h2 className="text-lg font-semibold">{label('New conversion', 'Nova conversão')}</h2>
      <Field label={label('Direction', 'Sentido')}>
        <select className="input-field" value={direction} onChange={e => setDirection(e.target.value as TreasuryDirection)}>
          <option value="to_base">Lightning → USDC (Base)</option><option value="to_lightning">USDC (Base) → Lightning</option>
        </select>
      </Field>
      <Field label={label('Your public Base address', 'Seu endereço público na Base')}>
        <input className="input-field font-mono" required pattern="0x[0-9A-Fa-f]{40}" value={wallet} onChange={e => setWallet(e.target.value)} />
      </Field>
      <Field label={label('Amount to quote (USDC)', 'Valor para cotar (USDC)')}>
        <input className="input-field" required inputMode="decimal" pattern="[0-9]{1,3}([.,][0-9]{1,6})?" value={value} onChange={e => setValue(e.target.value)} />
      </Field>
      {direction === 'to_base' && <Field label={label('Maximum Lightning routing fee (sats)', 'Teto da taxa de roteamento Lightning (sats)')}>
        <input className="input-field" type="number" min={1} max={1000} required value={fee} onChange={e => setFee(e.target.value)} />
      </Field>}
      <button className="btn-primary" disabled={working}>{label('Get quote', 'Cotar')}</button>
    </form>}
    {order && <section className={card}>
      <h2 className="text-lg font-semibold">{label('Amounts and fees', 'Valores e taxas')}</h2>
      <dl className="grid gap-2 sm:grid-cols-[1fr_2fr]">
        <dt>{label('Deposit', 'Depósito')}</dt><dd>{order.direction === 'to_base' ? `${amountFmt(order.amount_in, 0)} sats` : `${amountFmt(order.amount_in, 6)} USDC`}</dd>
        <dt>{label('Expected net receipt', 'Recebimento líquido esperado')}</dt><dd>{order.direction === 'to_base' ? `${amountFmt(order.amount_out, 6)} USDC` : `${amountFmt(order.amount_out, 0)} sats`}</dd>
        <dt>{label('Quoted provider fee, included', 'Taxa cotada do provedor, já incluída')}</dt>
        <dd>{order.provider_fee ? `${amountFmt(order.provider_fee.amount, order.provider_fee.decimals)} ${order.provider_fee.asset}` : label('Not itemized by provider', 'Não discriminada pelo provedor')}</dd>
        <dt>{label('Price tolerance requested from provider', 'Tolerância de preço solicitada ao provedor')}</dt><dd>1%</dd>
        <dt>{label('Effective exchange rate before network fees', 'Câmbio efetivo antes das taxas de rede')}</dt>
        <dd>{new Intl.NumberFormat(getLocale(i18n.language), { maximumFractionDigits: 2 }).format(
          order.direction === 'to_base' ? order.amount_in * 1e6 / order.amount_out : order.amount_out * 1e6 / order.amount_in)} sats/USDC</dd>
        {order.direction === 'to_base' && <>
          <dt>{label('Routing fee cap', 'Teto da taxa de roteamento')}</dt><dd>{order.max_routing_sat} sats</dd>
          <dt>{label('Maximum total debit', 'Débito total máximo')}</dt><dd>{((order.max_routing_sat ?? 0) + order.amount_in).toLocaleString(getLocale(i18n.language))} sats</dd>
        </>}
        <dt>{label('Destination', 'Destino')}</dt><dd className="break-all font-mono">{order.lightning_address ?? order.wallet}</dd>
        <dt>{label('Refund destination', 'Destino de reembolso')}</dt><dd className="break-all">{order.direction === 'to_base' ? label('Invoice on this LND', 'Invoice deste LND') : order.wallet}</dd>
        <dt>{label('Quote expiry', 'Validade da cotação')}</dt><dd>{new Date(order.expires_at).toLocaleString(getLocale(i18n.language))}</dd>
      </dl>
      {quoteLive && order.direction === 'to_lightning' && signing && <div className="space-y-3">
        <dl className="grid gap-2 sm:grid-cols-[1fr_2fr]">
          <dt>{label('Execution fee limit', 'Teto da taxa de execução')}</dt><dd>{formatETH(signing.maximumGasWei)}</dd>
          <dt>{label('Ethereum data fee estimate', 'Estimativa de publicação de dados na Ethereum')}</dt><dd>{formatETH(signing.l1EstimateWei)}</dd>
          <dt>{label('Network operator fee estimate', 'Estimativa da taxa do operador da rede')}</dt><dd>{formatETH(signing.operatorEstimateWei)}</dd>
          <dt>{label('Estimated network budget', 'Orçamento estimado de rede')}</dt><dd>{formatETH(signing.estimatedTotalWei)}</dd>
        </dl>
        {enoughGas ? <ol className="list-decimal space-y-2 pl-6">
          <li><button className="btn-secondary" type="button" onClick={() => downloadSigningRequest(signing)}>{label('Download order for signing', 'Baixar ordem para assinar')}</button></li>
          <li><a className="text-brass underline" href={signerURL} target="_blank" rel="noopener noreferrer">{label('Sign in the isolated wallet', 'Assinar na carteira isolada')}</a></li>
          <li><Field label={label('Signed transfer', 'Transferência assinada')}>
            <input className="input-field" type="file" accept=".json,application/json" onChange={e => setSigned(e.target.files?.[0])} />
          </Field></li>
        </ol> : <p role="status">{label('Add ETH on Base to this wallet to cover network fees.', 'Adicione ETH na Base a esta carteira para cobrir as taxas de rede.')}</p>}
      </div>}
      {quoteLive && (order.direction === 'to_base' || enoughGas) && <form className="space-y-3" onSubmit={confirm}>
        <Field label={label('Confirm LightningOS password', 'Confirme a senha do LightningOS')}>
          <input className="input-field" type="password" autoComplete="current-password" required value={password} onChange={e => setPassword(e.target.value)} />
        </Field>
        <button className="btn-primary" disabled={working || (order.direction === 'to_lightning' && !signed)}>
          {order.direction === 'to_base' ? label('Confirm Lightning payment', 'Confirmar pagamento Lightning') : label('Broadcast signed Base transfer', 'Transmitir transferência assinada na Base')}
        </button>
      </form>}
    </section>}
    <section className={card}>
      <h2 className="text-lg font-semibold">{label('Order history', 'Histórico de ordens')}</h2>
      {orders.map(item => <div key={item.id} className="flex flex-wrap items-center justify-between gap-2 border-t border-white/10 pt-3">
        <span>{item.direction === 'to_base' ? 'Lightning → Base' : 'Base → Lightning'} · {new Date(item.created_at).toLocaleString(getLocale(i18n.language))} · {item.state}</span>
        {item.actual_routing_msat !== undefined && <span>{label('Routing fee', 'Taxa de roteamento')} {amountFmt(item.actual_routing_msat, 3)} sats</span>}
        {item.actual_base_fee_wei !== undefined && <span>{label('Total Base fees', 'Total de taxas da Base')} {formatETH(item.actual_base_fee_wei)}</span>}
        {item.delivered_amount !== undefined && <span>{item.direction === 'to_base' ? `${amountFmt(item.delivered_amount, 6)} USDC` : `${amountFmt(item.delivered_amount, 0)} sats`}</span>}
        {!terminal.has(item.state) && <button className="btn-secondary" disabled={working} onClick={() => void busy(async () => update(await reconcileTreasury(item.id)))}>{label('Refresh status', 'Atualizar status')}</button>}
      </div>)}
    </section>
  </main>
}
