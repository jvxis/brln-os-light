// Run Vite first. This checks UI dispatch; native Docker validation is separate.
import assert from 'node:assert/strict'
import { mkdir, readFile } from 'node:fs/promises'
import { pathToFileURL } from 'node:url'
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE ? pathToFileURL(process.env.PLAYWRIGHT_MODULE).href : 'playwright')
const browser = await chromium.launch({ channel: process.env.LOS_BROWSER_CHANNEL || 'msedge', headless: true })
const base = process.env.LOS_UI_TEST_URL || 'http://127.0.0.1:5189'
const output = process.env.SMOKE_OUTPUT || 'test-results/bitcoin-store'
await mkdir(output, { recursive: true })
try {
  for (const language of ['en', 'pt-BR']) {
    const tr = JSON.parse(await readFile(new URL(`../src/i18n/${language}.json`, import.meta.url), 'utf8'))
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
    const page = await context.newPage()
    const calls = [], errors = []
    let running = true, upgraded = false, failure = false, confirm = false, dialogs = 0
    page.setDefaultTimeout(7000)
    page.on('pageerror', e => { errors.push(e.message); console.error(e.stack) })
    page.on('dialog', async dialog => { dialogs++; assert.equal(dialog.message(), tr.appStore.bitcoinStopConfirm); await (confirm ? dialog.accept() : dialog.dismiss()) })
    await page.addInitScript(lang => localStorage.setItem('los-lang', lang), language)
    await page.route('**/api/**', async route => {
      const request = route.request(), path = new URL(request.url()).pathname
      if (path === '/api/auth/state') return route.fulfill({ json: { enabled: true, authenticated: true, csrf_token: 'fixture-csrf' } })
      if (path === '/api/apps') return route.fulfill({ json: [
        { id: 'bitcoincore', name: 'Bitcoin Core', installed: true, status: running ? 'running' : 'stopped', installed_version: upgraded ? '31.2' : '31.1', available_version: '31.2', update_available: !upgraded },
        { id: 'robosats', name: 'RoboSats', installed: true, status: 'running' }
      ] })
      if (path === '/api/apps/operations') return route.fulfill({ json: {} })
      if (path === '/api/apps/electrs/status') return route.fulfill({ json: { running: false, index_height: 0, tip_height: 0 } })
      if (path === '/api/bitcoin-local/status') return route.fulfill({ json: { source: 'app' } })
      if (path === '/api/bitcoin/source') return route.fulfill({ json: { source: 'local' } })
      if (request.method() === 'POST' && /\/api\/apps\/.+\/(start|stop)$/.test(path)) {
        assert.equal(request.headers()['x-csrf-token'], 'fixture-csrf')
        calls.push(path)
        if (path.endsWith('bitcoincore/stop')) running = false
        if (path.endsWith('bitcoincore/start')) {
          if (failure) return route.fulfill({ status: 500, json: { error: 'fixture startup verification failed' } })
          upgraded = true; running = true
        }
        return route.fulfill({ json: { ok: true } })
      }
      return route.fulfill({ json: {} })
    })
    await page.goto(base + '/tests/app-store-smoke.html')
    const core = page.locator('.section-card').filter({ has: page.getByRole('heading', { name: 'Bitcoin Core', exact: true }) })
    await core.getByText(tr.appStore.updateAvailable, { exact: true }).waitFor()
    assert((await core.innerText()).includes(tr.appStore.bitcoinUpdateInstructions.replace('{{version}}', '31.2')))
    assert.equal(calls.length, 0, 'opening the Store must not apply the update')
    await core.getByRole('button', { name: tr.common.stop, exact: true }).click()
    assert.equal(calls.length, 0, 'cancelled shutdown must not mutate the backend')
    confirm = true
    await core.getByRole('button', { name: tr.common.stop, exact: true }).click()
    await core.getByRole('button', { name: tr.common.start, exact: true }).waitFor()
    failure = true
    await core.getByRole('button', { name: tr.common.start, exact: true }).click()
    await page.getByText('fixture startup verification failed', { exact: true }).waitFor()
    assert.equal(upgraded, false)
    failure = false
    await core.getByRole('button', { name: tr.common.start, exact: true }).click()
    await core.getByText(tr.appStore.upToDate, { exact: true }).waitFor()
    assert.deepEqual(calls, ['/api/apps/bitcoincore/stop', '/api/apps/bitcoincore/start', '/api/apps/bitcoincore/start'])
    const other = page.locator('.section-card').filter({ has: page.getByRole('heading', { name: 'RoboSats', exact: true }) })
    await other.getByRole('button', { name: tr.common.stop, exact: true }).click()
    assert.equal(dialogs, 2, 'other apps retain their existing Stop flow')
    await page.screenshot({ path: `${output}/${language}.png`, fullPage: true })
    assert.deepEqual(errors, [])
    await context.close()
    console.log(`PASS ${language}: versions/instructions, no automatic update, confirmation/cancel, Stop/Start, failed startup/retry, CSRF and unrelated app Stop`)
  }
} finally { await browser.close() }
