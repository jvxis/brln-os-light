import { createSolanaBackup, maxBackupBytes, recoverSolanaAddress, signSponsoredDeposit } from './wallet.mjs'
import { reviewSponsoredDeposit } from './sponsored.mjs'

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

function enableSigning() {
  const file = control('order')
  const form = control('authorize')
  const status = control('sign-status')
  const download = control('save-signature')
  let reviewedFile
  let signed
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
      const documentValue = JSON.parse(await input.text())
      if (input !== file.files?.[0]) return
      const terms = reviewSponsoredDeposit(documentValue)
      const list = control('costs')
      list.replaceChildren()
      for (const [label, value] of [
        ['Rede', terms.cluster === 'devnet' ? 'Solana devnet · USDC de teste' : 'Solana mainnet'],
        ['Depósito para conversão', formatUSDC(terms.usdc)],
        ['Taxa do patrocinador', formatUSDC(terms.fee)],
        ['Débito total', formatUSDC(terms.total)],
        ['Destino na Solana', terms.deposit],
        ['Conta que recebe a taxa', terms.sponsorFeeAccount],
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
    } catch {
      if (input === file.files?.[0]) status.textContent = 'Ordem inválida ou expirada.'
    }
  })
  form.addEventListener('submit', async event => {
    event.preventDefault()
    const backup = control('sign-wallet').files?.[0]
    if (busy || !reviewedFile || !backup || backup.size > maxBackupBytes) return
    const password = control('sign-password').value
    const confirm = form.querySelector('button')
    form.reset()
    busy = true
    file.disabled = true
    confirm.disabled = true
    try {
      signed = await signSponsoredDeposit(await backup.text(), password, reviewedFile)
      reviewedFile = undefined
      form.hidden = true
      download.hidden = false
      status.textContent = 'Ordem assinada. A transmissão depende da assinatura do patrocinador.'
    } catch {
      status.textContent = 'Não foi possível assinar. Verifique o backup, a senha e a validade da ordem.'
    } finally {
      busy = false
      file.disabled = false
      confirm.disabled = false
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
