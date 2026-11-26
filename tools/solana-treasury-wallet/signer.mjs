import { createSolanaBackup, maxBackupBytes, recoverSolanaAddress, signDirectDeposit } from './wallet.mjs'

const get = id => document.getElementById(id)
const params = new URL(location.href).searchParams
const origin = new URL(location.href).origin
const quoteID = params.get('quote')
const en = params.get('lang') === 'en'
const label = (english, portuguese) => en ? english : portuguese
const message = text => { get('message').textContent = text }
const send = (type, payload = {}) => parent.postMessage({ treasury: true, type, ...payload }, origin)
let created
let preparing
let working = false

if (en) {
  document.documentElement.lang = 'en'
  for (const element of document.querySelectorAll('[data-en]')) element.textContent = element.dataset.en
}
if (quoteID) {
  get('setup').hidden = true
  get('submit').textContent = label('Sign reviewed conversion', 'Assinar convers?o revisada')
}

for (const [form, button] of [['create', 'create-wallet'], ['recover', 'submit']]) {
  get(form).addEventListener('keydown', event => {
    if (event.key === 'Enter' && event.target.type === 'password') {
      event.preventDefault()
      get(button).click()
    }
  })
}

get('create-wallet').addEventListener('click', async () => {
  if (working || quoteID || !get('create').reportValidity()) return
  const password = get('new-password').value
  if (password !== get('repeat').value) {
    message(label('The passwords must match.', 'As senhas precisam ser iguais.'))
    return
  }
  get('create').reset()
  working = true
  try {
    created = await createSolanaBackup(password)
    get('create').hidden = true
    get('save').hidden = false
    message(label('Download the backup and keep it separate from its password.', 'Baixe o backup e guarde a senha separadamente.'))
  } catch {
    message(label('This browser could not create the wallet.', 'O navegador n?o conseguiu criar a carteira.'))
  } finally {
    working = false
  }
})

get('save').addEventListener('click', () => {
  if (!created || quoteID) return
  const url = URL.createObjectURL(new Blob([created.backup], { type: 'application/json' }))
  const link = document.createElement('a')
  link.href = url
  link.download = 'lightningos-solana-backup.json'
  link.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
  send('wallet', { address: created.address })
  created = undefined
  get('save').hidden = true
})

get('submit').addEventListener('click', async () => {
  if (working || !get('recover').reportValidity()) return
  const file = get('backup').files?.[0]
  if (!file || file.size > maxBackupBytes) {
    message(label('Select a LightningOS backup.', 'Selecione um backup do LightningOS.'))
    return
  }
  const password = get('password').value
  get('password').value = ''
  working = true
  get('submit').disabled = true
  try {
    const backup = await file.text()
    // Checking the backup before refreshing keeps the fresh blockhash available
    // for signing even when the user needs more time to enter their password.
    const address = await recoverSolanaAddress(backup, password)
    if (!quoteID) {
      send('wallet', { address })
      return
    }
    const fresh = await new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error('Quote refresh timed out')), 35_000)
      preparing = data => {
        clearTimeout(timer)
        if (data.order) resolve(data.order)
        else reject(new Error('Quote refresh failed'))
      }
      send('prepare', { id: quoteID, address })
    })
    if (fresh.id !== quoteID || fresh.wallet !== address || fresh.cluster !== 'mainnet') throw new Error('Order mismatch')
    const signed = await signDirectDeposit(backup, password, fresh)
    send('signed', { id: quoteID, signed })
    message(label('Signed. The Treasury is checking the deposit.', 'Assinatura conclu?da. A Tesouraria est? conferindo o dep?sito.'))
  } catch {
    message(label('Check the backup, password and quote, then try again.', 'Confira o backup, a senha e a cota??o antes de tentar novamente.'))
  } finally {
    preparing = undefined
    working = false
    get('submit').disabled = false
  }
})

window.addEventListener('message', event => {
  if (event.source !== parent || event.origin !== origin || !event.data?.treasury || event.data?.id !== quoteID) return
  if (event.data.type === 'order' && preparing) preparing(event.data)
})
