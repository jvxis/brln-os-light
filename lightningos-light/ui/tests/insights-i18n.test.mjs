import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const load = (file) => JSON.parse(readFileSync(new URL(`../src/i18n/${file}`, import.meta.url), 'utf8'))

const keys = (value, prefix = '') =>
  Object.entries(value)
    .flatMap(([key, entry]) =>
      typeof entry === 'object' ? keys(entry, `${prefix}${key}.`) : (assert.ok(String(entry).trim(), `${prefix}${key} is empty`), `${prefix}${key}`)
    )
    .sort()

test('English and Portuguese insights translations have matching nonempty keys', () => {
  const en = load('en.json')
  const pt = load('pt-BR.json')
  assert.deepEqual(keys(en.insights), keys(pt.insights))
  assert.ok(en.nav.insights.trim())
  assert.ok(pt.nav.insights.trim())
})

test('every backend code the Insights page renders has a translation', () => {
  const en = load('en.json').insights
  const kinds = ['repor_vendedor', 'mais_capacidade', 'parar_compra', 'capital_parado', 'abrir_canal', 'preco_fora', 'bloqueio_esperado']
  const tools = ['rebalance_target', 'loop_in', 'econ_override', 'open_channel', 'close_channel', 'autofee_review', 'use_as_source', 'none']
  const guards = ['not_parked', 'magma_commitment', 'sell_covers_floor', 'loop_installed', 'parallel_channel_leaves_fast_path', 'we_are_funder']
  const invalidate = ['local_at_target_24h', 'guard_released', 'turnover_below_2x', 'lot_sold_50pct', 'sales_30d_positive', 'channel_opened_or_candidate_gone', 'fee_within_band']
  const evidence = ['local_pct', 'sell_ppm_7d', 'rebal_cost_ppm_7d', 'fee_7d_sat', 'intent_age_h', 'jobs_24h', 'autopilot_reason', 'turnover_7d', 'capacity_sat', 'hours_to_drain', 'lot_sold_pct', 'rebal_fee_7d_sat', 'local_sat', 'fee_30d_sat', 'assisted_fee_30d_sat', 'rebal_fee_30d_sat', 'route_hits_30d', 'route_volume_30d_sat', 'candidate_score', 'route_cost_ppm_30d', 'fee_ppm', 'floor_ppm']
  for (const kind of kinds) assert.ok(en.kind[kind]?.title, `kind ${kind}`)
  for (const tool of tools) assert.ok(en.tool[tool]?.step, `tool ${tool}`)
  for (const guard of guards) assert.ok(en.guard[guard], `guard ${guard}`)
  for (const code of invalidate) assert.ok(en.invalidate[code], `invalidate ${code}`)
  for (const key of evidence) assert.ok(en.evidence[key], `evidence ${key}`)
  for (const rule of ['R1', 'R2', 'R3', 'R4', 'R5', 'R6', 'R7']) assert.ok(en.glossary.rules[rule]?.name, `rule ${rule}`)
  for (const verdict of ['pending', 'hit', 'neutral', 'miss', 'confirmed', 'unneeded']) assert.ok(en.outcomes.verdict[verdict], `verdict ${verdict}`)
})
