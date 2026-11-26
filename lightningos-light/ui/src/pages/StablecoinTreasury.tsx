import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { executeTreasury, getTreasuryStatus, prepareTreasurySolana, quoteTreasury, saveTreasuryBreezKey, saveTreasuryWallet, type TreasuryAmount, type TreasuryDirection, type TreasuryOrder, type TreasuryQuote, type TreasuryStatus } from '../api'
import SensitiveActionModal from '../components/SensitiveActionModal'

const breezHome = 'https://breez.technology/'
const breezForm = 'https://breez.technology/request-api-key/#contact-us-form-sdk'
const signerPath = '/apps/stablecoin-treasury/signer'
const terminal = new Set<TreasuryOrder['state']>(['delivered', 'refunded', 'deposit_failed'])
const validCost = (value: number | undefined): value is number => value !== undefined && Number.isSafeInteger(value) && value >= 0
type Label = (en: string, pt: string) => string

const directionButton = 'rounded-xl px-4 py-2.5 text-left text-sm font-medium transition disabled:opacity-60'
const activeDirection = 'bg-glow text-ink shadow-[0_8px_24px_rgba(34,211,238,.12)]'
const idleDirection = 'text-fog/65 hover:bg-white/5 hover:text-fog'
const panel = 'section-card min-w-0'
const historyTone: Record<TreasuryOrder['state'], string> = {
  deposit_pending: 'border-glow/20 bg-glow/10 text-glow',
  delivery_pending: 'border-glow/20 bg-glow/10 text-glow',
  delivery_issue: 'border-brass/20 bg-brass/10 text-brass',
  delivered: 'border-emerald-400/25 bg-emerald-400/10 text-emerald-300',
  refund_pending: 'border-brass/20 bg-brass/10 text-brass',
  refunded: 'border-brass/20 bg-brass/10 text-brass',
  deposit_failed: 'border-rose-400/25 bg-rose-400/10 text-rose-300',
}

function TreasuryProgress({ text }: { text: string }) {
  return <span role="status" className="inline-flex items-center gap-2 text-sm">
    <span aria-hidden="true" className="h-3.5 w-3.5 shrink-0 animate-spin rounded-full border-2 border-current border-t-transparent motion-reduce:animate-none" />
    {text}
  </span>
}

function TreasuryFlow({ label }: { label: Label }) {
  const wallet = label('Your Solana wallet', 'Sua carteira Solana')
  const bridge = 'Breez / Orchestra'
  const paths = [
    { action: label('Save in USDC', 'Guardar em USDC'), steps: [label('Your Lightning node', 'Seu nó Lightning'), bridge, wallet] },
    { action: label('Convert to bitcoin', 'Voltar para bitcoin'), steps: [wallet, bridge, label('Chosen Lightning Address', 'Lightning Address escolhido')] },
  ]

  return <section id="treasury-how-it-works" aria-labelledby="treasury-flow-heading" className="min-w-0 space-y-3 rounded-2xl border border-white/10 bg-black/20 p-3 sm:p-4">
    <h2 id="treasury-flow-heading" className="text-sm font-semibold">{label('How it works', 'Como funciona')}</h2>
    {paths.map(({ action, steps }) => <div key={action}>
      <p className="mb-1.5 text-xs font-medium text-fog/75">{action}</p>
      <ol aria-label={action} className="grid grid-cols-3 gap-3 sm:gap-5">
        {steps.map((step, index) => <li key={step} className={`relative flex min-w-0 items-center justify-center rounded-xl border p-1.5 text-center text-xs leading-4 [overflow-wrap:anywhere] sm:p-2 ${step === wallet ? 'border-brass/30 bg-brass/10 text-brass' : 'border-white/10 bg-white/5 text-fog'}`}>
          {step}
          {index < steps.length - 1 && <span aria-hidden="true" className="absolute left-full top-1/2 ml-0.5 h-1.5 w-1.5 -translate-y-1/2 rotate-45 border-r border-t border-glow sm:ml-1.5" />}
        </li>)}
      </ol>
    </div>)}
    <p className="border-t border-white/10 pt-2 text-xs text-fog/65">
      {label('Conversions via', 'Conversões via')}{' '}
      <a href={breezHome} target="_blank" rel="noopener noreferrer" className="font-medium text-glow underline underline-offset-2 hover:text-fog">
        Breez Technology <span aria-hidden="true">↗</span>
      </a>
    </p>
  </section>
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
  const totals = [
    [label('Conversion debit', 'Débito da conversão'), shown(quote.debit)],
    [label('Estimated amount received', 'Recebimento estimado'), shown(quote.receive)],
  ]
  const rows = [[label('Conversion fee included in quote', 'Taxa de conversão incluída na cotação'), shown(quote.bridge_fee)]]
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
  return <dl className="space-y-3 text-sm">
    <div className="grid gap-2 sm:grid-cols-2">
      {totals.map(([term, value], index) => <div key={term} className={`min-w-0 rounded-2xl border p-3 ${index ? 'border-brass/25 bg-brass/[.08]' : 'border-white/10 bg-black/15'}`}>
        <dt className="text-xs text-fog/65">{term}</dt>
        <dd className="mt-1 break-words text-lg font-semibold tabular-nums">{value}</dd>
      </div>)}
    </div>
    <div className="divide-y divide-white/10 rounded-2xl border border-white/10 bg-black/15 px-3">
      {rows.map(([term, value]) => <div key={term} className="grid gap-1 py-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
        <dt className="text-fog/65">{term}</dt><dd className="break-words font-medium tabular-nums sm:text-right">{value}</dd>
      </div>)}
    </div>
  </dl>
}

export default function StablecoinTreasury() {
  const { i18n, t } = useTranslation()
  const pt = i18n.language.startsWith('pt')
  const locale = pt ? 'pt-BR' : 'en-US'
  const label: Label = (en, br) => pt ? br : en
  const appName = t('nav.stablecoinTreasury')
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
  const quoteHeading = useRef<HTMLHeadingElement>(null)
  const signerHeading = useRef<HTMLHeadingElement>(null)
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
    quoteHeading.current?.focus()
    const timer = window.setInterval(() => setNow(Date.now()), 1_000)
    return () => window.clearInterval(timer)
  }, [quote])

  useEffect(() => {
    if (signer) signerHeading.current?.focus()
  }, [signer])

  const returnAddress = recipient ?? status?.saved_recipient ?? ''
  const parsed = parseAmount(amount)
  const sourceBalance = direction === 'to_solana' ? status?.lightning_sats : status?.usdc_micros
  const sourceAsset = direction === 'to_solana' ? 'sats' : 'USDC'
  const pending = status?.orders.some(order => !terminal.has(order.state)) ?? false
  const canSign = window.isSecureContext && !!window.crypto?.subtle
  const secureWalletMessage = label('Open LightningOS over HTTPS with a certificate your browser trusts to use the Solana wallet.', 'Para usar a carteira Solana, abra o LightningOS por HTTPS com um certificado confiável para o navegador.')
  const ready = !!status?.enabled && !unavailable && !!status.breez_configured && !!status.solana_address
  const current = !!quote && quote.direction === direction && Date.parse(quote.expires_at) > now &&
    quote.destination === (direction === 'to_solana' ? status?.solana_address : returnAddress)
  const secondsRemaining = quote && current ? Math.ceil((Date.parse(quote.expires_at) - now) / 1_000) : 0
  const quoteCountdown = Math.floor(secondsRemaining / 60) + ':' + String(secondsRemaining % 60).padStart(2, '0')
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
    <header className="rounded-[2rem] border border-brass/20 bg-[radial-gradient(circle_at_12%_0%,rgba(245,158,11,.2),transparent_40%),radial-gradient(circle_at_92%_20%,rgba(34,211,238,.1),transparent_36%),linear-gradient(135deg,rgba(13,17,23,.99),rgba(21,25,32,.96))] p-6 shadow-panel sm:p-8">
      <div className="grid items-center gap-5 lg:grid-cols-[minmax(0,1fr)_minmax(0,25rem)]">
        <div className="flex flex-wrap items-start gap-4 sm:flex-nowrap sm:items-center sm:gap-5">
          <span aria-hidden="true" className="flex h-14 w-14 shrink-0 items-center justify-center rounded-2xl border border-brass/25 bg-brass/10 text-3xl font-semibold text-brass sm:h-16 sm:w-16">$</span>
          <div className="min-w-0">
            <h1 className="text-3xl font-semibold tracking-tight sm:text-4xl">{appName}</h1>
            <p className="mt-2 text-sm font-medium text-brass">{label('Self-custodied USDC on Solana', 'USDC em autocustódia na Solana')}</p>
            <p className="mt-1 max-w-lg text-sm leading-6 text-fog/70">{label('You control the wallet key with your encrypted backup and password.', 'Você controla a chave da carteira com seu backup criptografado e sua senha.')}</p>
          </div>
        </div>
        <TreasuryFlow label={label} />
      </div>
    </header>

    {!status && !unavailable && <TreasuryProgress text={label(`Loading ${appName} balances…`, `Carregando saldos do ${appName}…`)} />}
    {unavailable && <p role="status" className="section-card">{label(`${appName} status is unavailable. The page will check again shortly.`, `O ${appName} está indisponível. A página consultará novamente em instantes.`)}</p>}
    {status && !status.enabled && <p role="status" className="section-card">{label(`Start ${appName} from the App Store to make a conversion.`, `Inicie o ${appName} na Loja para fazer uma conversão.`)}</p>}

    {status?.enabled && <section className="section-card space-y-4">
      <h2 className="text-lg font-semibold">{status.solana_address ? label('Balances', 'Saldos') : label(`Set up ${appName}`, `Configure o ${appName}`)}</h2>
      {status.solana_address && <div className="grid gap-2 text-sm sm:grid-cols-3">
        {([
          ['Lightning', status.lightning_sats, 'sats'],
          ['USDC · Solana', status.usdc_micros, 'USDC'],
          ['SOL · Solana', status.sol_lamports, 'SOL'],
        ] as const).map(([title, atoms, asset]) => <div key={asset} className="min-w-0 rounded-2xl border border-white/10 bg-black/15 p-3">
          <p className="text-fog/60">{title}</p>
          <p className="mt-1 break-words text-xl font-semibold tabular-nums">{atoms === undefined ? '—' : money({ atoms, asset }, locale)}</p>
        </div>)}
      </div>}
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

    <div className="grid items-start gap-6 xl:grid-cols-[minmax(0,3fr)_minmax(20rem,2fr)]">
      <div className="min-w-0 space-y-4">
        {ready && <section className={panel + ' space-y-4'}>
          <h2 className="text-lg font-semibold">{label('Convert balance', 'Converter saldo')}</h2>
          {pending ? <TreasuryProgress text={label('Delivery can take around an hour or more. Wait for delivery or refund before repeating the conversion.', 'A entrega pode levar cerca de uma hora ou mais. Aguarde a entrega ou o reembolso antes de repetir.')} />
            : <form className="space-y-4" onSubmit={event => { event.preventDefault(); void loadQuote() }}>
              <div className="grid gap-1 rounded-2xl border border-white/10 bg-black/15 p-1 sm:grid-cols-2" role="group" aria-label={label('Direction', 'Sentido da conversão')}>
                <button type="button" className={directionButton + ' ' + (direction === 'to_solana' ? activeDirection : idleDirection)} aria-pressed={direction === 'to_solana'} disabled={busy} onClick={() => { if (direction !== 'to_solana') { setDirection('to_solana'); setAmount(''); invalidate() } }}>{label('Save in USDC', 'Guardar em USDC')}</button>
                <button type="button" className={directionButton + ' ' + (direction === 'to_lightning' ? activeDirection : idleDirection)} aria-pressed={direction === 'to_lightning'} disabled={busy} onClick={() => { if (direction !== 'to_lightning') { setDirection('to_lightning'); setAmount(''); invalidate() } }}>{label('Convert to bitcoin', 'Voltar para bitcoin')}</button>
              </div>
              <div className="text-sm">
                <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
                  <label htmlFor="treasury-amount" className="font-medium">{direction === 'to_solana' ? label('Amount to save', 'Valor para guardar') : label('Amount to convert', 'Valor para converter')}</label>
                  {sourceBalance !== undefined && <span className="text-xs text-fog/65">
                    {label('Available', 'Disponível')} · {money({ atoms: sourceBalance, asset: sourceAsset }, locale)}
                  </span>}
                </div>
                <div className="relative mt-2">
                  <input id="treasury-amount" className="input-field w-full !pr-16 text-lg tabular-nums" inputMode="decimal" aria-describedby="treasury-amount-unit"
                    placeholder={pt ? '0,00' : '0.00'} value={amount} maxLength={24} disabled={busy} required
                    onChange={event => { setAmount(event.target.value); invalidate() }} />
                  <span id="treasury-amount-unit" className="pointer-events-none absolute inset-y-0 right-3 flex items-center text-xs font-semibold text-fog/70">USDC</span>
                </div>
              </div>
              {direction === 'to_lightning' && <div className="text-sm">
                <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
                  <label htmlFor="treasury-lightning-address" className="font-medium">{label('Lightning Address to receive sats', 'Lightning Address para receber os sats')}</label>
                  {status.saved_recipient && status.saved_recipient === returnAddress && <span className="text-xs font-medium text-brass">
                    {label('Last successful destination', 'Último destino confirmado')}
                  </span>}
                </div>
                <input id="treasury-lightning-address" className="input-field mt-2 w-full" inputMode="email" autoComplete="off" spellCheck={false} placeholder={label('name@example.com', 'nome@exemplo.com')}
                  value={returnAddress} maxLength={265} disabled={busy} required onChange={event => { setRecipient(event.target.value); invalidate() }} />
              </div>}
              <div className="flex flex-wrap items-center gap-3">
                <button type="submit" className="btn-primary" disabled={busy || !parsed} aria-busy={progress === 'quote'}>{label('Review quote and fees', 'Conferir cotação e custos')}</button>
                {progress === 'quote' && <TreasuryProgress text={label('Getting quote and checking fees…', 'Consultando cotação e calculando custos…')} />}
              </div>
            </form>}
          {quote && !pending && <div className="space-y-4 border-t border-white/10 pt-4">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <h3 ref={quoteHeading} tabIndex={-1} className="scroll-mt-24 rounded text-base font-semibold focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-glow">
                {label('Quote and costs', 'Cotação e custos')}
              </h3>
              {current && <span className="rounded-full border border-white/10 bg-white/5 px-2.5 py-1 text-xs tabular-nums text-fog/75">
                {label('Valid for', 'Válida por')} {quoteCountdown}
              </span>}
            </div>
            <Review quote={quote} locale={locale} label={label} />
            {!current && <p role="status">{label('This quote expired. Get a fresh quote to continue.', 'A cotação expirou. Gere outra cotação para continuar.')}</p>}
            {current && !costsKnown && <p role="status">{label('The full cost is unavailable. Get a fresh quote to continue.', 'O custo total está indisponível. Gere outra cotação para continuar.')}</p>}
            {direction === 'to_lightning' && !canSign && <p role="status">{secureWalletMessage}</p>}
            {!current ? <button type="button" className="btn-secondary" disabled={busy} onClick={() => void loadQuote()}>
              {label('Refresh quote', 'Renovar cotação')}
            </button> : <button type="button" className="btn-primary" disabled={!costsKnown || busy || (direction === 'to_lightning' && !canSign)} onClick={() => {
              if (direction === 'to_solana') setModal('lightning')
              else setSigner('return')
            }}>{direction === 'to_solana' ? label('Confirm conversion', 'Confirmar conversão') : label('Sign with Solana wallet', 'Assinar com a carteira Solana')}</button>}
          </div>}
        </section>}

        {!modal && error && <p role="alert" className="rounded-xl border border-rose-400/20 bg-rose-400/10 p-3 text-sm text-rose-200">{error}</p>}
        {!modal && progress === 'submit' && <TreasuryProgress text={label('Submitting your conversion…', 'Enviando sua conversão…')} />}

        {status?.enabled && !unavailable && signer && (signer === 'setup' || (quote && current && costsKnown && !pending)) && <section className="section-card space-y-3">
          <div className="flex items-center justify-between gap-2">
            <h2 ref={signerHeading} tabIndex={-1} className="scroll-mt-24 rounded text-lg font-semibold focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-glow">
              {label('Solana wallet', 'Carteira Solana')}
            </h2>
            <button className="btn-secondary" type="button" disabled={progress === 'submit'} onClick={() => { setSigner(undefined); void refresh() }}>{t('common.close')}</button>
          </div>
          <iframe ref={signerFrame} className="block min-h-[26rem] w-full border-0" title={label('Isolated Solana wallet', 'Carteira Solana isolada')} sandbox="allow-scripts allow-downloads" referrerPolicy="no-referrer"
            src={signerPath + '?lang=' + (pt ? 'pt-BR' : 'en') + (signer === 'return' ? '&quote=' + encodeURIComponent(quote?.id ?? '') : '')} />
        </section>}

      </div>
      {status?.enabled && <section className={panel + ' space-y-4'}>
        <h2 id="treasury-history-heading" className="text-lg font-semibold">{label('Conversion history', 'Histórico de conversões')}</h2>
        {status.orders.length ? <div role="region" aria-labelledby="treasury-history-heading" tabIndex={0} className="max-h-[25rem] overflow-y-auto overscroll-contain pr-2 [scrollbar-color:rgb(245_158_11_/_0.45)_transparent] [scrollbar-gutter:stable]">
          <ul className="space-y-2">
            {status.orders.map(order => <li key={order.id} className="space-y-2 rounded-2xl border border-white/10 bg-black/15 p-3 text-sm">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <span className="text-xs font-medium text-fog/65">{order.direction === 'to_solana' ? 'Lightning → Solana' : 'Solana → Lightning'}</span>
                <span className={`rounded-full border px-2 py-1 text-xs font-medium ${historyTone[order.state]}`}>{stateLabel(order.state)}</span>
              </div>
              <p className="break-words font-semibold tabular-nums">{money(order.debit, locale)}{order.receive && ' → ' + money(order.receive, locale)}</p>
              <p className="break-all text-xs text-fog/65">{order.destination}</p>
              {order.actual_fee && <p className="text-xs text-fog/70">{label('Actual network cost', 'Custo efetivo de rede')} · {money(order.actual_fee, locale)}</p>}
              {order.refund && <p className="text-xs text-fog/70">{label('Refund', 'Reembolso')} · {money(order.refund, locale)}</p>}
            </li>)}
          </ul>
        </div> : <p className="rounded-2xl border border-dashed border-white/10 p-4 text-sm text-fog/65">{label('Your conversions will appear here.', 'Suas conversões vão aparecer aqui.')}</p>}
      </section>}
    </div>

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
          : label(`Saving ${appName} settings…`, `Salvando a configuração do ${appName}…`)} />
      </div>}
    </SensitiveActionModal>
  </div>
}
