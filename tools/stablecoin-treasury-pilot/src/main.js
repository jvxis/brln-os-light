import './styles.css'
import { createBaseBackup, maximumBackupBytes, restoreBaseAddress, signBaseUSDCTransfer, validateBaseUSDCTransfer } from './wallet.js'

const byId = id => document.getElementById(id)
const modes = byId('modes')
const createForm = byId('create')
const restoreForm = byId('restore')
const backupPanel = byId('backup')
const receivePanel = byId('receive')
const createMode = byId('show-create')
const restoreMode = byId('show-restore')
const signMode = byId('show-sign')
const signPanel = byId('sign')
const signForm = byId('sign-confirm')
const transferFile = byId('transfer-file')
const signedPanel = byId('signed-transfer')
const newPassword = byId('new-password')
const confirmPassword = byId('confirm-password')
const restorePassword = byId('restore-password')
const backupFile = byId('backup-file')
const verify = byId('verify')
const status = byId('status')
const address = byId('address')
let pendingBackup
let pendingTransfer
let signedTransfer
let busy = false

function announce(message = '', error = false) {
  status.textContent = message
  status.classList.toggle('error', error)
}

function selectMode(mode) {
  if (busy) return
  const create = mode === 'create'
  const restore = mode === 'restore'
  const sign = mode === 'sign'
  createMode.setAttribute('aria-pressed', String(create))
  restoreMode.setAttribute('aria-pressed', String(restore))
  signMode.setAttribute('aria-pressed', String(sign))
  createForm.hidden = !create
  restoreForm.hidden = !restore
  signPanel.hidden = !sign
  signForm.hidden = true
  signedPanel.hidden = true
  receivePanel.hidden = true
  createForm.reset()
  restoreForm.reset()
  signForm.reset()
  transferFile.value = ''
  pendingTransfer = undefined
  signedTransfer = undefined
  announce()
  const firstField = create ? newPassword : restore ? backupFile : transferFile
  firstField.focus()
}

function downloadJSON(documentText, filename) {
  const link = document.createElement('a')
  const url = URL.createObjectURL(new Blob([documentText], { type: 'application/json' }))
  link.href = url
  link.download = filename
  document.body.append(link)
  link.click()
  link.remove()
  window.setTimeout(() => URL.revokeObjectURL(url), 1000)
}

function showTransfer(usdcMicros, recipient, gas, fee, transfer) {
  const usdc = `${usdcMicros / 1000000n},${String(usdcMicros % 1000000n).padStart(6, '0')}`
  return `${usdc} USDC → ${recipient}. Limite de execução ${gas * fee} wei. Dados na Ethereum estimados em ${transfer.l1EstimateWei} wei. Taxa de operador estimada em ${transfer.operatorEstimateWei} wei. Orçamento de rede estimado em ${transfer.estimatedTotalWei} wei.`
}

function bindLocalForm(form, submit) {
  const button = form.querySelector('button')
  button.addEventListener('click', () => {
    if (form.reportValidity()) void submit()
  })
  form.addEventListener('keydown', event => {
    if (event.key === 'Enter' && event.target instanceof HTMLInputElement && event.target.type !== 'file') {
      event.preventDefault()
      button.click()
    }
  })
}

function start() {
  const localPilot = window.location.hostname === '127.0.0.1' && window.location.pathname === '/'
  const isolatedSigner = window.location.pathname === '/treasury-signer.html' && window.origin === 'null'
  if (!window.isSecureContext || (!localPilot && !isolatedSigner) || window.top !== window.self) {
    announce('Abra o assinador em uma aba própria pelo LightningOS, ou use o piloto em 127.0.0.1.', true)
    return
  }

  modes.hidden = false
  createForm.hidden = false
  createMode.addEventListener('click', () => selectMode('create'))
  restoreMode.addEventListener('click', () => selectMode('restore'))
  signMode.addEventListener('click', () => selectMode('sign'))

  transferFile.addEventListener('change', async () => {
    const file = transferFile.files?.[0]
    pendingTransfer = undefined
    signedTransfer = undefined
    signForm.reset()
    signForm.hidden = true
    signedPanel.hidden = true
    announce()
    if (!file) return
    const button = signForm.querySelector('button')
    try {
      if (file.size > 4096) throw new Error()
      const transfer = JSON.parse(await file.text())
      if (file !== transferFile.files?.[0]) return
      if (transfer.version !== 1 || !/^[0-9a-f]{32}$/.test(transfer.id)) throw new Error()
      const { amount, destination, gas, fee } = validateBaseUSDCTransfer(transfer)
      if (![transfer.l1EstimateWei, transfer.operatorEstimateWei, transfer.estimatedTotalWei]
        .every(value => typeof value === 'string' && /^(0|[1-9][0-9]{0,29})$/.test(value))) throw new Error()
      if (BigInt(transfer.estimatedTotalWei) !== gas * fee + BigInt(transfer.l1EstimateWei) + BigInt(transfer.operatorEstimateWei)) throw new Error()
      pendingTransfer = transfer
      byId('sign-details').textContent = showTransfer(amount, destination, gas, fee, transfer)
      button.disabled = false
      signForm.hidden = false
    } catch {
      if (file === transferFile.files?.[0]) announce('Arquivo da ordem inválido ou cotação vencida.', true)
    }
  })

  bindLocalForm(signForm, async () => {
    const file = byId('sign-backup').files?.[0]
    if (busy || !pendingTransfer || !file || file.size > maximumBackupBytes) return
    const transfer = pendingTransfer
    const button = signForm.querySelector('button')
    const password = byId('sign-password').value
    signForm.reset()
    busy = true
    button.disabled = true
    transferFile.disabled = true
    announce('Assinando neste dispositivo…')
    try {
      const raw = await signBaseUSDCTransfer(await file.text(), password, transfer)
      signedTransfer = JSON.stringify({ version: 1, id: transfer.id, raw })
      pendingTransfer = undefined
      signForm.hidden = true
      signedPanel.hidden = false
      announce()
      byId('download-transfer').focus()
    } catch {
      announce('Não foi possível assinar. Confira o backup, a senha e a validade da cotação.', true)
    } finally {
      busy = false
      button.disabled = false
      transferFile.disabled = false
    }
  })

  byId('download-transfer').addEventListener('click', () => {
    if (signedTransfer) downloadJSON(signedTransfer, 'lightningos-base-signed-transfer.json')
  })

  bindLocalForm(createForm, async () => {
    if (busy) return
    if (newPassword.value !== confirmPassword.value) {
      announce('As senhas precisam ser iguais.', true)
      confirmPassword.focus()
      return
    }
    busy = true
    const button = createForm.querySelector('button')
    const password = newPassword.value
    createForm.reset()
    button.disabled = true
    announce('Criptografando a carteira neste dispositivo…')
    try {
      pendingBackup = await createBaseBackup(password)
      modes.hidden = true
      createForm.hidden = true
      backupPanel.hidden = false
      restoreForm.hidden = false
      verify.textContent = 'Verificar backup salvo'
      announce()
      byId('download').focus()
    } catch {
      announce('Não foi possível gerar o backup. Tente novamente.', true)
    } finally {
      button.disabled = false
      busy = false
    }
  })

  byId('download').addEventListener('click', () => {
    if (!pendingBackup) return
    downloadJSON(pendingBackup.backup, 'lightningos-base-backup.json')
    backupFile.focus()
  })

  bindLocalForm(restoreForm, async () => {
    if (busy) return
    const file = backupFile.files?.[0]
    if (!file || file.size < 1 || file.size > maximumBackupBytes) {
      announce('Selecione um arquivo de backup da Tesouraria válido.', true)
      return
    }
    busy = true
    verify.disabled = true
    const password = restorePassword.value
    restorePassword.value = ''
    announce('Verificando o backup neste dispositivo…')
    try {
      const recoveredAddress = await restoreBaseAddress(await file.text(), password)
      if (pendingBackup && pendingBackup.address !== recoveredAddress) {
        announce('Este backup pertence a outra carteira. Selecione o arquivo que acabou de baixar.', true)
        return
      }
      pendingBackup = undefined
      restoreForm.reset()
      backupPanel.hidden = true
      restoreForm.hidden = true
      modes.hidden = true
      address.value = recoveredAddress
      receivePanel.hidden = false
      announce('Backup verificado.')
      address.focus()
    } catch {
      announce('Não foi possível abrir o backup. Confira o arquivo e a senha.', true)
      restorePassword.focus()
    } finally {
      verify.disabled = false
      busy = false
    }
  })

  byId('copy').addEventListener('click', async () => {
    try {
      await navigator.clipboard.writeText(address.value)
      announce('Endereço público copiado.')
    } catch {
      address.select()
      announce('Endereço selecionado. Use o comando Copiar do navegador.')
    }
  })
}

start()
