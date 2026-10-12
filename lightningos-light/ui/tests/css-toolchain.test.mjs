import assert from 'node:assert/strict'
import test from 'node:test'
import postcss from 'postcss'
import nested from 'postcss-nested'
import selectorParser from 'postcss-selector-parser'
import tailwind from 'tailwindcss'
import { SourceMapConsumer } from 'source-map-js'
import config from '../tailwind.config.js'

test('indexed source maps reject excessive offsets before downstream work', () => {
  const map = { version: 3, sections: [{ offset: { line: 1e12, column: 0 },
    map: { version: 3, sources: ['fixture.css'], names: [], mappings: 'AAAA' } }] }
  // Constructor-only check: safe to run on the vulnerable version too. Never
  // feed that version's accepted map into an unbounded generator operation.
  assert.throws(() => new SourceMapConsumer(map), /offset line must not exceed/)
})

test('regular CSS source maps still point to the input after nesting', async () => {
  const result = await postcss([nested()]).process('.card {\n  &:hover { color: red; }\n}',
    { from: 'fixture.css', to: 'compiled.css', map: { inline: false, annotation: false } })
  assert.match(result.css, /\.card:hover\s*\{\s*color: red;/)
  const consumer = new SourceMapConsumer(result.map.toJSON())
  const original = consumer.originalPositionFor({ line: 1, column: 0 })
  assert.equal(original.source, 'fixture.css')
  assert.ok(original.line >= 1 && original.line <= 3)
})

test('selector parsing preserves escaped utilities, attributes and pseudo selectors', () => {
  const text = '.hover\\:bg-ember\\/20:hover, [data-state="open"] > :is(.card, .panel)'
  const root = selectorParser().astSync(text)
  assert.equal(root.toString(), text)
  const classes = []
  root.walkClasses(node => classes.push(node.value))
  assert.deepEqual(classes, ['hover:bg-ember/20', 'card', 'panel'])
  assert.equal(root.nodes.length, 2)
})

test('Tailwind 3 and postcss-nested preserve shared UI variants and @apply', async () => {
  const classes = ['flex', 'hover:bg-ember/20', 'md:grid-cols-2',
    'data-[state=open]:block', '[&>span]:text-fog/70']
  const result = await postcss([nested(), tailwind({ ...config,
    content: [{ raw: `<div class="${classes.join(' ')}"></div>`, extension: 'html' }],
  })]).process('@tailwind utilities;\n.security-card { @apply p-4; &:hover { @apply flex; } }',
    { from: 'fixture.css' })
  const found = new Set()
  const rules = []
  result.root.walkRules(rule => {
    rules.push(rule)
    selectorParser(selectors => selectors.walkClasses(node => found.add(node.value))).processSync(rule.selector)
  })
  for (const name of classes) assert.ok(found.has(name), `missing utility ${name}`)
  assert.ok(rules.some(rule => rule.selector === '.security-card:hover'
    && rule.nodes.some(node => node.prop === 'display' && node.value === 'flex')))
  assert.ok(rules.some(rule => rule.selector === '.security-card'
    && rule.nodes.some(node => node.prop === 'padding' && node.value === '1rem')))
  const media = []
  result.root.walkAtRules('media', rule => media.push(rule.params))
  assert.ok(media.includes('(min-width: 768px)'))
  assert.equal(result.warnings().length, 0)
})
