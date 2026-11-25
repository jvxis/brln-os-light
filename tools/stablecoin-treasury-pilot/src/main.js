import './styles.css'
import { createBaseBackup, maximumBackupBytes, restoreBaseAddress } from './wallet.js'

const byId = id => document.getElementById(id)
const modes = byId('modes')
const createForm = byId('create')
const restoreForm = byId('restore')
const backupPanel = byId('backup')
const receivePanel = byId('receive')
const createMode = byId('show-create')
const restoreMode = byId('show-restore')
const newPassword = byId('new-password')
const confirmPassword = byId('confirm-password')
const restorePassword = byId('restore-password')
const backupFile = byId('backup-file')
const verify = byId('verify')
const status = byId('status')
const address = byId('address')
let pendingBackup
let busy = false

function announce(message = '', error = false) {
  status.textContent = message
  status.classList.toggle('error', error)
}

function selectMode(mode) {
  if (busy) return
  const create = mode === 'create'
  createMode.setAttribute('aria-pressed', String(create))
  restoreMode.setAttribute('aria-pressed', String(!create))
  createForm.hidden = !create
  restoreForm.hidden = create
  createForm.reset()
  restoreForm.reset()
  announce()
  const firstField = create ? newPassword : backupFile
  firstField.focus()
}

function start() {
  if (!window.isSecureContext || window.location.hostname !== '127.0.0.1' || window.top !== window.self) {
    announce('Abra este piloto diretamente no endereço local 127.0.0.1 para acessar a carteira.', true)
    return
  }

  modes.hidden = false
  createForm.hidden = false
  createMode.addEventListener('click', () => selectMode('create'))
  restoreMode.addEventListener('click', () => selectMode('restore'))

  createForm.addEventListener('submit', async event => {
    event.preventDefault()
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
    const link = document.createElement('a')
    const url = URL.createObjectURL(new Blob([pendingBackup.backup], { type: 'application/json' }))
    link.href = url
    link.download = 'lightningos-base-backup.json'
    document.body.append(link)
    link.click()
    link.remove()
    window.setTimeout(() => URL.revokeObjectURL(url), 1000)
    backupFile.focus()
  })

  restoreForm.addEventListener('submit', async event => {
    event.preventDefault()
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
