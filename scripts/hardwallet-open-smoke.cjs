// Authenticated lab-only browser check. Password comes from stdin, never argv/env.
// Requires Playwright (or PLAYWRIGHT_MODULE), trusted node CA and a running app.
// LOS_MANAGER_URLS is a comma-separated list, e.g. the node's LAN and tailnet URLs.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');

(async () => {
  const password = fs.readFileSync(0, 'utf8').trim();
  assert(password, 'Password required on stdin');
  const origins = (process.env.LOS_MANAGER_URLS || '').split(',').filter(Boolean);
  assert(origins.length, 'LOS_MANAGER_URLS required');
  const browser = await chromium.launch({ headless: true, executablePath: process.env.CHROME_PATH || undefined });
  try {
    for (const base of origins) {
      assert.equal(new URL(base).protocol, 'https:');
      const context = await browser.newContext();
      try {
        const login = await context.request.post(base + '/api/auth/login', { data: { password }, headers: { Origin: base } });
        assert.equal(login.status(), 200, 'Manager login failed');
        const page = await context.newPage();
        const errors = [];
        context.on('page', p => p.on('pageerror', e => errors.push(e.message)));
        await page.goto(base + '/#apps');
        const card = page.locator('.section-card').filter({ has: page.getByRole('heading', { name: /Hardwallet/ }) });
        const link = card.getByRole('link', { name: /^Open$|^Abrir$/ });
        await link.waitFor({ timeout: 60000 });
        const launch = new URL(await link.getAttribute('href'));
        assert.equal(launch.hash, '#hardwallet-open');
        assert.equal(launch.protocol, 'https:');
        const popupReady = page.waitForEvent('popup');
        await link.click();
        const popup = await popupReady;
        if (launch.origin !== base) {
          const input = popup.locator('input[autocomplete="current-password"]');
          await input.waitFor({ timeout: 60000 });
          await input.fill(password);
          await popup.locator('form.auth-form--login button[type="submit"]').click();
        }
        const app = new URL(launch.origin);
        app.port = '4449';
        await popup.waitForURL(app.href, { timeout: 60000 });
        await popup.getByText('Placa · desconectada', { exact: true }).waitFor({ timeout: 30000 });
        assert(await popup.evaluate(() => isSecureContext && !!navigator.serial));
        assert.deepEqual(errors, [], 'Browser JS errors');
        assert.equal((await context.request.get(app.origin + '/api/v1/status')).status(), 200);
        const anonymous = await browser.newContext();
        try {
          assert.equal((await anonymous.request.get(app.origin + '/api/v1/status')).status(), 401);
        } finally { await anonymous.close(); }
        // Launcher must display a recovery action instead of redirecting to a stopped app.
        const blocked = await context.newPage();
        await blocked.route('**/api/apps', async route => {
          const response = await route.fetch();
          const apps = await response.json();
          await route.fulfill({ response, json: apps.map(a => a.id === 'brln-hardwallet' ? { ...a, status: 'stopped' } : a) });
        });
        await blocked.goto(launch.href);
        await blocked.getByText(/Could not open Hardwallet|Não foi possível abrir o Hardwallet/).waitFor();
        assert.equal(blocked.url(), launch.href);
        console.log(JSON.stringify({ manager: base, actualPopup: 'passed', loginOnAppHost: launch.origin !== base, secureWebSerial: true, anonymous: 401, stoppedRecovery: 'passed' }));
      } finally { await context.close(); }
    }
  } finally { await browser.close(); }
})().catch(error => { console.error(error.message); process.exitCode = 1; });
