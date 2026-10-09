// Run Vite first. PLAYWRIGHT_MODULE may point to an external index.mjs.
import assert from 'node:assert/strict'
import { mkdir, readFile } from 'node:fs/promises'
import { pathToFileURL } from 'node:url'
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE ? pathToFileURL(process.env.PLAYWRIGHT_MODULE).href : 'playwright')
const browser = await chromium.launch({ channel: process.env.LOS_BROWSER_CHANNEL || 'msedge', headless: true })
const output = process.env.SMOKE_OUTPUT || 'test-results/logs'
const base = process.env.LOS_UI_TEST_URL || 'http://127.0.0.1:5184'
await mkdir(output, { recursive: true })
try {
  for (const language of ['en', 'pt-BR']) {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, timezoneId: 'America/Sao_Paulo', locale: language })
    const page = await context.newPage()
    await page.addInitScript(lang => localStorage.setItem('los-lang', lang), language)
    const errors = [], queries = []
    page.on('pageerror', error => errors.push(error.message))
    let failure = false, delayed = false
    await page.route('**/api/**', async route => {
      const url = new URL(route.request().url())
      if (url.pathname === '/api/apps') return route.fulfill({ json: [{ id: 'fedimint-guardian', installed: true }] })
      if (url.pathname !== '/api/logs/query') return route.fulfill({ json: {} })
      const query = Object.fromEntries(url.searchParams)
      queries.push(query)
      if (delayed && query.service === 'lnd') await new Promise(resolve => setTimeout(resolve, 500))
      if (failure) return route.fulfill({ status: 503, json: { error: 'fixture query unavailable' } })
      const docker = query.service === 'fedimint-guardian'
      const entry = { time: '2026-10-08T17:30:00Z', message: '[ERR] Unable to connect fixture-peer password=[redacted]', level: 'error', event: 'connection' }
      const entries = query.q === 'empty' ? [] : [{ ...entry, message: docker ? 'Docker recent fixture' : entry.message, context: docker ? undefined : [{ ...entry, message: '[INF] surrounding context' }, entry] }]
      await route.fulfill({ json: {
        query: { ...query, since: query.since || '0001-01-01T00:00:00Z', until: query.until || '2026-10-08T18:00:00Z' }, source: docker ? 'docker:fedimintd' : 'systemd:' + query.service,
        entries, capabilities: { period: !docker, filters: !docker, events: query.service === 'lnd', context: query.service === 'lnd' },
        scanned: 1500, matched: entries.length, partial: docker || query.q === 'partial', reason: docker ? 'source_tail' : 'scan_limit', result_limited: query.limit === '500', queried_at: '2026-10-08T18:00:01Z'
      } })
    })
    await page.goto(base + '/tests/logs-smoke.html')
    const results = page.getByTestId('log-results')
    await results.getByText(/Unable to connect/).first().waitFor()
    const labels = language === 'en' ? { period: 'Period', level: 'Level', event: 'LND event', search: 'Search text', since: 'From', until: 'To', query: 'Search logs', export: 'Export results (.txt)', limit: 'Result limit', context: 'View context' } : { period: 'Período', level: 'Nível', event: 'Evento LND', search: 'Buscar texto', since: 'De', until: 'Até', query: 'Consultar logs', export: 'Exportar resultados (.txt)', limit: 'Limite de resultados', context: 'Ver contexto' }
    const field = key => page.getByLabel(labels[key], { exact: true })
    const search = page.getByRole('button', { name: labels.query, exact: true })
    const downloadButton = page.getByRole('button', { name: labels.export, exact: true })
    const initialCount = queries.length
    await field('period').selectOption('period')
    await field('since').fill('2026-10-08T14:00')
    await field('until').fill('2026-10-08T15:00')
    await field('level').selectOption('error')
    await field('event').selectOption('connection')
    await field('search').fill('fixture-peer')
    assert.equal(queries.length, initialCount, 'editing filters must not fire queries')
    assert.equal(await downloadButton.isDisabled(), true, 'pending filters must not export stale results')
    await search.click()
    await downloadButton.waitFor()
    assert.equal(queries.at(-1).since, '2026-10-08T17:00:00.000Z')
    assert.equal(queries.at(-1).until, '2026-10-08T18:00:00.000Z')
    assert.equal(queries.at(-1).level, 'error')
    assert.equal(queries.at(-1).event, 'connection')
    assert.equal(queries.at(-1).q, 'fixture-peer')
    await page.getByText(labels.context, { exact: true }).click()
    await page.getByText(/\[INF\] surrounding context/).waitFor()
    const downloadEvent = page.waitForEvent('download')
    await downloadButton.click()
    const download = await downloadEvent
    const text = await readFile(await download.path(), 'utf8')
    assert.match(download.suggestedFilename(), /^los-logs-lnd-.*\.txt$/)
    assert.match(text, /2026-10-08T17:30:00Z \[ERR\] Unable to connect/)
    assert.match(text, /America\/Sao_Paulo/)
    assert.match(text, /2026-10-08T17:00:00.000Z/)
    assert.doesNotMatch(text, /surrounding context/)
    const beforeInvalid = queries.length
    await field('until').fill('2026-10-08T13:00')
    await search.click()
    await page.getByRole('alert').waitFor()
    assert.equal(queries.length, beforeInvalid)
    await field('until').fill('2026-10-08T15:00')
    await field('search').fill('partial')
    await field('limit').selectOption('500')
    await search.click()
    await page.getByText(/50[,.]000/).waitFor()
    assert.equal(await downloadButton.isEnabled(), true)
    await page.screenshot({ path: output + '/logs-' + language + '-desktop.png', fullPage: true })
    for (const width of [320, 390, 768]) {
      await page.setViewportSize({ width, height: 1000 })
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, language + ' mobile overflow ' + width)
    }
    await page.screenshot({ path: output + '/logs-' + language + '-mobile.png', fullPage: true })
    await field('search').fill('empty')
    await search.click()
    await results.waitFor()
    assert.equal(await results.locator('article').count(), 0)
    assert.equal(await downloadButton.isDisabled(), true)
    failure = true
    await search.click()
    await page.getByRole('alert').getByText('fixture query unavailable').waitFor()
    assert.equal(await page.getByTestId('log-results').count(), 0, 'failed query must not leave old export/results')
    failure = false
    await field('search').fill('fixture-peer')
    await search.click()
    await results.getByText(/Unable to connect/).first().waitFor()
    delayed = true
    await search.click()
    await page.getByRole('button', { name: 'Fedimint Guardian', exact: true }).click()
    await results.getByText('Docker recent fixture').waitFor()
    await page.waitForTimeout(600)
    assert.equal(await field('level').isDisabled(), true)
    assert.equal(await field('search').isDisabled(), true)
    assert.equal(await field('period').locator('option[value=period]').evaluate(el => el.disabled), true)
    assert.equal(await results.getByText('Docker recent fixture').count(), 1, 'late LND response must not overwrite service')
    assert.equal(await downloadButton.isEnabled(), true)
    assert.deepEqual(errors, [])
    assert.doesNotMatch(await page.locator('body').innerText(), /logs\.explorer\./)
    console.log('PASS ' + language + ': timezone, manual submit, context, TXT snapshot, validation, truncation, mobile, empty/error/retry, Docker capabilities and service race')
    await context.close()
  }
} finally { await browser.close() }
