import { createSolanaBackup, maxBackupBytes, recoverSolanaAddress, signDirectDeposit, signSponsoredDeposit } from './wallet.mjs'
import { normalizeLightningAddress, reviewDirectDeposit, reviewSponsoredDeposit } from './sponsored.mjs'

const control = id => document.getElementById(id)
const create = control('create')
const recover = control('recover')
const save = control('save')
const status = control('status')
const pilot = ['4177', '4178'].includes(location.port)
let pending
let busy = false

function downloadJSON(text, name) {
  const url = URL.createObjectURL(new Blob([text], { type: 'application/json' }))
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = name
  anchor.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

function formatUSDC(micros) {
  return `${micros / 1_000_000n},${String(micros % 1_000_000n).padStart(6, '0')} USDC`
}

function formatSOL(lamports) {
  return `${lamports / 1_000_000_000n},${String(lamports % 1_000_000_000n).padStart(9, '0')} SOL`
}

const pilotErrors = {
  invalid_lightning_address: 'Informe um Lightning Address no formato nome@dominio.com.',
  recipient_changed: 'O destino da cotação mudou. Confira o endereço e gere uma nova cotação.',
  quote_expired: 'A cotação expirou. Gere uma nova para conferir os valores.',
  invalid_signature_or_order: 'O serviço rejeitou a assinatura ou a ordem. Nenhum envio foi iniciado.',
  simulation_unavailable: 'A rede não respondeu à simulação. Nenhum envio foi iniciado.',
  simulation_rejected: 'A rede rejeitou a simulação. Nenhum envio foi iniciado.',
  refresh_unavailable: 'Não foi possível renovar a validade. Gere uma nova cotação.',
  reconcile: 'A transmissão precisa ser conferida. Aguarde a verificação antes de repetir.',
  busy: 'O assinador está processando outra ação. Aguarde alguns segundos.',
}

const progressLabels = {
  deposit_pending: 'Aguardando confirmação do depósito na Solana.',
  deposit_failed: 'A Solana registrou uma falha na transação.',
  delivery_pending: 'Depósito confirmado. Aguardando a entrega pela ponte.',
  delivered: 'Entrega informada pela ponte.',
  refund_pending: 'Depósito confirmado. Aguardando a confirmação do reembolso na Solana.',
  refunded: 'Reembolso confirmado na carteira Solana.',
  delivery_issue: 'A ponte encerrou a entrega. A ordem segue em conciliação.',
}

function sameReviewedTerms(previous, next) {
  const fields = ['id', 'cluster', 'wallet', 'deposit', 'depositMicros', 'providerFeeMicros',
    'expectedSats', 'lightningAddress', 'feeLamports', 'accountLamports', 'createDepositAccount', 'feeMode']
  return fields.every(field => previous[field] === next[field])
}

function enableSigning() {
  const file = control('order')
  const form = control('authorize')
  const status = control('sign-status')
  const download = control('save-signature')
  const quote = control('pilot-quote')
  const recipientInput = control('pilot-recipient')
  const passwordInput = control('sign-password')
  const confirm = form.querySelector('button[type="submit"]')
  let reviewedFile
  let signed
  let following = false

  function review(documentValue) {
    const direct = documentValue?.feeMode === 'wallet'
    const terms = direct ? reviewDirectDeposit(documentValue) : reviewSponsoredDeposit(documentValue)
    const costs = direct ? [
      ['Débito total em USDC', formatUSDC(terms.usdc)],
      ['Taxa de conversão incluída', terms.providerFee === undefined ? 'Não discriminada pelo provedor' : formatUSDC(terms.providerFee)],
      ['Endereço Lightning informado na ordem', terms.lightningAddress],
      ['Recebimento estimado', `${terms.expectedSats} sats`],
      ['Taxa estimada da rede', formatSOL(terms.feeLamports)],
      ['Reserva para a conta de USDC do destino', formatSOL(terms.accountLamports)],
      ['Total estimado em SOL', formatSOL(terms.feeLamports + terms.accountLamports)],
    ] : [
      ['Taxa do patrocinador', formatUSDC(terms.fee)],
      ['Débito total', formatUSDC(terms.total)],
      ['Conta que recebe a taxa', terms.sponsorFeeAccount],
    ]
    const list = control('costs')
    list.replaceChildren()
    for (const [label, value] of [
      ['Rede', terms.cluster === 'devnet' ? 'Solana devnet · USDC de teste' : 'Solana mainnet'],
      ['Depósito para conversão', formatUSDC(terms.usdc)],
      ...costs,
      ['Destino na Solana', terms.deposit],
      ['Criação de conta de token', terms.createDepositAccount ? 'Incluída na ordem' : 'Conta existente'],
    ]) {
      const title = document.createElement('dt')
      const details = document.createElement('dd')
      title.textContent = label
      details.textContent = value
      list.append(title, details)
    }
    reviewedFile = documentValue
    form.hidden = false
    if (pilot) confirm.disabled = false
  }

  async function pilotRequest(path, body) {
    const response = await fetch(path, {
      method: 'POST', headers: { 'X-LightningOS-Pilot': '1', 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })
    if (!response.ok) {
      const result = await response.json().catch(() => ({}))
      throw new Error(result.error ?? 'pilot_unavailable')
    }
    return response.json()
  }

  async function followOrder() {
    if (following) return
    following = true
    const panel = control('pilot-progress')
    const state = control('pilot-progress-state')
    const details = control('pilot-progress-details')
    let previousState

    async function check() {
      try {
        const snapshot = await pilotRequest('/pilot/status', {})
        if (snapshot.state === 'idle') {
          following = false
          return
        }
        form.hidden = true
        panel.hidden = false
        const message = progressLabels[snapshot.state] + (snapshot.checkDelayed ? ' Atualização da rede pendente.' : '')
        if (state.textContent !== message) state.textContent = message
        if (snapshot.state !== previousState) {
          details.replaceChildren()
          const rows = [
            ['Lightning Address de retorno', snapshot.lightningAddress],
            ['Valor da ordem', formatUSDC(BigInt(snapshot.depositMicros))],
            ['Taxa de rede e reserva estimadas', formatSOL(BigInt(snapshot.feeLamports) + BigInt(snapshot.accountLamports))],
          ]
          if (snapshot.deliveredSats !== undefined) rows.push(['Entrega informada', `${snapshot.deliveredSats} sats`])
          if (snapshot.refundedMicros !== undefined) rows.push(['Reembolso confirmado', formatUSDC(BigInt(snapshot.refundedMicros))])
          for (const [label, value] of rows) {
            const term = document.createElement('dt')
            const description = document.createElement('dd')
            term.textContent = label
            description.textContent = value
            details.append(term, description)
          }
          previousState = snapshot.state
        }
        if (['delivered', 'refunded', 'deposit_failed'].includes(snapshot.state)) return
      } catch {
        panel.hidden = false
        state.textContent = 'A atualização do estado está indisponível. Nova consulta automática em instantes.'
      }
      setTimeout(check, 15_000)
    }

    await check()
  }

  if (pilot) {
    control('import-order').hidden = true
    control('pilot-recipient-field').hidden = false
    recipientInput.required = true
    quote.hidden = false
    form.hidden = false
    confirm.disabled = true
    confirm.textContent = 'Assinar e transmitir os valores exibidos'
    followOrder()
    recipientInput.addEventListener('input', () => {
      reviewedFile = undefined
      confirm.disabled = true
      control('costs').replaceChildren()
      passwordInput.value = ''
      status.textContent = ''
    })
    quote.addEventListener('click', async () => {
      if (busy) return
      const backup = control('sign-wallet').files?.[0]
      if (!backup || backup.size > maxBackupBytes) {
        status.textContent = 'Selecione o backup da carteira Solana antes de cotar.'
        return
      }
      busy = true
      quote.disabled = true
      recipientInput.disabled = true
      confirm.disabled = true
      reviewedFile = undefined
      control('costs').replaceChildren()
      status.textContent = 'Consultando a cotação e os custos de rede…'
      try {
        const recipient = normalizeLightningAddress(recipientInput.value)
        const order = await pilotRequest('/pilot/quote', { recipient })
        if (order.lightningAddress !== recipient) throw new Error('recipient_changed')
        review(order)
        status.textContent = 'Confira os valores e assine. A validade da transação será renovada na confirmação.'
        passwordInput.focus()
      } catch (error) {
        if (error.message === 'reconcile') followOrder()
        status.textContent = pilotErrors[error.message] ?? 'Cotação indisponível. Tente novamente em alguns segundos.'
      } finally {
        busy = false
        quote.disabled = false
        recipientInput.disabled = false
      }
    })
  }

  file.addEventListener('change', async () => {
    reviewedFile = undefined
    signed = undefined
    status.textContent = ''
    download.hidden = true
    form.hidden = true
    form.reset()
    const input = file.files?.[0]
    if (!input) return
    try {
      if (input.size > maxBackupBytes) throw new Error()
      const value = JSON.parse(await input.text())
      if (input === file.files?.[0]) review(value)
    } catch {
      if (input === file.files?.[0]) status.textContent = 'Ordem inválida ou expirada.'
    }
  })
  form.addEventListener('submit', async event => {
    event.preventDefault()
    const backup = control('sign-wallet').files?.[0]
    if (busy || !reviewedFile || !backup || backup.size > maxBackupBytes) return
    const password = passwordInput.value
    if (pilot) passwordInput.value = ''
    else form.reset()
    busy = true
    file.disabled = true
    confirm.disabled = true
    if (pilot) recipientInput.disabled = true
    let submitting = false
    try {
      if (pilot) {
        const current = await pilotRequest('/pilot/refresh', { id: reviewedFile.id })
        if (current.lightningAddress !== reviewedFile.lightningAddress) {
          reviewedFile = undefined
          control('costs').replaceChildren()
          throw new Error('recipient_changed')
        }
        if (!sameReviewedTerms(reviewedFile, current)) {
          review(current)
          status.textContent = 'Os valores da rede mudaram. Confira a cotação atualizada antes de assinar.'
          return
        }
        reviewedFile = current
      }
      const direct = reviewedFile.feeMode === 'wallet'
      const sign = direct ? signDirectDeposit : signSponsoredDeposit
      signed = await sign(await backup.text(), password, reviewedFile)
      reviewedFile = undefined
      if (pilot) {
        submitting = true
        const result = await pilotRequest('/pilot/submit', signed)
        status.textContent = `Transação enviada à Solana. Assinatura: ${result.signature}`
        quote.disabled = true
        followOrder()
        return
      }
      form.hidden = true
      download.hidden = false
      status.textContent = direct
        ? 'Ordem assinada. Baixe o arquivo para transmitir a transação.'
        : 'Ordem assinada. A transmissão depende da assinatura do patrocinador.'
    } catch (error) {
      if (submitting || error.message === 'reconcile') followOrder()
      status.textContent = pilotErrors[error.message] ?? (submitting
        ? 'O serviço não respondeu à transmissão. Aguarde a conferência antes de repetir.'
        : 'Não foi possível assinar. Confira o backup e a senha.')
    } finally {
      busy = false
      file.disabled = false
      confirm.disabled = pilot && !reviewedFile
      if (pilot && !quote.disabled) recipientInput.disabled = false
    }
  })
  download.addEventListener('click', () => {
    if (signed) downloadJSON(JSON.stringify(signed), 'lightningos-solana-signed.json')
  })
}

if (!isSecureContext || location.hostname !== '127.0.0.1' || window !== window.top) {
  create.hidden = true
  recover.hidden = true
  control('sign').hidden = true
  status.textContent = 'Abra o piloto diretamente em 127.0.0.1.'
} else {
  if (pilot) {
    create.hidden = true
    recover.hidden = true
    document.querySelector('main > p').textContent = 'Confira o endereço Lightning e os custos da conversão antes de assinar.'
  }
  enableSigning()
  create.addEventListener('submit', async event => {
    event.preventDefault()
    if (busy) return
    const password = control('password').value
    if (password !== control('repeat').value) {
      status.textContent = 'As senhas precisam ser iguais.'
      return
    }
    create.reset()
    busy = true
    const button = create.querySelector('button')
    button.disabled = true
    status.textContent = 'Criptografando a carteira neste dispositivo…'
    try {
      pending = await createSolanaBackup(password)
      create.hidden = true
      control('receive').hidden = true
      save.hidden = false
      status.textContent = 'Baixe o backup e selecione o arquivo salvo para verificar a recuperação.'
      save.focus()
    } catch {
      status.textContent = 'Não foi possível criar a carteira neste navegador.'
    } finally {
      busy = false
      button.disabled = false
    }
  })

  save.addEventListener('click', () => {
    if (!pending) return
    downloadJSON(pending.backup, 'lightningos-solana-backup.json')
    control('file').focus()
  })

  recover.addEventListener('submit', async event => {
    event.preventDefault()
    if (busy) return
    const file = control('file').files?.[0]
    if (!file || file.size > maxBackupBytes) {
      status.textContent = 'Selecione um backup da carteira Solana do LightningOS.'
      return
    }
    busy = true
    const button = recover.querySelector('button')
    const password = control('recover-password').value
    control('recover-password').value = ''
    button.disabled = true
    status.textContent = 'Verificando o backup neste dispositivo…'
    try {
      const address = await recoverSolanaAddress(await file.text(), password)
      if (pending && address !== pending.address) {
        status.textContent = 'Selecione o backup da carteira que você acabou de criar.'
        return
      }
      pending = undefined
      recover.reset()
      save.hidden = true
      control('address').value = address
      control('receive').hidden = false
      control('address').select()
      status.textContent = 'Backup verificado. Guarde o arquivo e a senha separadamente.'
    } catch {
      status.textContent = 'Não foi possível recuperar. Confira o arquivo e a senha.'
    } finally {
      busy = false
      button.disabled = false
    }
  })
}
