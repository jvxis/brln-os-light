import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { rebalanceEligibility } from '../src/components/rebalance/eligibility.ts'

const config = { auto_enabled: true, scheduler_mode: 'rules_auto', manual_restart_watch: true, manual_restart_ignore_economic_gates: false, sovereign_candidate_scope: 'auto_and_manual_restart' }
const channel = { auto_enabled: true, manual_restart_enabled: false, auto_bypass_cost_gate: false, eligible_as_manual_target: true, eligible_as_target: false, excluded_as_source: false }

test('economic failure, operator exclusion and automation off are independent', () => {
  const result = rebalanceEligibility({ ...channel, excluded_as_source: true }, { ...config, auto_enabled: false })
  assert.equal(result.automation, 'globalOff')
  assert.equal(result.costBlocked, true)
  assert.equal(result.sourceExcluded, true)
})

test('general target ineligibility is not falsely labelled as cost gate', () => {
  assert.equal(rebalanceEligibility({ ...channel, eligible_as_manual_target: false }, config).costBlocked, false)
  assert.equal(rebalanceEligibility({ ...channel, eligible_as_target: true }, config).costBlocked, false)
})

test('bypass clears only the cost label, never enables global or channel automation', () => {
  const result = rebalanceEligibility({ ...channel, auto_enabled: false, auto_bypass_cost_gate: true }, config)
  assert.equal(result.costBlocked, false)
  assert.equal(result.automation, 'channelOff')
})

test('parked state takes precedence and source exclusion does not disable a target', () => {
  assert.equal(rebalanceEligibility({ ...channel, automation_mode: 'parked' }, config).automation, 'parked')
  assert.equal(rebalanceEligibility({ ...channel, excluded_as_source: true }, config).automation, 'auto')
})

test('manual restart respects master switch, watcher and conviction', () => {
  const restart = { ...channel, auto_enabled: false, manual_restart_enabled: true }
  assert.equal(rebalanceEligibility(restart, { ...config, auto_enabled: false }).automation, 'globalOff')
  assert.equal(rebalanceEligibility(restart, { ...config, manual_restart_watch: false }).automation, 'restartOff')
  const conviction = rebalanceEligibility(restart, { ...config, manual_restart_ignore_economic_gates: true })
  assert.equal(conviction.automation, 'restart')
  assert.equal(conviction.conviction, true)
  assert.equal(conviction.costBlocked, false)
})

test('sovereign scope replaces the classic watcher and does not inherit conviction bypass', () => {
  const restart = { ...channel, auto_enabled: false, manual_restart_enabled: true }
  const sovereign = { ...config, scheduler_mode: 'sovereign_live', manual_restart_watch: false, manual_restart_ignore_economic_gates: true }
  const result = rebalanceEligibility(restart, sovereign)
  assert.equal(result.automation, 'restart')
  assert.equal(result.conviction, false)
  assert.equal(result.costBlocked, true)
  assert.equal(rebalanceEligibility(restart, { ...sovereign, sovereign_candidate_scope: 'auto_only' }).automation, 'scopeExcluded')
  assert.equal(rebalanceEligibility(restart, { ...sovereign, scheduler_mode: 'sovereign_shadow' }).automation, 'shadow')
})

test('guaranteed pool is not incorrectly shown as all automation disabled', () => {
  assert.equal(rebalanceEligibility({ ...channel, auto_enabled: false, guaranteed_rebalance_enabled: true }, config).automation, 'guaranteed')
})

test('English and Portuguese eligibility translations have matching nonempty keys', () => {
  const keys = (value, prefix = '') => Object.entries(value).flatMap(([key, entry]) => typeof entry === 'object' ? keys(entry, `${prefix}${key}.`) : (assert.ok(entry.trim()), `${prefix}${key}`)).sort()
  const en = JSON.parse(readFileSync(new URL('../src/i18n/en.json', import.meta.url), 'utf8')).rebalanceEligibility
  const pt = JSON.parse(readFileSync(new URL('../src/i18n/pt-BR.json', import.meta.url), 'utf8')).rebalanceEligibility
  assert.deepEqual(keys(en), keys(pt))
})
