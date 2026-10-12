// Run Vite first. PLAYWRIGHT_MODULE may point to an external index.mjs.
import assert from 'node:assert/strict'
import { mkdir, readFile } from 'node:fs/promises'
import { pathToFileURL } from 'node:url'
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE ? pathToFileURL(process.env.PLAYWRIGHT_MODULE).href : 'playwright')
const browser = await chromium.launch({ channel: process.env.LOS_BROWSER_CHANNEL || 'msedge', headless: true })
const base = process.env.LOS_UI_TEST_URL || 'http://127.0.0.1:5187'
const output = process.env.SMOKE_OUTPUT || 'test-results/manager-restart'
await mkdir(output, { recursive: true })
try {
  for (const language of ['en', 'pt-BR']) {
    const translations = JSON.parse(await readFile(new URL(`../src/i18n/${language}.json`, import.meta.url), 'utf8'))
    const labels = translations.dashboard
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
    const page = await context.newPage()
    const errors = [], requests = []
    const csrfHeaders = []
    let mode = 'success', online = false, authChecks = 0
    let releaseRestart
    page.on('pageerror', error => errors.push(error.message))
    page.setDefaultTimeout(5000)
    await page.addInitScript(lang => {
      localStorage.setItem('los-lang', lang)
      window.authRequiredCount = 0
      window.addEventListener('auth:required', () => window.authRequiredCount++)
    }, language)
    await page.clock.install()
    await page.route('**/api/**', async route => {
      const path = new URL(route.request().url()).pathname
      if (path === '/api/actions/restart') {
        const payload = route.request().postDataJSON()
        requests.push(payload)
        csrfHeaders.push(route.request().headers()['x-csrf-token'])
        if (payload.service === 'lnd') return route.fulfill({ json: { ok: true } })
        if (mode === 'rejected') return route.fulfill({ status: 500, json: { error: 'restart failed' } })
        if (mode === 'forbidden') return route.fulfill({ status: 403, json: { error: 'forbidden' } })
        if (mode === 'dropped') return route.abort('connectionreset')
        if (mode === 'pending') await new Promise(resolve => { releaseRestart = resolve })
        return route.fulfill({ json: { ok: true } })
      }
      if (path === '/api/auth/state') {
        authChecks++
        if (!online) return route.abort('connectionrefused')
        if (mode === 'invalid') return route.fulfill({ json: {} })
        if (mode === 'hung') await new Promise(resolve => { releaseRestart = resolve })
        return route.fulfill({ json: { enabled: ['expired', 'authenticated'].includes(mode), authenticated: mode === 'authenticated', csrf_token: mode === 'authenticated' ? 'fixture-csrf' : '' } })
      }
      if (path === '/api/lnops/channels' || path === '/api/lnops/peers') return route.fulfill({ json: [] })
      if (path === '/api/reports/summary') return route.fulfill({ json: { totals: {}, averages: {}, days: 0 } })
      return route.fulfill({ json: {} })
    })
    await page.goto(base + '/tests/dashboard-smoke.html')
    await page.clock.pauseAt(new Date(Date.now() + 1000))
    const button = page.getByRole('button', { name: labels.restartManager, exact: true })
    const status = page.getByRole('status').filter({ hasText: /Manager/ })
    const alert = page.getByRole('alert').filter({ hasText: /Manager/ })
    const tick = async ms => { await page.clock.runFor(ms); await page.waitForTimeout(100) }
    const start = async () => { await button.click(); await page.waitForTimeout(100) }
    mode = 'pending'
    await start()
    assert.equal(await button.isDisabled(), true, 'restart must immediately disable repeat clicks while the command is pending')
    assert.equal(await button.getAttribute('aria-busy'), 'true')
    assert.equal(await button.locator('.animate-spin').count(), 1)
    assert.equal(await status.innerText(), labels.managerRestart.restarting)
    await button.evaluate(el => { el.click(); el.click() })
    assert.deepEqual(requests, [{ service: 'lightningos-manager' }], 'no duplicate restart requests')
    for (const name of [labels.restartLnd, labels.safeRestart, labels.safeShutdown]) {
      assert.equal(await page.getByRole('button', { name, exact: true }).isDisabled(), true)
    }
    for (const theme of ['default', 'miner-watch']) {
      await page.evaluate(value => document.documentElement.setAttribute('data-visual-theme', value), theme)
      for (const width of [1440, 320, 390, 768]) {
        await page.setViewportSize({ width, height: 1000 })
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `${theme}: overflow at ${width}px`)
        if ([1440, 390].includes(width)) await page.locator('.section-card').first().screenshot({ path: `${output}/${language}-${theme}-${width}.png` })
      }
    }
    await page.emulateMedia({ reducedMotion: 'reduce' })
    assert.equal(await button.locator('.animate-spin').evaluate(el => getComputedStyle(el).animationName), 'none')
    await page.emulateMedia({ reducedMotion: 'no-preference' })
    releaseRestart()
    await page.waitForTimeout(100)
    await tick(5000)
    assert.equal(await button.isDisabled(), true, 'offline Manager must keep showing progress')
    online = true
    await tick(2000)
    assert.equal(await button.isDisabled(), true, 'one response is not enough')
    await tick(2000)
    await status.getByText(labels.managerRestart.available, { exact: true }).waitFor()
    assert.equal(await button.isDisabled(), false)
    assert.equal(requests.length, 1)
    mode = 'authenticated'
    await start()
    await tick(5000)
    await tick(2000)
    await status.getByText(labels.managerRestart.available, { exact: true }).waitFor()
    assert.equal(await page.evaluate(() => window.authRequiredCount), 0, 'valid session stays signed in')
    const settledChecks = authChecks
    await tick(6000)
    assert.equal(authChecks, settledChecks, 'polling stops after recovery')

    for (mode of ['rejected', 'forbidden']) {
      await start()
      await alert.getByText(labels.managerRestart.failed, { exact: true }).waitFor()
      assert.equal(await button.isDisabled(), false)
      assert.equal(authChecks, settledChecks, 'explicit rejection must not be reported as success')
      assert.equal(csrfHeaders.at(-1), 'fixture-csrf', 'restart preserves the shared CSRF contract')
    }
    mode = 'dropped'
    await start()
    const afterDropped = requests.length
    await tick(5000)
    await tick(2000)
    await status.getByText(labels.managerRestart.available, { exact: true }).waitFor()
    assert.equal(requests.length, afterDropped, 'lost response must not resend the command')

    // Deadline also covers a POST or a probe that never settles.
    for (mode of ['invalid', 'pending', 'hung']) {
      await start()
      await tick(5000)
      await tick(85000)
      await alert.getByText(labels.managerRestart.timeout, { exact: true }).waitFor()
      assert.equal(await button.isDisabled(), false)
      releaseRestart?.()
      await page.waitForTimeout(100)
      assert.equal(await alert.innerText(), labels.managerRestart.timeout, 'late response must not overwrite timeout')
    }

    mode = 'expired'
    await start()
    await tick(5000)
    await tick(2000)
    await status.getByText(labels.managerRestart.available, { exact: true }).waitFor()
    assert.equal(await page.evaluate(() => window.authRequiredCount), 1, 'expired session must invoke the existing login flow')

    // Existing neighboring actions retain their confirmation/status flows.
    for (const [name, title] of [[labels.safeRestart, labels.confirmRestartTitle], [labels.safeShutdown, labels.confirmShutdownTitle]]) {
      await page.getByRole('button', { name, exact: true }).click()
      const dialog = page.getByRole('dialog', { name: title })
      await dialog.waitFor()
      await dialog.getByRole('button', { name: translations.common.cancel, exact: true }).click()
    }
    await page.getByRole('button', { name: labels.restartLnd, exact: true }).click()
    await page.getByRole('dialog', { name: labels.lndRestartTitle }).waitFor()
    assert.equal(requests.at(-1).service, 'lnd')

    await page.reload()
    mode = 'success'
    await start()
    await page.getByRole('button', { name: 'Unmount dashboard' }).click()
    const beforeUnmount = authChecks
    await tick(95000)
    assert.equal(authChecks, beforeUnmount, 'unmount cancels polling')
    assert.deepEqual(errors, [])
    console.log(`PASS ${language}: pending feedback, duplicate prevention, offline/recovery, fast restart, errors/retry, lost response, timeout/hung requests, auth expiry, mobile, LND/system actions, cleanup`)
    await context.close()
  }
} finally { await browser.close() }
