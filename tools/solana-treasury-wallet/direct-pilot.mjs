import { spawn } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import { readFile, writeFile } from 'node:fs/promises'
import { getBase58Decoder, getTransactionDecoder } from '@solana/kit'
import { directOrderRpc, prepareDirectOrder } from './prepare-direct-order.mjs'
import { verifyDirectDeposit } from './sponsored.mjs'

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

async function ensureUnsubmitted(path) {
  try {
    await readFile(path)
  } catch (error) {
    if (error.code === 'ENOENT') return
    throw error
  }
  throw new Error('A previous submission needs reconciliation')
}

async function signedBody(request) {
  let length = 0
  const chunks = []
  for await (const chunk of request) {
    length += chunk.length
    if (length > 4096) throw new Error('Signed transaction exceeds expected size')
    chunks.push(chunk)
  }
  return JSON.parse(Buffer.concat(chunks).toString('utf8'))
}

export function directPilot(config) {
  let active
  let busy = false

  return async (request, response) => {
    if (busy) {
      response.writeHead(409).end()
      return
    }
    busy = true
    const quoting = request.url === '/pilot/quote'
    try {
      await ensureUnsubmitted(config.submissionFile)
      if (quoting) {
        if (!active || active.order.expiresAt <= Date.now() + 15_000) {
          const apiKey = (await readFile(config.apiKeyFile, 'utf8')).trim()
          const quote = await workerRequest(config.worker, {
            action: 'quote', api_key: apiKey, direction: 'to_lightning',
            recipient: config.recipient, refund: config.wallet, amount: config.quoteMicros,
          })
          if (!quote.bridge_id || !quote.status_token || !quote.deposit ||
              !Number.isSafeInteger(quote.amount_in) || quote.amount_in <= 0 || quote.amount_in > config.maxUsdcMicros ||
              !Number.isSafeInteger(quote.amount_out) || quote.amount_out < config.minSats) {
            throw new Error('Quote outside pilot bounds')
          }
          const order = await prepareDirectOrder({
            id: randomUUID().replaceAll('-', ''), wallet: config.wallet, deposit: quote.deposit,
            depositMicros: String(quote.amount_in), expectedSats: String(quote.amount_out),
            providerFeeMicros: quote.provider_fee?.asset === 'USDC' ? String(quote.provider_fee.amount) : undefined,
            lightningAddress: config.recipient, expiresAt: quote.expires_at * 1000,
          })
          if (Number(order.feeLamports) + Number(order.accountLamports) > config.maxLamports) {
            throw new Error('Network cost outside pilot bounds')
          }
          active = { order, quote }
        }
        response.writeHead(200).end(JSON.stringify(active.order))
        return
      }
      if (!active) throw new Error('No active quote')
      const signed = await signedBody(request)
      const transaction = await verifyDirectDeposit(active.order, signed)
      const decoded = getTransactionDecoder().decode(Buffer.from(transaction, 'base64'))
      const signature = getBase58Decoder().decode(decoded.signatures[active.order.wallet])
      const simulation = await directOrderRpc('simulateTransaction', transaction, {
        encoding: 'base64', sigVerify: true, commitment: 'confirmed',
      })
      if (simulation.value?.err !== null || active.order.expiresAt <= Date.now()) {
        throw new Error('Unable to simulate the current transaction')
      }
      await writeFile(config.submissionFile, JSON.stringify({
        ...active, signature, submission: 'submitted_reconcile',
      }), { flag: 'wx' })
      const sent = await directOrderRpc('sendTransaction', transaction, {
        encoding: 'base64', skipPreflight: false, preflightCommitment: 'confirmed', maxRetries: 3,
      })
      if (sent !== signature) throw new Error('Submission requires reconciliation')
      response.writeHead(200).end(JSON.stringify({ signature }))
    } catch {
      response.writeHead(409).end()
    } finally {
      busy = false
    }
  }
}
