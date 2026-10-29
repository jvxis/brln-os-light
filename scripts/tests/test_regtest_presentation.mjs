// npm ci in lightningos-light/ui first; no browser/global test framework needed.
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { readFileSync } from 'node:fs'
const ui = new URL('../../lightningos-light/ui/', import.meta.url)
const require = createRequire(new URL('package.json', ui))
const ts = require('typescript')
const load = async name => {
  const source = readFileSync(new URL(`src/utils/${name}.ts`, ui), 'utf8')
  const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText
  return import('data:text/javascript;base64,' + Buffer.from(compiled).toString('base64'))
}
const { isRegtestLab } = await load('labProfile')
assert.equal(isRegtestLab(), false)
globalThis.document = { getElementById: () => null }
assert.equal(isRegtestLab(), false, 'ordinary mainnet HTML must retain mainnet labels')
globalThis.document = { getElementById: id => id === 'los-regtest-notice' ? {} : null }
assert.equal(isRegtestLab(), true)
delete globalThis.document

const { financialOperationLabel: label } = await load('financialOperationLabel')
assert.equal(label('lnops/channel/close', undefined, false), 'Cooperatively close channel')
assert.equal(label('lnops/channel/close', 'existing_commitment', false), 'Force-close channel')
assert.equal(label('lnops/channel/close', undefined, true), 'Fechar canal cooperativamente')
assert.equal(label('lnops/channel/close', 'existing_commitment', true), 'Forçar fechamento de canal')
assert.equal(label('wallet/pay', undefined, false), 'Lightning payment')
assert.equal(label('wallet/pay', undefined, true), 'Pagamento Lightning')
assert.equal(label('wallet/pay/mpp', undefined, false), 'Lightning MPP payment')
assert.equal(label('onchain_send', undefined, false), 'On-chain transfer')
assert.equal(label('unknown/operation', undefined, false), 'unknown/operation')

const { manualRebalanceReason: reason } = await load('manualRebalanceReason')
const channel = { eligible_as_manual_target: false, active: true, automation_mode: 'normal', target_outbound_pct: 60, local_pct: 40, peer_policy_known: true }
assert.equal(reason(channel, 5), 'budget')
assert.equal(reason({ ...channel, eligible_as_manual_target: true }, 5), '')
assert.equal(reason({ ...channel, active: false }, 5), 'inactive')
assert.equal(reason({ ...channel, automation_mode: 'parked' }, 5), 'parked')
assert.equal(reason({ ...channel, local_pct: 55 }, 5), 'target')
assert.equal(reason({ ...channel, peer_policy_known: false }, 5), 'policy')
console.log('PASS regtest/mainnet presentation, bilingual financial labels and manual eligibility explanations.')
