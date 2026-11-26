import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { executeTreasury, getTreasuryStatus, prepareTreasurySolana, quoteTreasury, saveTreasuryBreezKey, saveTreasuryWallet, type TreasuryAmount, type TreasuryDirection, type TreasuryOrder, type TreasuryQuote, type TreasuryStatus } from '../api'
import SensitiveActionModal from '../components/SensitiveActionModal'

const breezForm = 'https://breez.technology/request-api-key/#contact-us-form-sdk'
const signerPath = '/apps/stablecoin-treasury/signer'
const terminal = new Set<TreasuryOrder['state']>(['delivered', 'refunded', 'deposit_failed'])
const validCost = (value: number | undefined): value is number => value !== undefined && Number.isSafeInteger(value) && value >= 0
type Label = (en: string, pt: string) => string

function TreasuryProgress({ text }: { text: string }) {
  return <span role="status" className="inline-flex items-center gap-2 text-sm">
    <span aria-hidden="true" className="h-3.5 w-3.5 shrink-0 animate-spin rounded-full border-2 border-current border-t-transparent motion-reduce:animate-none" />
    {text}
  </span>
}

function parseAmount(input: string) {
  const normalized = input.trim().replace(',', '.')
  if (!/^\d+(\.\d{1,6})?$/.test(normalized)) return 0
  const [whole, fraction = ''] = normalized.split('.')
  const amount = Number(whole + fraction.padEnd(6, '0'))
  return Number.isSafeInteger(amount) && amount > 0 ? amount : 0
}

function money(value: TreasuryAmount, locale: string) {
  const digits = { sats: 0, msats: 0, USDC: 6, SOL: 9 }[value.asset]
  return new Intl.NumberFormat(locale, { maximumFractionDigits: digits }).format(value.atoms / 10 ** digits) + ' ' + value.asset
}

function Review({ quote, locale, label }: { quote: TreasuryQuote; locale: string; label: Label }) {
  const sol = (atoms: number) => money({ atoms, asset: 'SOL' }, locale)
  const shown = (amount?: TreasuryAmount) => amount ? money(amount, locale) : label('Not provided', 'Não informada')
  const rows = [
    [label('Conversion debit', 'Débito da conversão'), shown(quote.debit)],
    [label('Estimated amount received', 'Recebimento estimado'), shown(quote.receive)],
    [label('Conversion fee included in quote', 'Taxa de conversão incluída na cotação'), shown(quote.bridge_fee)],
  ]
  if (quote.direction === 'to_solana') {
    const limit = quote.lightning_fee_limit_sats
    rows.push([label('Maximum Lightning routing fee', 'Teto de taxa de roteamento Lightning'), limit === undefined
      ? label('Not available', 'Indisponível') : money({ atoms: limit, asset: 'sats' }, locale)])
    if (limit !== undefined) rows.push([label('Maximum total debit', 'Débito máximo total'), money({ atoms: quote.debit.atoms + limit, asset: 'sats' }, locale)])
  } else {
    const fee = quote.solana_fee_lamports
    const account = quote.solana_account_lamports
    rows.push([label('Estimated Solana network fee', 'Taxa estimada da rede Solana'), fee === undefined ? label('Not available', 'Indisponível') : sol(fee)])
    rows.push([label('Reserve for destination token account', 'Reserva para a conta de token no destino'), account === undefined ? label('Not available', 'Indisponível') : sol(account)])
    if (fee !== undefined && account !== undefined) rows.push([label('Estimated total in SOL', 'Total estimado em SOL'), sol(fee + account)])
  }
  if (quote.market) {
    const rate = new Intl.NumberFormat(locale, { style: 'currency', currency: 'USD' }).format(quote.market.btc_usd)
    const difference = new Intl.NumberFormat(locale, { style: 'percent', signDisplay: 'exceptZero', maximumFractionDigits: 2 }).format(quote.market.difference_bps / 10_000)
    rows.push([label('Market reference', 'Referência de mercado'), rate + ' / BTC'])
    rows.push([label('Estimated receipt vs. reference', 'Recebimento estimado vs. referência'), difference + ' (' + shown(quote.market.reference_receive) + ')'])
  } else {
    rows.push([label('Market reference', 'Referência de mercado'), label('Unavailable for this quote', 'Indisponível nesta cotação')])
  }
  rows.push([label('Destination', 'Destino'), quote.destination])
  return <dl className="space-y-2 text-sm">
    {rows.map(([term, value]) => <div key={term} className="grid gap-1 sm:grid-cols-2">
      <dt className="text-fog/65">{term}</dt><dd className="break-all font-medium sm:text-right">{value}</dd>
    </div>)}
  </dl>
}

export default function StablecoinTreasury() {
  const { i18n, t } = useTranslation()
  const pt = i18n.language.startsWith('pt')
  const locale = pt ? 'pt-BR' : 'en-US'
  const label: Label = (en, br) => pt ? br : en
  const [status, setStatus] = useState<TreasuryStatus>()
  const [unavailable, setUnavailable] = useState(false)
  const [direction, setDirection] = useState<TreasuryDirection>('to_solana')
  const [amount, setAmount] = useState('')
  const [recipient, setRecipient] = useState<string>()
  const [quote, setQuote] = useState<TreasuryQuote>()
  const [modal, setModal] = useState<'key' | 'lightning' | 'wallet'>()
  const [key, setKey] = useState('')
  const [newWallet, setNewWallet] = useState('')
  const signerFrame = useRef<HTMLIFrameElement>(null)
  const [password, setPassword] = useState('')
  const [progress, setProgress] = useState<'quote' | 'submit' | 'settings'>()
  const busy = progress !== undefined
  const [error, setError] = useState('')
  const [signer, setSigner] = useState<'setup' | 'return'>()
  const [copied, setCopied] = useState(false)
  const walletAddressInput = useRef<HTMLInputElement>(null)
  const [now, setNow] = useState(Date.now())
  const inFlight = useRef(false)
  const refresh = useCallback(async () => {
    try {
      setStatus(await getTreasuryStatus())
      setUnavailable(false)
    } catch {
      setUnavailable(true)
    }
  }, [])
  useEffect(() => {
    void refresh()
    const timer = window.setInterval(() => { void refresh() }, 12_000)
    return () => window.clearInterval(timer)
  }, [refresh])
  useEffect(() => {
    if (!quote) return
    const timer = window.setInterval(() => setNow(Date.now()), 1_000)
    return () => window.clearInterval(timer)
  }, [quote])

  const returnAddress = recipient ?? status?.saved_recipient ?? ''
  const parsed = parseAmount(amount)
  const pending = status?.orders.some(order => !terminal.has(order.state)) ?? false
  const canSign = window.isSecureContext && !!window.crypto?.subtle
  const secureWalletMessage = label('Open LightningOS over HTTPS with a certificate your browser trusts to use the Solana wallet.', 'Para usar a carteira Solana, abra o LightningOS por HTTPS com um certificado confiável para o navegador.')
  const ready = !!status?.enabled && !unavailable && !!status.breez_configured && !!status.solana_address
  const current = !!quote && quote.direction === direction && Date.parse(quote.expires_at) > now &&
    quote.destination === (direction === 'to_solana' ? status?.solana_address : returnAddress)
  const costsKnown = quote?.direction === 'to_solana'
    ? validCost(quote.lightning_fee_limit_sats) && Number.isSafeInteger(quote.lightning_fee_limit_sats + quote.debit.atoms)
    : validCost(quote?.solana_fee_lamports) && validCost(quote?.solana_account_lamports)
      && Number.isSafeInteger(quote.solana_fee_lamports + quote.solana_account_lamports)
  const stateLabel = (state: TreasuryOrder['state']) => ({
    deposit_pending: label('Awaiting deposit confirmation', 'Aguardando confirmação do depósito'),
    delivery_pending: label('Deposit confirmed, delivery in progress', 'Depósito confirmado, entrega em processamento'),
    delivery_issue: label('Checking delivery or refund', 'Verificando a entrega ou o reembolso'),
    delivered: label('Delivered', 'Entregue'),
    refund_pending: label('Awaiting refund confirmation', 'Aguardando confirmação do reembolso'),
    refunded: label('Refunded', 'Reembolsado'),
    deposit_failed: label('Deposit failed', 'Falha no depósito'),
  })[state]

  useEffect(() => {
    if (!signer) return
    let live = true
    const receive = async (event: MessageEvent) => {
      const frame = signerFrame.current?.contentWindow
      if (!frame || event.origin !== 'null' || event.source !== frame || event.data?.treasury !== true) return
      const data = event.data
      if (signer === 'setup' && data.type === 'wallet' && typeof data.address === 'string' &&
          /^[1-9A-HJ-NP-Za-km-z]{32,44}$/.test(data.address)) {
        setNewWallet(data.address)
        setSigner(undefined)
        setError('')
        setModal('wallet')
      }
      if (signer !== 'return' || !quote || data.id !== quote.id || !current || !costsKnown || !ready || pending || inFlight.current) return
      if (data.type === 'prepare') {
        if (data.address !== status.solana_address) {
          frame.postMessage({ treasury: true, type: 'order', id: quote.id }, '*')
          return
        }
        inFlight.current = true
        try {
          const order = await prepareTreasurySolana(quote.id)
          if (live) frame.postMessage({ treasury: true, type: 'order', id: quote.id, order }, '*')
        } catch {
          if (live) frame.postMessage({ treasury: true, type: 'order', id: quote.id }, '*')
        } finally {
          inFlight.current = false
        }
      }
      if (data.type === 'signed' && typeof data.signed?.transaction === 'string' &&
          data.signed?.id === quote.id && data.signed.transaction.length <= 1644) {
        inFlight.current = true
        setProgress('submit')
        try {
          const order = await executeTreasury(quote.id, '', data.signed)
          if (live) {
            setStatus(previous => previous && ({ ...previous, orders: [order, ...previous.orders.filter(item => item.id !== order.id)] }))
            setQuote(undefined)
            setSigner(undefined)
          }
        } catch {
          if (live) setError(pt ? 'Confira a atividade antes de repetir. Se os custos mudarem, gere outra cotação.' : 'Check the activity before repeating. If the costs changed, get another quote.')
        } finally {
          inFlight.current = false
          setProgress(undefined)
          void refresh()
        }
      }
    }
    window.addEventListener('message', receive)
    return () => { live = false; window.removeEventListener('message', receive) }
  }, [signer, quote, status?.solana_address, current, costsKnown, ready, pending, pt, refresh])

  function invalidate() {
    setQuote(undefined)
    setError('')
    setSigner(undefined)
  }

  async function copyWalletAddress() {
    const field = walletAddressInput.current
    if (!field) return
    try {
      await navigator.clipboard.writeText(field.value)
      setCopied(true)
    } catch {
      field.focus()
      field.select()
    }
  }

  function closeModal() {
    setModal(undefined)
    setKey('')
    setNewWallet('')
    setPassword('')
    setError('')
  }

  async function loadQuote() {
    if (!ready || pending || inFlight.current || !parsed) return
    inFlight.current = true
    setProgress('quote')
    invalidate()
    try {
      const [account, domain, ...rest] = returnAddress.trim().split('@')
      const lightningAddress = account && domain && rest.length === 0 ? account + '@' + domain.toLowerCase() : ''
      if (direction === 'to_lightning' && !lightningAddress) throw new Error('invalid destination')
      const newQuote = await quoteTreasury({
        direction, amount: parsed,
        ...(direction === 'to_lightning' ? { lightning_address: lightningAddress } : {}),
      })
      const expectedAsset = direction === 'to_solana' ? 'sats' : 'USDC'
      if (newQuote.direction !== direction || newQuote.debit.asset !== expectedAsset ||
          newQuote.receive.asset !== (direction === 'to_solana' ? 'USDC' : 'sats') ||
          ![newQuote.debit.atoms, newQuote.receive.atoms].every(value => Number.isSafeInteger(value) && value > 0) ||
          newQuote.destination !== (direction === 'to_solana' ? status.solana_address : lightningAddress)) {
        throw new Error('quote mismatch')
      }
      if (direction === 'to_lightning') setRecipient(lightningAddress)
      setNow(Date.now())
      setQuote(newQuote)
    } catch (cause) {
      const code = typeof cause === 'object' && cause && 'code' in cause ? cause.code : undefined
      const messages: Record<string, string> = {
        insufficient_sol: label('Add SOL on the Solana network before trying again.', 'Deposite SOL pela rede Solana antes de tentar novamente.'),
        insufficient_usdc: label('The wallet does not have enough USDC for this quote.', 'A carteira tem USDC insuficiente para esta cotação.'),
        solana_estimate_unavailable: label('The Solana fee estimate is unavailable. Try getting a quote again in a moment.', 'A estimativa da taxa da Solana está indisponível. Tente cotar novamente em instantes.'),
      }
      const message = typeof code === 'string' && Object.prototype.hasOwnProperty.call(messages, code) ? messages[code] : undefined
      setError(message ?? label('Could not get a quote. Check the amount, destination and balances.', 'Não foi possível cotar. Confira valor, destino e saldos.'))
    } finally {
      inFlight.current = false
      setProgress(undefined)
    }
  }

  async function confirm() {
    if (inFlight.current || !password || !modal || !status?.enabled || unavailable ||
        (modal === 'key' && !key.trim())) return
    if (modal === 'lightning' && (!current || !ready || pending || !costsKnown || !quote || Date.parse(quote.expires_at) <= Date.now())) return
    inFlight.current = true
    setProgress(modal === 'lightning' ? 'submit' : 'settings')
    setError('')
    try {
      if (modal === 'key') {
        await saveTreasuryBreezKey(key.trim(), password)
      } else if (modal === 'wallet') {
        await saveTreasuryWallet(newWallet, password)
      } else if (quote) {
        const order = await executeTreasury(quote.id, password)
        setStatus(previous => previous && ({ ...previous, orders: [order, ...previous.orders.filter(item => item.id !== order.id)] }))
        invalidate()
      }
      closeModal()
      await refresh()
    } catch {
      setPassword('')
      setError(modal === 'key'
        ? label('Could not save the key. Check the key and the LightningOS password.', 'Não foi possível salvar a chave. Confira a chave e a senha do LightningOS.')
        : modal === 'wallet'
          ? label('Could not link the wallet. Check the LightningOS password.', 'Não foi possível vincular a carteira. Confira a senha do LightningOS.')
          : label('Check conversion activity before submitting again.', 'Confira a atividade da conversão antes de tentar novamente.'))
      await refresh()
    } finally {
      setProgress(undefined)
      inFlight.current = false
    }
  }

  return <div className="space-y-6">
    <header>
      <h1 className="text-2xl font-semibold">{t('nav.stablecoinTreasury')}</h1>
      <p className="mt-2 text-fog/70">{label('Keep part of your Lightning balance in USDC on Solana and convert it back to bitcoin when you choose.', 'Guarde parte do seu saldo Lightning em USDC na Solana e converta de volta para bitcoin quando quiser.')}</p>
    </header>

    {!status && !unavailable && <TreasuryProgress text={label('Loading Treasury balances…', 'Carregando saldos da Tesouraria…')} />}
    {unavailable && <p role="status" className="section-card">{label('Treasury status is unavailable. The page will check again shortly.', 'O status da Tesouraria está indisponível. A página consultará novamente em instantes.')}</p>}
    {status && !status.enabled && <p role="status" className="section-card">{label('Start USDC Treasury from the App Store to make a conversion.', 'Inicie a Tesouraria USDC na Loja para fazer uma conversão.')}</p>}

    {status?.enabled && <section className="section-card space-y-4">
      <h2 className="text-lg font-semibold">{status.solana_address ? label('Balances', 'Saldos') : label('Set up your Treasury', 'Configure sua Tesouraria')}</h2>
      {!status.breez_configured && <div className="space-y-2">
        <p>{label('Request an SDK API key using the official Breez form. Select the SDK option, fill in your contact information and paste the key here when Breez sends it.', 'Solicite uma chave de API do SDK pelo formulário oficial da Breez. Selecione a opção do SDK, preencha seu contato e cole aqui a chave quando a Breez enviar.')}</p>
        <div className="flex flex-wrap gap-2">
          <a className="btn-secondary" href={breezForm} target="_blank" rel="noopener noreferrer">{label('Request Breez SDK key', 'Solicitar chave do SDK Breez')}</a>
          <button type="button" className="btn-primary" onClick={() => { setError(''); setModal('key') }}>{label('Enter API key', 'Informar chave de API')}</button>
        </div>
      </div>}
      {status.breez_configured && <details className="text-sm">
        <summary className="cursor-pointer font-medium">{label('Breez settings', 'Configuração da Breez')}</summary>
        <button type="button" className="btn-secondary mt-2" disabled={pending} onClick={() => { setError(''); setModal('key') }}>{label('Change API key', 'Alterar a chave de API')}</button>
      </details>}
      {!status.solana_address && !canSign && <p role="status">{secureWalletMessage}</p>}
      {!status.solana_address && <button disabled={!canSign} type="button" className="btn-secondary" onClick={() => setSigner('setup')}>
        {label('Create or recover Solana wallet', 'Criar ou recuperar carteira Solana')}
      </button>}
      {status.solana_address && <div className="grid gap-4 text-sm sm:grid-cols-3">
        {([
          ['Lightning', status.lightning_sats, 'sats'],
          ['USDC · Solana', status.usdc_micros, 'USDC'],
          ['SOL · Solana', status.sol_lamports, 'SOL'],
        ] as const).map(([title, atoms, asset]) => <div key={asset}>
          <p className="text-fog/60">{title}</p>
          <p className="mt-1 text-lg font-semibold">{atoms === undefined ? '—' : money({ atoms, asset }, locale)}</p>
        </div>)}
      </div>}
      {status.solana_address && <details className="text-sm" open={(status.sol_lamports === undefined || status.sol_lamports === 0) || undefined}>
        <summary className="cursor-pointer font-medium">{label('Receive SOL on Solana', 'Receber SOL na Solana')}</summary>
        <p className="mt-2">{label('Suggested starting balance: 0.01 SOL. Send SOL on the Solana network to this address before converting back. Your quote shows the amount required.', 'Saldo inicial sugerido: 0,01 SOL. Envie SOL pela rede Solana para este endereço antes de converter de volta. A cotação mostra o valor necessário.')}</p>
        <div className="mt-2 flex flex-wrap items-center gap-2">
          <input ref={walletAddressInput} className="input-field min-w-0 flex-1 font-mono" aria-label={label('Solana public address', 'Endereço público Solana')} readOnly value={status.solana_address} />
          <button type="button" className="btn-secondary" onClick={() => void copyWalletAddress()}>{label('Copy address', 'Copiar endereço')}</button>
          {copied && <span role="status">{label('Copied', 'Copiado')}</span>}
        </div>
      </details>}
    </section>}

    {ready && <section className="section-card space-y-4">
      <h2 className="text-lg font-semibold">{label('Convert balance', 'Converter saldo')}</h2>
      {pending ? <TreasuryProgress text={label('Delivery can take around an hour or more. Wait for delivery or refund before repeating the conversion.', 'A entrega pode levar cerca de uma hora ou mais. Aguarde a entrega ou o reembolso antes de repetir.')} />
        : <form className="space-y-4" onSubmit={event => { event.preventDefault(); void loadQuote() }}>
          <div className="flex flex-wrap gap-2" role="group" aria-label={label('Direction', 'Sentido da conversão')}>
            <button type="button" className={direction === 'to_solana' ? 'btn-primary' : 'btn-secondary'} aria-pressed={direction === 'to_solana'} disabled={busy} onClick={() => { if (direction !== 'to_solana') { setDirection('to_solana'); setAmount(''); invalidate() } }}>{label('Save in USDC', 'Guardar em USDC')}</button>
            <button type="button" className={direction === 'to_lightning' ? 'btn-primary' : 'btn-secondary'} aria-pressed={direction === 'to_lightning'} disabled={busy} onClick={() => { if (direction !== 'to_lightning') { setDirection('to_lightning'); setAmount(''); invalidate() } }}>{label('Convert to bitcoin', 'Voltar para bitcoin')}</button>
          </div>
          <label className="block text-sm">
                             {direction === 'to_solana' ? label('USDC to save', 'Quanto deseja guardar em USDC') : label('USDC to convert', 'Quanto deseja converter em USDC')}
            <input className="input-field mt-1 w-full" inputMode="decimal"  value={amount} maxLength={24} disabled={busy} required onChange={event => { setAmount(event.target.value); invalidate() }} />
          </label>
          {direction === 'to_lightning' && <label className="block text-sm">
            {label('Lightning Address to receive sats', 'Lightning Address para receber os sats')}
            <input className="input-field mt-1 w-full" inputMode="email" autoComplete="off" spellCheck={false} value={returnAddress} maxLength={265} disabled={busy} required onChange={event => { setRecipient(event.target.value); invalidate() }} />
          </label>}
          <div className="flex flex-wrap items-center gap-3">
            <button type="submit" className="btn-primary" disabled={busy || !parsed} aria-busy={progress === 'quote'}>{label('Review quote and fees', 'Conferir cotação e custos')}</button>
            {progress === 'quote' && <TreasuryProgress text={label('Getting quote and checking fees…', 'Consultando cotação e calculando custos…')} />}
          </div>
        </form>}
      {quote && !pending && <div className="space-y-4 border-t border-white/10 pt-4">
        <Review quote={quote} locale={locale} label={label} />
        {!current && <p role="status">{label('This quote expired. Get a fresh quote to continue.', 'A cotação expirou. Gere outra cotação para continuar.')}</p>}
        {current && !costsKnown && <p role="status">{label('The full cost is unavailable. Get a fresh quote to continue.', 'O custo total está indisponível. Gere outra cotação para continuar.')}</p>}
        {direction === 'to_lightning' && !canSign && <p role="status">{secureWalletMessage}</p>}
        <button type="button" className="btn-primary" disabled={!current || !costsKnown || busy || (direction === 'to_lightning' && !canSign)} onClick={() => {
          if (direction === 'to_solana') setModal('lightning')
          else setSigner('return')
        }}>{direction === 'to_solana' ? label('Confirm conversion', 'Confirmar conversão') : label('Sign with Solana wallet', 'Assinar com a carteira Solana')}</button>
      </div>}
    </section>}

    {!modal && progress === 'submit' && <TreasuryProgress text={label('Submitting your conversion…', 'Enviando sua conversão…')} />}

    {status?.enabled && !unavailable && signer && (signer === 'setup' || (quote && current && costsKnown && !pending)) && <section className="section-card space-y-3">
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-lg font-semibold">{label('Solana wallet', 'Carteira Solana')}</h2>
        <button className="btn-secondary" type="button" disabled={progress === 'submit'} onClick={() => { setSigner(undefined); void refresh() }}>{t('common.close')}</button>
      </div>
      <iframe ref={signerFrame} className="block min-h-[26rem] w-full border-0" title={label('Isolated Solana wallet', 'Carteira Solana isolada')} sandbox="allow-scripts allow-downloads" referrerPolicy="no-referrer"
        src={signerPath + '?lang=' + (pt ? 'pt-BR' : 'en') + (signer === 'return' ? '&quote=' + encodeURIComponent(quote?.id ?? '') : '')} />
    </section>}

    {status?.orders.length ? <section className="section-card space-y-3">
      <h2 className="text-lg font-semibold">{label('Conversion activity', 'Atividade de conversão')}</h2>
      <ul className="space-y-3">
        {status.orders.map(order => <li key={order.id} className="border-t border-white/10 pt-3 text-sm">
          <p className="font-medium">{money(order.debit, locale)}{order.receive && ' → ' + money(order.receive, locale)} · {stateLabel(order.state)}</p>
          <p className="break-all text-fog/65">{order.destination}</p>
          {order.actual_fee && <p>{label('Actual network cost', 'Custo efetivo de rede')} · {money(order.actual_fee, locale)}</p>}
          {order.refund && <p>{label('Refund', 'Reembolso')} · {money(order.refund, locale)}</p>}
        </li>)}
      </ul>
    </section> : null}

    {!modal && error && <p role="alert" className="text-rose-300">{error}</p>}
    <SensitiveActionModal open={!!modal}
      title={modal === 'key' ? label('Save Breez API key', 'Salvar chave de API da Breez') : modal === 'wallet'
        ? label('Link Solana wallet', 'Vincular carteira Solana') : label('Confirm Lightning conversion', 'Confirmar conversão Lightning')}
      description={label('Confirm with your LightningOS password.', 'Confirme com a senha do LightningOS.')}
      password={password} onPasswordChange={setPassword} busy={busy} error={error}
      confirmDisabled={!password || !status?.enabled || unavailable || (modal === 'key' ? !key.trim() : modal === 'wallet'
        ? !newWallet : !current || !ready || pending || !costsKnown)}
      confirmLabel={modal === 'key' ? label('Save key', 'Salvar chave') : modal === 'wallet'
        ? label('Link wallet', 'Vincular carteira') : label('Convert balance', 'Converter saldo')}
      onClose={closeModal} onConfirm={confirm}>
      {modal === 'key' && <label className="mt-4 block text-sm">
        {label('Breez SDK API key', 'Chave de API do SDK Breez')}
        <input className="input-field mt-1 w-full" type="password" autoComplete="off" maxLength={4096} value={key} onChange={event => setKey(event.target.value)} />
      </label>}
      {modal === 'wallet' && <p className="mt-3 break-all font-mono text-sm">{newWallet}</p>}
      {modal === 'lightning' && quote && <div className="mt-4"><Review quote={quote} locale={locale} label={label} /></div>}
      {busy && <div className="mt-4">
        <TreasuryProgress text={progress === 'submit' ? label('Submitting your conversion…', 'Enviando sua conversão…')
          : label('Saving Treasury settings…', 'Salvando a configuração da Tesouraria…')} />
      </div>}
    </SensitiveActionModal>
  </div>
}
