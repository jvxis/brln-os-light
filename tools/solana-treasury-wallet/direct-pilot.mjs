import { spawn } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import { readFile, writeFile } from 'node:fs/promises'
import { getBase58Decoder, getTransactionDecoder } from '@solana/kit'
import { directOrderRpc, prepareDirectOrder } from './prepare-direct-order.mjs'
import { normalizeLightningAddress, verifyDirectDeposit } from './sponsored.mjs'

export async function readPilotJson(path) {
  return JSON.parse((await readFile(path, 'utf8')).replace(/^\uFEFF/, ''))
}

function workerRequest(worker, request) {
  return new Promise((resolve, reject) => {
    const child = spawn(worker, ['--bridge-worker'], {
      windowsHide: true, stdio: ['pipe', 'pipe', 'ignore'], timeout: 15_000,
    })
    let result = ''
    child.stdout.setEncoding('utf8')
    child.stdout.on('data', chunk => { result += chunk })
    child.on('error', reject)
    child.stdin.on('error', reject)
    child.on('close', code => {
      if (code !== 0) return reject(new Error('Provider quote unavailable'))
      try { resolve(JSON.parse(result)) } catch (error) { reject(error) }
    })
    child.stdin.end(JSON.stringify(request))
  })
}

async function priorSubmission(path) {
  try {
    return await readPilotJson(path)
  } catch (error) {
    if (error.code === 'ENOENT') return
    throw error
  }
}

async function requestBody(request) {
  let length = 0
  const chunks = []
  for await (const chunk of request) {
    length += chunk.length
    if (length > 4096) throw new Error('Pilot request exceeds expected size')
    chunks.push(chunk)
  }
  return JSON.parse(Buffer.concat(chunks).toString('utf8'))
}

function reply(response, status, body) {
  response.writeHead(status).end(JSON.stringify(body))
}

export function directPilot(config, {
  rpc = directOrderRpc, requestQuote = workerRequest, prepare = prepareDirectOrder,
} = {}) {
  let active
  let busy = false

  return async (request, response) => {
    if (busy) {
      reply(response, 409, { error: 'busy' })
      return
    }
    busy = true
    let phase = 'quote_unavailable'
    try {
      phase = 'reconcile'
      if (await priorSubmission(config.submissionFile)) {
        reply(response, 409, { error: phase })
        return
      }
      if (request.url === '/pilot/quote') {
        phase = 'invalid_lightning_address'
        const body = await requestBody(request)
        const recipient = normalizeLightningAddress(Object.hasOwn(body, 'recipient') ? body.recipient : config.recipient)
        phase = 'quote_unavailable'
        if (active && active.order.lightningAddress !== recipient) active = undefined
        if (!active || active.quote.expires_at * 1000 <= Date.now() + 15_000) {
          const apiKey = (await readFile(config.apiKeyFile, 'utf8')).trim()
          const quote = await requestQuote(config.worker, {
            action: 'quote', api_key: apiKey, direction: 'to_lightning',
            recipient, refund: config.wallet, amount: config.quoteMicros,
          })
          if (!quote.bridge_id || !quote.status_token || !quote.deposit ||
              !Number.isSafeInteger(quote.amount_in) || quote.amount_in <= 0 || quote.amount_in > config.maxUsdcMicros ||
              !Number.isSafeInteger(quote.amount_out) || quote.amount_out < config.minSats) {
            throw new Error('Quote outside pilot bounds')
          }
          const order = await prepareOrder(config, quote, randomUUID().replaceAll('-', ''), recipient, prepare)
          active = { order, quote }
        } else if (active.order.expiresAt <= Date.now() + 15_000) {
          active.order = await prepareOrder(config, active.quote, active.order.id, recipient, prepare)
        }
        reply(response, 200, active.order)
        return
      }
      if (request.url === '/pilot/refresh') {
        phase = 'quote_expired'
        const { id } = await requestBody(request)
        if (!active || active.order.id !== id || active.quote.expires_at * 1000 <= Date.now() + 10_000) {
          reply(response, 409, { error: phase })
          return
        }
        phase = 'refresh_unavailable'
        active.order = await prepareOrder(config, active.quote, id, active.order.lightningAddress, prepare)
        reply(response, 200, active.order)
        return
      }
      phase = 'invalid_signature_or_order'
      if (!active) throw new Error('No active quote')
      const signed = await requestBody(request)
      if (active.order.expiresAt <= Date.now()) {
        reply(response, 409, { error: 'quote_expired' })
        return
      }
      const transaction = await verifyDirectDeposit(active.order, signed)
      const decoded = getTransactionDecoder().decode(Buffer.from(transaction, 'base64'))
      const signature = getBase58Decoder().decode(decoded.signatures[active.order.wallet])
      phase = 'simulation_unavailable'
      const simulation = await rpc('simulateTransaction', transaction, {
        encoding: 'base64', sigVerify: true, commitment: 'confirmed',
      })
      if (simulation.value?.err !== null) {
        reply(response, 409, { error: 'simulation_rejected' })
        return
      }
      if (active.order.expiresAt <= Date.now()) {
        reply(response, 409, { error: 'quote_expired' })
        return
      }
      phase = 'reconcile'
      await writeFile(config.submissionFile, JSON.stringify({
        ...active, signature, submission: 'submitted_reconcile',
      }), { flag: 'wx' })
      const sent = await rpc('sendTransaction', transaction, {
        encoding: 'base64', skipPreflight: false, preflightCommitment: 'confirmed', maxRetries: 3,
      })
      if (sent !== signature) throw new Error('Submission requires reconciliation')
      reply(response, 200, { signature })
    } catch {
      process.stderr.write(`Pilot request failed at ${phase}\n`)
      reply(response, 409, { error: phase })
    } finally {
      busy = false
    }
  }
}

async function prepareOrder(config, quote, id, recipient, prepare) {
  const order = await prepare({
    id, wallet: config.wallet, deposit: quote.deposit,
    depositMicros: String(quote.amount_in), expectedSats: String(quote.amount_out),
    providerFeeMicros: quote.provider_fee?.asset === 'USDC' ? String(quote.provider_fee.amount) : undefined,
    lightningAddress: recipient, expiresAt: quote.expires_at * 1000,
  })
  if (Number(order.feeLamports) + Number(order.accountLamports) > config.maxLamports) {
    throw new Error('Network cost outside pilot bounds')
  }
  return { ...order, quoteExpiresAt: quote.expires_at * 1000 }
}
