import { createHash } from 'node:crypto'
import { readFile, writeFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { build } from 'esbuild'

const pilot = new URL('../../../tools/stablecoin-treasury-pilot/', import.meta.url)
const source = await readFile(new URL('index.html', pilot), 'utf8')
const result = await build({
  entryPoints: [fileURLToPath(new URL('src/main.js', pilot))],
  nodePaths: [fileURLToPath(new URL('../node_modules', import.meta.url))],
  bundle: true,
  minify: true,
  format: 'iife',
  outfile: 'treasury-signer.js',
  write: false,
  legalComments: 'inline',
})
const program = result.outputFiles.find(file => file.path.endsWith('.js')).text.replace(/<\/script/gi, '<\\/script')
const styles = result.outputFiles.find(file => file.path.endsWith('.css')).text.replace(/<\/style/gi, '<\\/style')
const hash = value => createHash('sha256').update(value).digest('base64')
const policy = `default-src 'none'; script-src 'sha256-${hash(program)}'; style-src 'sha256-${hash(styles)}'; base-uri 'none'; form-action 'none'`
const output = source
  .replace(/(<meta http-equiv="Content-Security-Policy" content=")[^"]+/, (_, start) => start + policy)
  .replace('<script type="module" src="./src/main.js"></script>', () => `<style>${styles}</style>`)
  .replace('</body>', () => `<script>${program}</script></body>`)
await writeFile(new URL('../dist/treasury-signer.html', import.meta.url), output)
