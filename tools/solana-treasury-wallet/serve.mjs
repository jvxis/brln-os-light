import { createServer } from 'node:http'
import { readFile } from 'node:fs/promises'

const files = new Map([
  ['/', ['index.html', 'text/html; charset=utf-8']],
  ['/wallet.css', ['wallet.css', 'text/css; charset=utf-8']],
  ['/page.js', ['dist/page.js', 'text/javascript; charset=utf-8']],
])
const headers = {
  'Cache-Control': 'no-store',
  'Content-Security-Policy': "default-src 'none'; script-src 'self'; style-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'",
  'X-Content-Type-Options': 'nosniff',
  'X-Frame-Options': 'DENY',
  'Referrer-Policy': 'no-referrer'
}

createServer(async (request, response) => {
  const target = files.get(request.url)
  if (request.method !== 'GET' || request.headers.host !== '127.0.0.1:4176' || !target) {
    response.writeHead(404, headers).end()
    return
  }
  try {
    const content = await readFile(new URL(target[0], import.meta.url))
    response.writeHead(200, { ...headers, 'Content-Type': target[1] }).end(content)
  } catch {
    response.writeHead(500, headers).end()
  }
}).listen(4176, '127.0.0.1', () => process.stdout.write('Carteira Solana em http://127.0.0.1:4176/\n'))
