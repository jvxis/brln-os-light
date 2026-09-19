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
    for (const width of [390, 1440]) {
      const page = await browser.newPage({ viewport: { width, height: 1000 } })
      const errors = []
      page.on('pageerror', (error) => errors.push(error.message))
      await page.addInitScript((lang) => localStorage.setItem('los-lang', lang), language)
      await page.goto('http://127.0.0.1:5178/tests/rebalance-smoke.html')
      const controls = page.getByTestId('controls')
      await controls.locator('summary').last().click()
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
      console.log(`PASS ${language} ${width}px: render, keyboard disclosure, mutually exclusive controls, no viewport overflow`)
      await page.close()
    }
  }
} finally { await browser.close() }
