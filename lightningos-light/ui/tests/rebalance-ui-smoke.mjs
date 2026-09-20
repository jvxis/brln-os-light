// Start Vite on localhost:5178 first. Uses an installed Playwright package;
// PLAYWRIGHT_MODULE may point to its index.mjs without adding an app dependency.
import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import { pathToFileURL } from 'node:url'
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE ? pathToFileURL(process.env.PLAYWRIGHT_MODULE).href : 'playwright')
const browser = await chromium.launch({ channel: 'chrome', headless: true })
const output = process.env.SMOKE_OUTPUT || 'test-results'
await mkdir(output, { recursive: true })
try {
  for (const language of ['en', 'pt-BR']) {
    for (const width of [320, 390, 768, 1440, 1920]) {
      const page = await browser.newPage({ viewport: { width, height: 1000 } })
      const errors = []
      page.on('pageerror', (error) => errors.push(error.message))
      await page.addInitScript((lang) => localStorage.setItem('los-lang', lang), language)
      await page.goto('http://127.0.0.1:5178/tests/rebalance-smoke.html')
      const controls = page.getByTestId('controls')
      await controls.locator('summary').last().waitFor()
      const panel = controls.locator('details').last()
      const measure = () => panel.evaluate((element) => ({
        width: element.getBoundingClientRect().width,
        available: element.parentElement.getBoundingClientRect().width,
        fixed: 20 * parseFloat(getComputedStyle(document.documentElement).fontSize),
        overflow: element.scrollWidth > element.clientWidth
      }))
      const collapsed = await measure()
      assert.ok(Math.abs(collapsed.width - Math.min(collapsed.fixed, collapsed.available)) <= 1,
        `${language} ${width}: control must stay at 20rem or fit its container, got ${collapsed.width}px`)
      await controls.locator('summary').last().click()
      const expanded = await measure()
      assert.ok(Math.abs(expanded.width - collapsed.width) <= 1, 'opening controls must not change their width')
      assert.equal(expanded.overflow, false, 'expanded content must fit the panel')
      const restart = controls.getByRole('checkbox', { name: 'Manual Restart', exact: true })
      await restart.check()
      assert.equal(await controls.getByRole('checkbox').first().isChecked(), false)
      await controls.locator('summary').first().focus()
      await page.keyboard.press('Enter')
      await page.getByTestId('unknown').waitFor()
      assert.ok(await controls.locator('details').first().getAttribute('open') !== null)
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)
      assert.equal(overflow, false, `${language} ${width}: document overflow`)
      assert.deepEqual(errors, [])
      assert.equal(await page.locator('body').innerText().then((text) => text.includes('rebalanceEligibility.')), false)
      await page.screenshot({ path: `${output}/rebalance-${language}-${width}.png`, fullPage: true })
      // A narrow action cell must also work at desktop breakpoints.
      await controls.evaluate((element) => { element.style.width = '240px' })
      const narrow = await measure()
      assert.ok(narrow.width <= 214, 'control must shrink with a narrow padded container')
      assert.equal(narrow.overflow, false)
      console.log(`PASS ${language} ${width}px: fixed/responsive width, render, keyboard disclosure, mutually exclusive controls, no viewport overflow`)
      await page.close()
    }
  }
} finally { await browser.close() }
