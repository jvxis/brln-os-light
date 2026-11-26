// Run after npm run build. All App Store and Treasury responses use local fixtures.
import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { mkdtemp, readFile } from 'node:fs/promises'
import { createServer } from 'node:http'
import { tmpdir } from 'node:os'
import { extname, join } from 'node:path'

const delay = ms => new Promise(resolve => setTimeout(resolve, ms))
const appPath = '/api/apps/stablecoin-treasury'
const wallet = 'test-solana-address'
const address = 'receiver@example.com'
const quoteId = 'test-quote'
let installed = false
let configured = false
let walletReady = false
let pending = false
let oldOrders = []
let quoteRequests = 0
let submitted = 0
let waitForQuote
const mime = {
  '.html': 'text/html', '.js': 'application/javascript', '.css': 'text/css',
  '.woff2': 'font/woff2', '.png': 'image/png', '.svg': 'image/svg+xml',
}
const app = () => ({
  id: 'stablecoin-treasury', name: 'BRLN Reserva', description: 'Native USDC on Solana',
  installed, available: true, status: installed ? 'running' : 'not_installed', port: 0,
})
const order = () => ({
  id: 'test-order', direction: 'to_solana', state: 'delivery_pending', destination: wallet,
  debit: { atoms: 1700, asset: 'sats' }, receive: { atoms: 1000000, asset: 'USDC' },
})
const status = () => ({
  enabled: installed, breez_configured: configured, orders: pending ? [order(), ...oldOrders] : oldOrders,
  ...(walletReady && { solana_address: wallet, saved_recipient: address, lightning_sats: 5000, usdc_micros: 10000000, sol_lamports: 0 }),
})

async function api(path, method, body) {
  if (path === '/api/auth/state') return { enabled: true, authenticated: true, csrf_token: 'fixture' }
  if (path.startsWith('/api/reports/summary')) return null
  if (path === '/api/reports/reconciliation') return { running: false, missing_count: 0, missing_dates: [] }
  if (path === '/api/wizard/status') return { wallet_exists: true }
  if (path === '/api/lnd/status') return { wallet_state: 'unlocked' }
  if (path === '/api/apps') return [app()]
  if (path === appPath + '/install' && method === 'POST') { installed = true; return {} }
  if (path === appPath + '/status') return status()
  if (path === appPath + '/breez-key' && method === 'POST') {
    assert.deepEqual(body, { api_key: 'fixture-key', confirm_password: 'fixture-password' })
    configured = true
    walletReady = true
    return {}
  }
  if (path === appPath + '/quote' && method === 'POST') {
    quoteRequests++
    await waitForQuote
    const toSolana = body.direction === 'to_solana'
    assert.equal(body.amount, 1000000)
    if (!toSolana) assert.equal(body.lightning_address, address)
    const debit = toSolana ? { atoms: 1700, asset: 'sats' } : { atoms: 1065174, asset: 'USDC' }
    const receive = toSolana ? { atoms: 1000000, asset: 'USDC' } : { atoms: 1674, asset: 'sats' }
    return {
      id: quoteId, direction: body.direction, destination: toSolana ? wallet : address,
      expires_at: new Date(Date.now() + 60_000).toISOString(), debit, receive,
      bridge_fee: { atoms: 21045, asset: 'USDC' },
      ...(toSolana ? { lightning_fee_limit_sats: 10 } : { solana_fee_lamports: 5000, solana_account_lamports: 1488440 }),
      market: { btc_usd: 60000, reference_receive: receive, difference_bps: -42 },
    }
  }
  if (path === appPath + '/execute' && method === 'POST') {
    assert.deepEqual(body, { quote_id: quoteId, confirm_password: 'fixture-password' })
    submitted++
    pending = true
    return order()
  }
  if (path === '/api/ui/preferences/menu') return { exists: true, version: 1, favorites: [], hidden: [] }
  if (path === '/api/ui/preferences/app-store') return { exists: true, version: 1, hidden: [] }
  if (path === '/api/apps/storage-targets') return []
  return {}
}

const server = createServer(async (req, res) => {
  try {
    const pathname = new URL(req.url, 'http://localhost').pathname
    if (pathname.startsWith('/api/')) {
      let body = ''
      for await (const chunk of req) body += chunk
      res.setHeader('Content-Type', 'application/json')
      res.end(JSON.stringify(await api(pathname, req.method, body && JSON.parse(body))))
      return
    }
    if (pathname === '/apps/stablecoin-treasury/signer') {
      res.writeHead(200, { 'Content-Type': 'text/html' }).end('<p>Isolated signer fixture</p>')
      return
    }
    assert(!pathname.includes('..'))
    const file = pathname === '/' ? 'index.html' : pathname.slice(1)
    const data = await readFile(join('dist', file))
    res.writeHead(200, { 'Content-Type': mime[extname(file)] || 'application/octet-stream' }).end(data)
  } catch (error) {
    res.writeHead(error.code === 'ENOENT' ? 404 : 500).end()
    if (error.code !== 'ENOENT') console.error(error)
  }
})

async function until(label, check) {
  for (let i = 0; i < 180; i++) {
    const value = await check()
    if (value) return value
    await delay(100)
  }
  throw new Error('Timed out waiting for ' + label)
}

let child
let ws
try {
  await new Promise((resolve, reject) => server.listen(0, '127.0.0.1', resolve).on('error', reject))
  const base = 'http://127.0.0.1:' + server.address().port
  const profile = await mkdtemp(join(tmpdir(), 'los-treasury-ui-'))
  const browserPath = process.env.LOS_BROWSER_PATH || (process.platform === 'win32'
    ? 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe' : 'chromium')
  child = spawn(browserPath, ['--headless=new', '--no-first-run', '--remote-debugging-port=0', '--user-data-dir=' + profile], { windowsHide: true, stdio: 'ignore' })
  const debugPort = await until('Chromium debugging port', async () => {
    try { return (await readFile(join(profile, 'DevToolsActivePort'), 'utf8')).split(/\s+/)[0] } catch { return false }
  })
  const tab = await fetch('http://127.0.0.1:' + debugPort + '/json/new?about:blank', { method: 'PUT' }).then(response => response.json())
  ws = new WebSocket(tab.webSocketDebuggerUrl)
  await new Promise((resolve, reject) => { ws.onopen = resolve; ws.onerror = reject })
  let sequence = 0
  const waiting = new Map()
  ws.onmessage = event => {
    const message = JSON.parse(event.data)
    if (message.method === 'Runtime.exceptionThrown') {
      console.error(message.params.exceptionDetails.exception?.description || message.params.exceptionDetails.text)
    }
    if (waiting.has(message.id)) {
      waiting.get(message.id)(message)
      waiting.delete(message.id)
    }
  }
  const call = (method, params) => new Promise(resolve => {
    const id = ++sequence
    waiting.set(id, resolve)
    ws.send(JSON.stringify({ id, method, params }))
  })
  const evaluate = async expression => {
    const response = await call('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true })
    assert(!response.error && !response.result.exceptionDetails, JSON.stringify(response))
    return response.result.result.value
  }
  const exists = selector => evaluate('!!document.querySelector(' + JSON.stringify(selector) + ')')
  const text = value => evaluate('document.body.innerText.includes(' + JSON.stringify(value) + ')')
  const button = value => '[...document.querySelectorAll("button")].find(b=>b.textContent.trim()===' + JSON.stringify(value) + ')'
  const click = value => evaluate(button(value) + '.click()')
  const waitText = value => until(value, () => text(value))
  async function input(selector, value) {
    await evaluate('(()=>{const field=document.querySelector(' + JSON.stringify(selector) + ');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,"value").set.call(field,' + JSON.stringify(value) + ');field.dispatchEvent(new Event("input",{bubbles:true}));})()')
  }
  await call('Runtime.enable', {})
  await call('Page.navigate', { url: base + '/#apps' })
  try {
    await waitText('Native USDC on Solana')
    assert(await evaluate(`[...document.querySelectorAll('main h3')].some(h => h.textContent === 'BRLN Reserve')`))
  } catch (error) {
    console.error(await evaluate('document.body.innerText.slice(0, 1500)'))
    throw error
  }
  assert.equal(await exists('a[href="#stablecoin-treasury"]'), false)
  await click('Install')
  await until('App Store opening link and installed menu', () => evaluate('document.querySelectorAll(\'a[href="#stablecoin-treasury"]\').length >= 2'))
  await evaluate('document.querySelector(\'main a[href="#stablecoin-treasury"]\').click()')
  await waitText('Set up BRLN Reserve')
  assert(await evaluate('document.querySelector("main h1").textContent === "BRLN Reserve"'))
  assert(await evaluate(`[...document.querySelectorAll('a[href="#stablecoin-treasury"]')].some(a => a.textContent.trim() === 'BRLN Reserve')`))
  await waitText('Your conversions will appear here.')
  assert(await exists('a[href="https://breez.technology/request-api-key/#contact-us-form-sdk"]'))
  await click('Enter API key')
  await input('[role=dialog] input[type=password][autocomplete=off]', 'fixture-key')
  await input('[role=dialog] input[autocomplete=current-password]', 'fixture-password')
  await click('Save key')
  await waitText('Suggested starting balance: 0.01 SOL')
  await click('Convert to bitcoin')
  assert.equal(await evaluate('document.querySelector(\'input[inputmode="email"]\').value'), address)
  await input('input[inputmode=decimal]', '1')
  let releaseQuote
  waitForQuote = new Promise(resolve => { releaseQuote = resolve })
  try {
    await click('Review quote and fees')
    await waitText('Getting quote and checking fees…')
    assert(await evaluate(button('Review quote and fees') + '.disabled'))
    assert(await exists('[role=status] .animate-spin'))
  } finally {
    releaseQuote()
    waitForQuote = undefined
  }
  await waitText('0.00149344 SOL')
  assert.equal(await text('Getting quote and checking fees…'), false)
  await waitText('Estimated receipt vs. reference')
  await click('Sign with Solana wallet')
  assert(await exists('iframe[sandbox="allow-scripts allow-downloads"][src$="&quote=test-quote"]'))
  await click('Save in USDC')
  await input('input[inputmode=decimal]', '1')
  await click('Review quote and fees')
  await waitText('1,710 sats')
  await click('Confirm conversion')
  await input('[role=dialog] input[autocomplete=current-password]', 'fixture-password')
  await click('Convert balance')
  await waitText('around an hour or more')
  await waitText('Deposit confirmed, delivery in progress')
  assert.equal(quoteRequests, 2)
  assert.equal(submitted, 1)
  assert.equal(await evaluate(button('Review quote and fees') + ' === undefined'), true)
  oldOrders = Array.from({ length: 12 }, (_, i) => ({
    ...order(), id: 'history-' + i, state: 'delivered', destination: 'history-' + i + '@example.com',
  }))
  await evaluate('localStorage.setItem("los-lang", "pt-BR")')
  await call('Emulation.setDeviceMetricsOverride', { width: 390, height: 860, deviceScaleFactor: 1, mobile: true })
  await call('Page.reload', {})
  await waitText('Aguarde a entrega ou o reembolso')
  assert(await evaluate('document.querySelector("main h1").textContent === "BRLN Reserva"'))
  assert(await evaluate(`[...document.querySelectorAll('a[href="#stablecoin-treasury"]')].some(a => a.textContent.trim() === 'BRLN Reserva')`))
  assert(await evaluate('document.documentElement.scrollWidth <= innerWidth'))
  const history = 'document.querySelector("[role=region][aria-labelledby=treasury-history-heading]")'
  await until('scrollable conversion history', () => evaluate(`${history}?.scrollHeight > ${history}?.clientHeight`))
  assert.equal(await evaluate(`getComputedStyle(${history}).overflowY`), 'auto')
  assert(await evaluate(`(() => {
    const region = ${history}
    region.scrollTop = region.scrollHeight
    return region.scrollTop > 0 && region.querySelector('li:last-child').textContent.includes('history-11@example.com')
  })()`))
  pending = false
  await call('Page.reload', {})
  await waitText('Converter saldo')
  assert(await evaluate("Object.defineProperty(crypto, 'subtle', { value: undefined }); typeof crypto.subtle === 'undefined'"))
  await click('Voltar para bitcoin')
  await input('input[inputmode=decimal]', '1')
  await click('Conferir cotação e custos')
  await waitText('Recebimento estimado')
  await waitText('HTTPS com um certificado confiável')
  assert.equal(await evaluate(button('Assinar com a carteira Solana') + '.disabled'), true)
  assert.equal(quoteRequests, 3)
  assert.equal(submitted, 1)
  await call('Page.navigate', { url: base + '/#apps' })
  await until('Portuguese App Store title', () => evaluate(`[...document.querySelectorAll('main h3')].some(h => h.textContent === 'BRLN Reserva')`))
  console.log('PASS: localized app name, install, menu, embedded route, Breez instructions, fees, signer isolation, pending order, mobile pt-BR and quote without Web Crypto.')
} finally {
  ws?.close()
  child?.kill()
  server.close()
}
