const { chromium } = require('playwright');
const assert = require('node:assert/strict');
(async () => {
  const browser = await chromium.launch({ channel: process.env.LOS_BROWSER_CHANNEL || 'msedge', headless: true });
  try {
    for (const language of ['en', 'pt-BR']) {
      const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
      const page = await context.newPage();
      await page.addInitScript(lang => localStorage.setItem('los-lang', lang), language);
      const errors = [];
      page.on('pageerror', error => errors.push(error.message));
      let authenticated = false;
      const auth = () => ({ enabled: true, password_configured: true, setup_required: false, authenticated });
      await page.route('**/api/**', async route => {
        const path = new URL(route.request().url()).pathname;
        let data = {};
        if (path === '/api/auth/state') data = auth();
        else if (path === '/api/auth/login') {
          if (route.request().postDataJSON().password !== 'fixture-only') return route.fulfill({ status: 401, json: { error: 'Invalid fixture password' } });
          authenticated = true; data = auth();
        } else if (path === '/api/wizard/status') data = { wallet_exists: true };
        else if (path === '/api/lnd/status') data = { wallet_state: 'unlocked', alias: 'Disposable browser fixture', synced_to_chain: true };
        else if (path === '/api/ui/preferences/menu') data = { exists: true, favorites: [], hidden: [] };
        else if (path.includes('/reports/reconciliation')) data = { running: false, missing_count: 0, missing_dates: [] };
        else if (path === '/api/apps' || path === '/api/lnops/channels' || path === '/api/wallet/activity') data = [];
        else if (path === '/api/notifications') data = { items: [{ id: 1, type: 'keysend', action: 'received', direction: 'in', status: 'SETTLED', amount_sat: 1, occurred_at: new Date().toISOString(), memo: 'Browser regression fixture', peer_alias: 'Fixture peer' }], has_more: false };
        else if (path === '/api/notifications/stream') return route.fulfill({ status: 200, contentType: 'text/event-stream', body: ': fixture\n\n' });
        await route.fulfill({ json: data });
      });
      await page.goto(`${process.env.LOS_UI_TEST_URL || 'http://127.0.0.1:5178'}/#notifications`);
      await page.locator('input[type=password]').fill('wrong');
      await page.getByRole('button', { name: language === 'en' ? 'Sign in' : 'Entrar', exact: true }).click();
      await page.getByText('Invalid fixture password').waitFor({ timeout: 5000 });
      await page.locator('input[type=password]').fill('fixture-only');
      await page.getByRole('button', { name: language === 'en' ? 'Sign in' : 'Entrar', exact: true }).click();
      await page.getByText('Fixture peer').first().waitFor({ timeout: 5000 });
      assert.equal(await page.locator('html').getAttribute('lang'), language);
      assert.equal(await page.evaluate(() => document.fonts.check('16px "Space Grotesk"')), true);
      const selects = page.locator('main select');
      for (let i = 0; i < await selects.count(); i++) {
        const options = await selects.nth(i).locator('option').evaluateAll(nodes => nodes.map(n => n.value));
        if (options.includes('keysend')) await selects.nth(i).selectOption('keysend');
      }
      await page.getByText('Fixture peer').first().waitFor();
      await page.setViewportSize({ width: 390, height: 844 });
      await page.getByText('Fixture peer').first().waitFor();
      assert.deepEqual(errors, []);
      console.log(JSON.stringify({ language, loginError: true, loginSuccess: true, notifications: true, keysendFilter: true, mobile: true, fonts: true, pageErrors: errors }));
      await context.close();
    }
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
