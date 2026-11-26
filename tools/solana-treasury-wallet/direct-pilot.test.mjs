import assert from 'node:assert/strict'
import { mkdtemp, readdir, rm, rmdir, unlink, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { Readable } from 'node:stream'
import test from 'node:test'
import { getBase58Decoder, getTransactionDecoder } from '@solana/kit'
import { directPilot, readPilotJson } from './direct-pilot.mjs'
import { createSolanaBackup, signDirectDeposit } from './wallet.mjs'

async function request(handler, url, body = {}) {
  let status
  let result
  const stream = Readable.from([Buffer.from(JSON.stringify(body))])
  stream.url = url
  const response = {
    writeHead(code) { status = code; return this },
    end(value) { result = JSON.parse(value) },
  }
  await handler(stream, response)
  return { status, result }
}

test('binds the chosen address to a fresh quote and records the exact signature before sending', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'los-direct-pilot-'))
  const apiKeyFile = join(dir, 'key')
  const submissionFile = join(dir, 'submission.json')
  try {
    await writeFile(apiKeyFile, 'synthetic key')
    const password = 'synthetic pilot password'
    const { address: wallet, backup } = await createSolanaBackup(password)
    const { address: deposit } = await createSolanaBackup(password)
    const { address: nextHash } = await createSolanaBackup(password)
    let prepared = 0
    const recipients = []
    const calls = []
    const handler = directPilot({
      worker: 'unused', apiKeyFile, submissionFile, wallet, recipient: 'pilot@example.com',
      quoteMicros: 1_000_000, maxUsdcMicros: 1_090_000, minSats: 1500, maxLamports: 3_000_000,
    }, {
      requestQuote: async (_worker, args) => {
        recipients.push(args.recipient)
        assert.equal(args.refund, wallet)
        return {
          bridge_id: 'synthetic-bridge', status_token: 'private-token', deposit,
          amount_in: 1_065_000, amount_out: 1680, expires_at: Math.floor(Date.now() / 1000) + 120,
        }
      },
      prepare: async order => ({
        ...order, version: 1, feeMode: 'wallet', cluster: 'mainnet', createDepositAccount: true,
        feeLamports: '5000', accountLamports: '1488440', lastValidBlockHeight: 100 + prepared,
        blockhash: prepared++ ? nextHash : deposit, expiresAt: Date.now() + 60_000,
      }),
      rpc: async (method, transaction, options) => {
        calls.push(method)
        if (method === 'simulateTransaction') {
          assert.equal(options.sigVerify, true)
          return { value: { err: null } }
        }
        assert.equal(method, 'sendTransaction')
        const decoded = getTransactionDecoder().decode(Buffer.from(transaction, 'base64'))
        const signature = getBase58Decoder().decode(decoded.signatures[wallet])
        const saved = await readPilotJson(submissionFile)
        assert.equal(saved.signature, signature)
        assert.equal(saved.submission, 'submitted_reconcile')
        assert.equal(saved.order.lightningAddress, 'Other@pay.example.com')
        return signature
      },
    })
    const first = await request(handler, '/pilot/quote')
    assert.equal(first.status, 200)
    assert.equal(first.result.lightningAddress, 'pilot@example.com')
    assert.equal(first.result.status_token, undefined)
    const oldSignature = await signDirectDeposit(backup, password, first.result)
    const updated = await request(handler, '/pilot/refresh', { id: first.result.id })
    assert.equal(updated.status, 200)
    assert.equal(updated.result.id, first.result.id)
    assert.equal(updated.result.deposit, first.result.deposit)
    assert.equal(updated.result.depositMicros, first.result.depositMicros)
    assert.notEqual(updated.result.blockhash, first.result.blockhash)
    assert.deepEqual(recipients, ['pilot@example.com'])
    assert.equal((await request(handler, '/pilot/submit', oldSignature)).result.error, 'invalid_signature_or_order')
    assert.deepEqual(calls, [])
    const invalid = await request(handler, '/pilot/quote', { recipient: 'https://example.com' })
    assert.equal(invalid.result.error, 'invalid_lightning_address')
    assert.deepEqual(recipients, ['pilot@example.com'])
    const alternate = await request(handler, '/pilot/quote', { recipient: ' Other@PAY.example.com ' })
    assert.equal(alternate.status, 200)
    assert.equal(alternate.result.lightningAddress, 'Other@pay.example.com')
    assert.notEqual(alternate.result.id, updated.result.id)
    assert.deepEqual(recipients, ['pilot@example.com', 'Other@pay.example.com'])
    assert.equal((await request(handler, '/pilot/refresh', { id: updated.result.id })).result.error, 'quote_expired')
    const oldDestinationSignature = await signDirectDeposit(backup, password, updated.result)
    assert.equal((await request(handler, '/pilot/submit', oldDestinationSignature)).result.error, 'invalid_signature_or_order')
    const signed = await signDirectDeposit(backup, password, alternate.result)
    assert.equal((await request(handler, '/pilot/submit', signed)).status, 200)
    assert.equal((await request(handler, '/pilot/submit', signed)).result.error, 'reconcile')
    assert.deepEqual(calls, ['simulateTransaction', 'sendTransaction'])
  } finally {
    await unlink(submissionFile).catch(error => { if (error.code !== 'ENOENT') throw error })
    await unlink(apiKeyFile)
    await rmdir(dir)
  }
})

test('resumes from a saved submission and distinguishes deposit, delivery and verified refund', async (t) => {
  const dir = await mkdtemp(join(tmpdir(), 'los-direct-status-'))
  const apiKeyFile = join(dir, 'key')
  const submissionFile = join(dir, 'submission.json')
  let now = Date.now()
  t.mock.method(Date, 'now', () => now)
  try {
    await writeFile(apiKeyFile, 'synthetic key')
    const order = {
      lightningAddress: 'payee@example.com', cluster: 'mainnet', deposit: 'bridge-wallet',
      wallet: 'user-wallet', depositMicros: '1065000', expectedSats: '1680',
      feeLamports: '5000', accountLamports: '1488440',
    }
    let depositFinal = false
    let refundFinal = false
    let bridge = { status: 'pending', amount_in: 1065000, deposit_ref: 'source-signature' }
    const deps = {
      rpc: async (method) => {
        assert.equal(method, 'getSignatureStatuses')
        return { value: [null] }
      },
      requestStatus: async (_worker, args) => {
        if (args.action === 'solana-proof') {
          assert.equal(args.reference, 'source-signature')
          assert.equal(args.recipient, order.deposit)
          if (!depositFinal) throw new Error('proof pending')
          return { status: 'verified', delivery_ref: args.reference, amount_out: 1065000 }
        }
        if (args.action === 'solana-refund-proof') {
          assert.equal(args.recipient, order.wallet)
          assert.equal(args.reference, 'refund-signature')
          if (!refundFinal) throw new Error('proof pending')
          return { status: 'verified', delivery_ref: args.reference, amount_out: 1064000 }
        }
        assert.equal(args.action, 'status')
        return bridge
      },
    }
    const config = { worker: 'unused', apiKeyFile, submissionFile, wallet: order.wallet }
    assert.equal((await request(directPilot(config, deps), '/pilot/status')).result.state, 'idle')
    await writeFile(submissionFile, JSON.stringify({
      order, quote: { bridge_id: 'synthetic-order', status_token: 'private-token' }, signature: 'source-signature',
    }))
    const handler = directPilot(config, deps)
    const read = async (recipient = order.lightningAddress) => {
      now += 9_000
      const { status, result } = await request(handler, '/pilot/status')
      assert.equal(status, 200)
      assert.equal(result.lightningAddress, recipient)
      assert(!JSON.stringify(result).includes('private-token'))
      return result
    }
    assert.equal((await read()).state, 'deposit_pending')
    depositFinal = true
    assert.equal((await read()).state, 'delivery_pending')
    assert.equal((await request(handler, '/pilot/quote')).result.error, 'reconcile')
    assert.equal((await request(handler, '/pilot/next')).status, 409)
    assert.equal((await readdir(dir)).length, 2)
    bridge = { ...bridge, status: 'completed', delivery_ref: 'delivery-id', amount_out: 1681, deposit_ref: 'unrelated' }
    assert.equal((await read()).state, 'delivery_pending')
    bridge.deposit_ref = 'source-signature'
    assert.equal((await read()).deliveredSats, 1681)
    assert.equal((await read()).state, 'delivered')
    const next = await request(handler, '/pilot/next')
    assert.deepEqual(next, { status: 200, result: { state: 'idle', savedRecipient: order.lightningAddress } })
    const files = await readdir(dir)
    const archive = files.find(name => name.endsWith('.archive.json'))
    assert(archive)
    assert.equal((await readPilotJson(join(dir, archive))).signature, 'source-signature')
    assert.equal((await request(directPilot(config, deps), '/pilot/status')).result.savedRecipient, order.lightningAddress)
    assert.equal((await request(directPilot({ ...config, wallet: 'another-wallet' }, deps), '/pilot/status')).result.savedRecipient, undefined)
    const refundOrder = { ...order, lightningAddress: 'different@example.com' }
    await writeFile(submissionFile, JSON.stringify({
      order: refundOrder, quote: { bridge_id: 'refund-order', status_token: 'private-token' }, signature: 'source-signature',
    }))
    bridge = { ...bridge, status: 'refunded', refund_ref: 'refund-signature' }
    assert.equal((await read(refundOrder.lightningAddress)).state, 'refund_pending')
    assert.equal((await request(handler, '/pilot/next')).status, 409)
    refundFinal = true
    const refund = await read(refundOrder.lightningAddress)
    assert.equal(refund.state, 'refunded')
    assert.equal(refund.refundedMicros, '1064000')
    assert.equal((await request(handler, '/pilot/next')).result.savedRecipient, order.lightningAddress)
  } finally {
    for (const name of await readdir(dir)) await rm(join(dir, name))
    await rmdir(dir)
  }
})
