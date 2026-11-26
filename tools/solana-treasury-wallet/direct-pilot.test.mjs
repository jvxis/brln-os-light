import assert from 'node:assert/strict'
import { mkdtemp, rmdir, unlink, writeFile } from 'node:fs/promises'
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
