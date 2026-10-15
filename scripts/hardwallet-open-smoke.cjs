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
        await context.addInitScript(origin => {
          if (location.origin === origin) {
            localStorage.setItem('los-visual-theme','block-zero');
            localStorage.setItem('los-palette','ocean');
            localStorage.setItem('los-theme','light');
          }
        }, base);
        const login = await context.request.post(base + '/api/auth/login', { data: { password }, headers: { Origin: base } });
        assert.equal(login.status(), 200, 'Manager login failed');
        const page = await context.newPage();
        const errors = [];
        context.on('page', p => p.on('pageerror', e => errors.push(e.message)));
        await page.goto(base + '/#apps');
        const card = page.locator('.section-card').filter({ has: page.getByRole('heading', { name: /Hardwallet/ }) });
        const link = card.getByRole('link', { name: /^Open$|^Abrir$/ });
        await link.waitFor({ timeout: 60000 });
        const launch = new URL(await link.getAttribute('href'), base);
        assert.equal(launch.hash, '#hardwallet-open');
        assert.equal(launch.protocol, 'https:');
        assert.equal(launch.origin, base, 'Open must keep the Manager host');
        const popupReady = page.waitForEvent('popup');
        await link.click();
        const popup = await popupReady;
        const app = new URL(launch.origin);
        app.port = '4449';
        await popup.waitForURL(app.href, { timeout: 60000 });
        await popup.getByText('Placa · desconectada', { exact: true }).waitFor({ timeout: 30000 });
        assert(await popup.evaluate(() => isSecureContext && !!navigator.serial));
        assert.deepEqual(await popup.evaluate(() => ({theme:document.documentElement.dataset.visualTheme,
          palette:document.documentElement.dataset.palette,mode:document.documentElement.dataset.theme})),
          {theme:'block-zero',palette:'ocean',mode:'light'}, 'LOS appearance must survive the port change');
        assert.equal(new URL(popup.url()).hash, '', 'Appearance fragment must be consumed');
        assert.deepEqual(errors, [], 'Browser JS errors');
        assert.equal((await context.request.get(app.origin + '/api/v1/status')).status(), 200);
        const anonymous = await browser.newContext();
        try {
          assert.equal((await anonymous.request.get(app.origin + '/api/v1/status')).status(), 401);
          // A saved launcher link goes through the normal login when expired.
          const expired = await anonymous.newPage();
          await expired.goto(launch.href);
          const input = expired.locator('input[autocomplete="current-password"]');
          await input.waitFor({ timeout: 30000 });
          await input.fill(password);
          await expired.locator('form.auth-form--login button[type="submit"]').click();
          await expired.waitForURL(app.href, { timeout: 60000 });
          await expired.getByText('Placa · desconectada', { exact: true }).waitFor();
        } finally { await anonymous.close(); }
        // Launcher must display a recovery action instead of redirecting to a stopped app.
        const blocked = await context.newPage();
        const apps = await (await context.request.get(base + '/api/apps')).json();
        await blocked.route('**/api/apps', route => route.fulfill({ json: apps.map(a => a.id === 'brln-hardwallet' ? { ...a, status: 'stopped' } : a) }));
        await blocked.goto(launch.href);
        await blocked.getByText(/Could not open Hardwallet|Não foi possível abrir o Hardwallet/).waitFor();
        assert.equal(blocked.url(), launch.href);
        await blocked.unrouteAll({ behavior: 'wait' });
        console.log(JSON.stringify({ manager: base, actualPopup: 'passed', appearanceTransfer: 'passed', sameHostSession: true, expiredSessionLogin: 'passed', secureWebSerial: true, anonymous: 401, stoppedRecovery: 'passed' }));
      } finally { await context.close(); }
    }
  } finally { await browser.close(); }
})().catch(error => { console.error('Hardwallet browser check failed: ' + error.name); process.exitCode = 1; });
