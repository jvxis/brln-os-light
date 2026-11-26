import { spawn } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import { readFile, rename, unlink, writeFile } from 'node:fs/promises'
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

const terminalStates = new Set(['delivered', 'refunded', 'deposit_failed'])

async function saveRecipient(path, wallet, lightningAddress) {
  const temporary = path + '.' + randomUUID()
  await writeFile(temporary, JSON.stringify({ wallet, lightningAddress }), { flag: 'wx', mode: 0o600 })
  try {
    await rename(temporary, path)
  } finally {
    await unlink(temporary).catch(error => { if (error.code !== 'ENOENT') throw error })
  }
}

function reply(response, status, body) {
  response.writeHead(status).end(JSON.stringify(body))
}

export function directPilot(config, {
  rpc = directOrderRpc, requestQuote = workerRequest, prepare = prepareDirectOrder,
  requestStatus = workerRequest,
} = {}) {
  let active
  let busy = false
  let latestStatus
  let verifiedDeposit
  const recipientFile = config.recipientFile ?? config.submissionFile + '.recipient.json'

  async function savedRecipient() {
    const saved = await priorSubmission(recipientFile)
    if (saved?.wallet && saved.wallet === config.wallet) return normalizeLightningAddress(saved.lightningAddress)
  }

  function currentStatus(saved) {
    if (latestStatus?.signature !== saved.signature || latestStatus.checkedAt + 8_000 <= Date.now()) {
      latestStatus = {
        signature: saved.signature, checkedAt: Date.now(),
        result: reconcile(saved),
      }
    }
    return latestStatus.result
  }

  return async (request, response) => {
    if (request.url === '/pilot/status') {
      try {
        await requestBody(request)
        const saved = await priorSubmission(config.submissionFile)
        if (!saved) {
          reply(response, 200, { state: 'idle', savedRecipient: await savedRecipient() })
          return
        }
        reply(response, 200, await currentStatus(saved))
      } catch {
        reply(response, 503, { error: 'status_unavailable' })
      }
      return
    }
    if (busy) {
      reply(response, 409, { error: 'busy' })
      return
    }
    busy = true
    let phase = 'quote_unavailable'
    try {
      phase = 'reconcile'
      const saved = await priorSubmission(config.submissionFile)
      if (request.url === '/pilot/next') {
        await requestBody(request)
        if (saved) {
          const current = await currentStatus(saved)
          if (!terminalStates.has(current.state)) {
            reply(response, 409, { error: phase })
            return
          }
          if (current.state === 'delivered' && saved.order.wallet === config.wallet && saved.order.cluster === 'mainnet') {
            await saveRecipient(recipientFile, config.wallet, current.lightningAddress)
          }
          await rename(config.submissionFile, config.submissionFile + '.' + randomUUID() + '.archive.json')
          active = undefined
          latestStatus = undefined
          verifiedDeposit = undefined
        }
        reply(response, 200, { state: 'idle', savedRecipient: await savedRecipient() })
        return
      }
      if (saved) {
        reply(response, 409, { error: phase })
        return
      }
      if (request.url === '/pilot/quote') {
        phase = 'invalid_lightning_address'
        const body = await requestBody(request)
        const recipient = normalizeLightningAddress(Object.hasOwn(body, 'recipient') ? body.recipient : (await savedRecipient()) ?? config.recipient)
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
      }), { flag: 'wx', mode: 0o600 })
      const sent = await rpc('sendTransaction', transaction, {
        encoding: 'base64', skipPreflight: false, preflightCommitment: 'confirmed', maxRetries: 3,
      })
      if (sent !== signature) throw new Error('Submission requires reconciliation')
      reply(response, 200, { signature })
    } catch (error) {
      if (['insufficient_sol', 'insufficient_usdc'].includes(error.code)) phase = error.code
      process.stderr.write(`Pilot request failed at ${phase}\n`)
      reply(response, 409, { error: phase })
    } finally {
      busy = false
    }
  }

  async function reconcile(saved) {
    const { order, quote, signature } = saved
    const amount = Number(order.depositMicros)
    const status = {
      state: 'deposit_pending', lightningAddress: order.lightningAddress,
      depositMicros: order.depositMicros, expectedSats: order.expectedSats,
      feeLamports: order.feeLamports, accountLamports: order.accountLamports,
    }
    if (verifiedDeposit !== signature) {
      try {
        const proof = await requestStatus(config.worker, {
          action: 'solana-proof', cluster: order.cluster, recipient: order.deposit, reference: signature, amount,
        })
        if (proof.status === 'verified' && proof.amount_out === amount && proof.delivery_ref === signature) {
          verifiedDeposit = signature
        }
      } catch {
        // Until an exact credit is verified, the order stays in reconciliation.
      }
    }
    if (verifiedDeposit !== signature) {
      try {
        const { value: [tx] } = await rpc('getSignatureStatuses', [signature], { searchTransactionHistory: true })
        if (tx?.confirmationStatus === 'finalized' && tx.err != null) status.state = 'deposit_failed'
      } catch {
        status.checkDelayed = true
      }
      return status
    }
    status.state = 'delivery_pending'
    try {
      const apiKey = (await readFile(config.apiKeyFile, 'utf8')).trim()
      const bridge = await requestStatus(config.worker, {
        action: 'status', api_key: apiKey, bridge_id: quote.bridge_id, status_token: quote.status_token,
      })
      // A provider's terminal state must refer to this exact on-chain deposit.
      if (bridge.deposit_ref !== signature || bridge.amount_in !== amount) return status
      if (bridge.status === 'completed' && bridge.delivery_ref && Number.isSafeInteger(bridge.amount_out) && bridge.amount_out > 0) {
        status.state = 'delivered'
        status.deliveredSats = bridge.amount_out
      }
      if (bridge.status === 'refunding' || bridge.status === 'refunded') status.state = 'refund_pending'
      if (bridge.status === 'failed' || bridge.status === 'expired') status.state = 'delivery_issue'
      if (bridge.status === 'refunded' && bridge.refund_ref) {
        const proof = await requestStatus(config.worker, {
          action: 'solana-refund-proof', cluster: order.cluster, recipient: order.wallet,
          reference: bridge.refund_ref, amount,
        })
        if (proof.status === 'verified' && proof.delivery_ref === bridge.refund_ref &&
            Number.isSafeInteger(proof.amount_out) && proof.amount_out > 0 && proof.amount_out <= amount) {
          status.state = 'refunded'
          status.refundedMicros = String(proof.amount_out)
        }
      }
    } catch {
      status.checkDelayed = true
    }
    return status
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
