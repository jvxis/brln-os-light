// Requires Playwright (or LOS_PLAYWRIGHT_MODULE pointing to an installed copy).
// All API calls are local fixtures. No real Tor/LND/Manager is contacted.
// Set LOS_TOR_COMPONENT_REF=0.5.43-Beta to reproduce the old UI failure.
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import test from 'node:test'
import { createServer } from 'vite'

const require = createRequire(import.meta.url)
const { chromium } = require(process.env.LOS_PLAYWRIGHT_MODULE || 'playwright')
const root = fileURLToPath(new URL('..', import.meta.url))
const componentPath = 'lightningos-light/ui/src/components/dashboard/TorUpgradeCard.tsx'
const reference = process.env.LOS_TOR_COMPONENT_REF
const oldComponent = reference
  ? execFileSync('git', ['show', `${reference}:${componentPath}`], { cwd: root, encoding: 'utf8' })
  : null

test('Tor modal keeps the initial versions through polling; reopening and warnings still work', async () => {
  const server = await createServer({ root, server: { host: '127.0.0.1', port: 0, strictPort: false, proxy: {} },
    plugins: [{ name: 'tor-regression-fixture', enforce: 'pre',
      resolveId(id) { if (id === 'virtual:tor-fixture') return '\0virtual:tor-fixture' },
      load(id) {
        if (id === '\0virtual:tor-fixture') return `
          import React from 'react';
          import { createRoot } from 'react-dom/client';
          import '/src/i18n/index.ts';
          import Card from '/src/components/dashboard/TorUpgradeCard.tsx';
          createRoot(document.getElementById('root')).render(React.createElement(Card));`
      },
      transform(source, id) {
        if (oldComponent && id.replaceAll('\\', '/').endsWith('/src/components/dashboard/TorUpgradeCard.tsx')) return oldComponent
      },
      configureServer(vite) {
        vite.middlewares.use('/tor-fixture', async (_req, res) => {
          const html = await vite.transformIndexHtml('/tor-fixture', `<!doctype html><div id="root"></div>
            <script type="module" src="/@id/__x00__virtual:tor-fixture"></script>`)
          res.setHeader('Content-Type', 'text/html'); res.end(html)
        })
      },
    }],
  })
  let browser
  try {
    await server.listen()
    browser = await chromium.launch({ channel: process.env.LOS_BROWSER_CHANNEL || 'msedge', headless: true })
    for (const language of ['en', 'pt-BR']) {
      const page = await browser.newPage()
      page.setDefaultTimeout(15000)
      const errors = []
      page.on('pageerror', error => errors.push(error.message))
      await page.addInitScript(lang => localStorage.setItem('los-lang', lang), language)
      let starts = 0
      let status = { version: '0.4.9.12', candidate_version: '0.4.9.14', repository_official: true,
        can_update: true, update_available: true, running: false, service_status: 'healthy', socks_ready: true, control_ready: true }
      let lines = []
      await page.route('**/api/**', async route => {
        const path = new URL(route.request().url()).pathname
        let body
        if (path === '/api/tor/upgrade/status') body = status
        else if (path === '/api/logs') body = { lines }
        else if (path === '/api/tor/upgrade/start' && route.request().method() === 'POST') {
          starts++; status = { ...status, running: true, can_update: false }; body = { ok: true }
        } else { await route.abort(); throw Error(`Unexpected API call: ${path}`) }
        await route.fulfill({ json: body })
      })
      await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/tor-fixture`)
      const upgrade = page.getByRole('button', { name: /^(Upgrade Tor|Atualizar Tor)$/ })
      await upgrade.click()
      const dialog = page.getByRole('dialog')
      const description = dialog.locator('p').first()
      assert.match(await description.innerText(), /v0\.4\.9\.12.*v0\.4\.9\.14/)
      await dialog.getByRole('button', { name: /^(Confirm upgrade|Confirmar upgrade)$/ }).click()
      status = { ...status, version: '0.4.9.14', running: false, update_available: false }
      lines = ['[WARN] LND onion: fixture recovery warning', '[OK] Tor update complete.']
      await dialog.getByRole('alert').waitFor()
      await dialog.getByText(/^(Tor upgrade completed successfully\.|Upgrade do Tor concluído com sucesso\.)$/).waitFor()
      assert.match(await description.innerText(), /v0\.4\.9\.12.*v0\.4\.9\.14/)
      assert.equal(await dialog.getByRole('alert').count(), 1)
      assert.equal(starts, 1)
      await dialog.getByRole('button', { name: /^(Close|Fechar)$/ }).click()
      status = { ...status, candidate_version: '0.4.9.15', can_update: true, update_available: true }
      lines = []
      await page.getByRole('button', { name: /^(Refresh|Atualizar)$/ }).click()
      await upgrade.click()
      assert.match(await description.innerText(), /v0\.4\.9\.14.*v0\.4\.9\.15/)
      await dialog.getByRole('button', { name: /^(Cancel|Cancelar)$/ }).click()
      status = { ...status, running: true, can_update: false }
      await page.getByRole('button', { name: /^(Refresh|Atualizar)$/ }).click()
      await page.getByRole('button', { name: /^(View logs|Ver logs)$/ }).click()
      assert.doesNotMatch(await description.innerText(), /v0\.4\.9/)
      assert.equal(starts, 1)
      assert.deepEqual(errors, [])
      await page.close()
    }
  } finally {
    await browser?.close()
    await server.close()
  }
})
