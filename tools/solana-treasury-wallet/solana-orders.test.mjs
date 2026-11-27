import assert from 'node:assert/strict'
import test from 'node:test'
import { prepareDirectOrder } from './solana-orders.mjs'
import { tokenProgram, usdcMint } from './sponsored.mjs'

test('rebuilds a return quote when a Solana node does not recognize the first blockhash', async () => {
  const wallet = 'At4mSNudfMM87kRVbsWr2VYriLqAxJfz2xN19WpgnZ7m'
  const initialHash = wallet
  const finalHash = usdcMint
  const quote = {
    id: '7766554433221100aabbccddeeff0022', wallet, deposit: usdcMint,
    depositMicros: '1066151', providerFeeMicros: '21046', expectedSats: '1663',
    lightningAddress: 'jvx@example.com', expiresAt: Date.now() + 90_000,
  }
  const token = {
    owner: tokenProgram,
    data: { parsed: { info: { owner: wallet, mint: usdcMint, state: 'initialized',
      tokenAmount: { amount: '1391022', decimals: 6 } } } },
  }
  const feeChecks = []
  const order = await prepareDirectOrder(quote, async (method, ...params) => {
    if (method === 'getMultipleAccounts') return { value: [token, null] }
    if (method === 'getBalance') return { value: 11_039_360 }
    if (method === 'getMinimumBalanceForRentExemption') return params[0] === 0 ? 650_240 : 1_488_440
    if (method === 'getLatestBlockhash') {
      const first = params[0].commitment === 'confirmed'
      return { value: { blockhash: first ? initialHash : finalHash, lastValidBlockHeight: first ? 170 : 200 },
        context: { slot: first ? 100 : 120 } }
    }
    assert.equal(method, 'getFeeForMessage')
    feeChecks.push(params)
    return { value: feeChecks.length === 1 ? null : 5000 }
  })
  assert.equal(feeChecks.length, 2)
  assert.notEqual(feeChecks[0][0], feeChecks[1][0])
  assert.deepEqual(feeChecks.map(([, options]) => options), [
    { commitment: 'confirmed', minContextSlot: 100 },
    { commitment: 'finalized', minContextSlot: 120 },
  ])
  assert.equal(order.id, quote.id)
  assert.equal(order.depositMicros, quote.depositMicros)
  assert.equal(order.lightningAddress, quote.lightningAddress)
  assert.equal(order.blockhash, finalHash)
  assert.equal(order.feeLamports, '5000')
  assert.equal(order.accountLamports, '1488440')
})
