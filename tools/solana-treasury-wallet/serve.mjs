import { createServer } from 'node:http'
import { readFile } from 'node:fs/promises'
import { directPilot, readPilotJson } from './direct-pilot.mjs'

const pilotConfig = process.argv[2] ? await readPilotJson(process.argv[2]) : undefined
const pilot = pilotConfig ? directPilot(pilotConfig) : undefined
const port = pilotConfig?.parallel === true ? 4178 : pilot ? 4177 : 4176
const host = `127.0.0.1:${port}`

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
if (pilot) headers['Content-Security-Policy'] += "; connect-src 'self'"

createServer(async (request, response) => {
  if (pilot && request.headers.host === host && request.method === 'POST' &&
      request.headers.origin === `http://${host}` && request.headers['x-lightningos-pilot'] === '1' &&
      ['/pilot/quote', '/pilot/refresh', '/pilot/submit', '/pilot/status', '/pilot/next'].includes(request.url)) {
    for (const [name, value] of Object.entries(headers)) response.setHeader(name, value)
    response.setHeader('Content-Type', 'application/json; charset=utf-8')
    await pilot(request, response)
    return
  }
  const target = files.get(request.url)
  if (request.method !== 'GET' || request.headers.host !== host || !target) {
    response.writeHead(404, headers).end()
    return
  }
  try {
    const content = await readFile(new URL(target[0], import.meta.url))
    response.writeHead(200, { ...headers, 'Content-Type': target[1] }).end(content)
  } catch {
    response.writeHead(500, headers).end()
  }
}).listen(port, '127.0.0.1', () => process.stdout.write(`Carteira Solana em http://${host}/\n`))
