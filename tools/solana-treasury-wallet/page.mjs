import { createSolanaBackup, maxBackupBytes, recoverSolanaAddress, signDirectDeposit, signSponsoredDeposit } from './wallet.mjs'
import { reviewDirectDeposit, reviewSponsoredDeposit } from './sponsored.mjs'

const control = id => document.getElementById(id)
const create = control('create')
const recover = control('recover')
const save = control('save')
const status = control('status')
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
  quote_expired: 'A cotação expirou. Gere uma nova para conferir os valores.',
  invalid_signature_or_order: 'O serviço rejeitou a assinatura ou a ordem. Nenhum envio foi iniciado.',
  simulation_unavailable: 'A rede não respondeu à simulação. Nenhum envio foi iniciado.',
  simulation_rejected: 'A rede rejeitou a simulação. Nenhum envio foi iniciado.',
  refresh_unavailable: 'Não foi possível renovar a validade. Gere uma nova cotação.',
  reconcile: 'A transmissão precisa ser conferida. Aguarde a verificação antes de repetir.',
  busy: 'O assinador está processando outra ação. Aguarde alguns segundos.',
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
  const passwordInput = control('sign-password')
  const confirm = form.querySelector('button[type="submit"]')
  const pilot = location.port === '4177'
  let reviewedFile
  let signed

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

  if (pilot) {
    control('import-order').hidden = true
    quote.hidden = false
    form.hidden = false
    confirm.disabled = true
    confirm.textContent = 'Assinar e transmitir os valores exibidos'
    quote.addEventListener('click', async () => {
      if (busy) return
      const backup = control('sign-wallet').files?.[0]
      if (!backup || backup.size > maxBackupBytes) {
        status.textContent = 'Selecione o backup da carteira Solana antes de cotar.'
        return
      }
      busy = true
      quote.disabled = true
      confirm.disabled = true
      reviewedFile = undefined
      control('costs').replaceChildren()
      status.textContent = 'Consultando a cotação e os custos de rede…'
      try {
        review(await pilotRequest('/pilot/quote', {}))
        status.textContent = 'Confira os valores e assine. A validade da transação será renovada na confirmação.'
        passwordInput.focus()
      } catch (error) {
        status.textContent = pilotErrors[error.message] ?? 'Cotação indisponível. Tente novamente em alguns segundos.'
      } finally {
        busy = false
        quote.disabled = false
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
    let submitting = false
    try {
      if (pilot) {
        const current = await pilotRequest('/pilot/refresh', { id: reviewedFile.id })
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
        return
      }
      form.hidden = true
      download.hidden = false
      status.textContent = direct
        ? 'Ordem assinada. Baixe o arquivo para transmitir a transação.'
        : 'Ordem assinada. A transmissão depende da assinatura do patrocinador.'
    } catch (error) {
      status.textContent = pilotErrors[error.message] ?? (submitting
        ? 'O serviço não respondeu à transmissão. Aguarde a conferência antes de repetir.'
        : 'Não foi possível assinar. Confira o backup e a senha.')
    } finally {
      busy = false
      file.disabled = false
      confirm.disabled = pilot && !reviewedFile
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
  if (location.port === '4177') {
    create.hidden = true
    recover.hidden = true
    document.querySelector('main > p').textContent = 'Converta USDC da carteira do piloto em sats no LOS-TEST2.'
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
