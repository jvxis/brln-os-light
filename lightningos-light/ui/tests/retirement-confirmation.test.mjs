// Exercises the exported API helper and real financial-intent transport.
// npm ci, then: node --test tests/retirement-confirmation.test.mjs
import assert from 'node:assert/strict'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { mkdtempSync, writeFileSync, unlinkSync, rmdirSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import test, { after } from 'node:test'
import { build } from 'esbuild'

const root = fileURLToPath(new URL('..', import.meta.url))
const bundle = await build({
  stdin: { contents: `export { confirmNodeRetirementCoopClose } from './src/api.ts'; export { subscribeFinancialConfirmation } from './src/financialIntents.ts';`, resolveDir: root, loader: 'ts' },
  bundle: true, write: false, format: 'esm', platform: 'browser', target: 'es2022'
})
const moduleDirectory = mkdtempSync(join(tmpdir(), 'los-retirement-confirm-'))
const moduleFile = join(moduleDirectory, 'api.mjs')
writeFileSync(moduleFile, bundle.outputFiles[0].text, { mode: 0o600 })
after(() => { unlinkSync(moduleFile); rmdirSync(moduleDirectory) })

for (const [name, payload] of [['omitted payload', undefined], ['explicit payload', { confirm_password: 'fixture-only-password' }]]) {
  test(`retirement cooperative confirmation prepares a financial snapshot with ${name}`, async t => {
    const original = { fetch: globalThis.fetch, sessionStorage: globalThis.sessionStorage, window: globalThis.window }
    const storage = new Map()
    globalThis.sessionStorage = { getItem: key => storage.get(key) || null, setItem: (key, value) => storage.set(key, value) }
    globalThis.window = { dispatchEvent() {} }
    t.after(() => Object.assign(globalThis, original))
    const path = '/api/lnops/node-retirement/sessions/test-session/confirm-coop'
    const id = 'a'.repeat(32)
    const normalized = { approved_session_policy: 2 }
    let probes = 0, prepared = 0, confirmations = 0, submitted = 0
    const response = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
    globalThis.fetch = async (url, options) => {
      assert.equal(options.method, 'POST')
      if (url === '/api/financial/intents') {
        prepared++
        const request = JSON.parse(options.body)
        assert.deepEqual(request, { method: 'POST', path, payload: {} }, 'passwords must not enter the persisted financial snapshot')
        return response({ required: true, intent_id: id, scope: 'fixture-snapshot', path, method: 'POST', payload: normalized, summary: { operation: 'retirement_coop_confirm' } }, 201)
      }
      assert.equal(url, path)
      if (!new Headers(options.headers).has('X-LOS-Financial-Intent')) {
        probes++
        return response({ error: 'Snapshot confirmation required', requires_financial_intent: true }, 428)
      }
      assert.equal(new Headers(options.headers).get('X-LOS-Financial-Intent'), id)
      assert.deepEqual(JSON.parse(options.body), normalized, 'only the approved server snapshot may execute')
      submitted++
      return response({ ok: true })
    }
    const module = await import(pathToFileURL(moduleFile).href + '#' + encodeURIComponent(name))
    const unsubscribe = module.subscribeFinancialConfirmation(task => { if (task) { confirmations++; task.approve() } })
    t.after(unsubscribe)
    assert.deepEqual(await module.confirmNodeRetirementCoopClose('test-session', payload), { ok: true })
    assert.deepEqual({ probes, prepared, confirmations, submitted }, { probes: 1, prepared: 1, confirmations: 1, submitted: 1 })
  })
}
