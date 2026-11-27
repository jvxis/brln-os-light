import { getBase58Decoder, getTransactionDecoder } from '@solana/kit'
import { directOrderRpc, prepareDirectOrder } from './solana-orders.mjs'
import { address } from '@solana/kit'
import { tokenProgram, usdcMint, verifyDirectDeposit } from './sponsored.mjs'

async function run({ action, recipient, reference, order, signed }) {
  switch (action) {
    case 'solana-prepare':
      return { order: await prepareDirectOrder(order) }
    case 'solana-validate':
    case 'solana-broadcast': {
      const transaction = await verifyDirectDeposit(order, signed)
      const decoded = getTransactionDecoder().decode(Buffer.from(transaction, 'base64'))
      const signature = getBase58Decoder().decode(decoded.signatures[order.wallet])
      if (action === 'solana-validate') {
        const simulation = await directOrderRpc('simulateTransaction', transaction, {
          encoding: 'base64', sigVerify: true, commitment: 'confirmed',
        })
        if (simulation.value?.err !== null) throw new Error('Transaction simulation failed')
        return { status: 'verified', deposit_ref: signature }
      }
      if (signature !== reference) throw new Error('Transaction signature mismatch')
      const sent = await directOrderRpc('sendTransaction', transaction, {
        encoding: 'base64', skipPreflight: false, preflightCommitment: 'confirmed', maxRetries: 3,
      })
      if (sent !== signature) throw new Error('Submission requires reconciliation')
      return { status: 'submitted', deposit_ref: signature }
    }
    case 'solana-finality': {
      const { value: [tx] } = await directOrderRpc('getSignatureStatuses', [reference], { searchTransactionHistory: true })
      if (tx?.confirmationStatus === 'finalized') {
        if (tx.err !== null) return { status: 'failed' }
        const receipt = await directOrderRpc('getTransaction', reference, {
          encoding: 'jsonParsed', commitment: 'finalized', maxSupportedTransactionVersion: 0,
        })
        const meta = receipt?.meta
        const payer = receipt?.transaction?.message?.accountKeys?.[0]?.pubkey
        if (payer === order?.wallet && receipt?.transaction?.signatures?.[0] === reference && meta?.err === null &&
            [meta.fee, meta.preBalances?.[0], meta.postBalances?.[0]].every(Number.isSafeInteger)) {
          const debit = meta.preBalances[0] - meta.postBalances[0]
          if (debit >= meta.fee && meta.fee >= 0) return { status: 'finalized', sol_lamports: debit }
        }
        return { status: 'finalized' }
      }
      const limit = order?.lastValidBlockHeight
      if (!tx && Number.isSafeInteger(limit) && limit > 0) {
        const height = await directOrderRpc('getBlockHeight', { commitment: 'finalized' })
        if (Number.isSafeInteger(height) && height > limit + 150) {
          const { value: [confirmed] } = await directOrderRpc('getSignatureStatuses', [reference], { searchTransactionHistory: true })
          if (!confirmed) return { status: 'expired' }
        }
      }
      return { status: 'pending' }
    }
    case 'solana-balance': {
      const owner = address(recipient)
      const [{ value: balance }, { value: accounts }] = await Promise.all([
        directOrderRpc('getBalance', owner, { commitment: 'finalized' }),
        directOrderRpc('getTokenAccountsByOwner', owner, { mint: usdcMint }, { commitment: 'finalized', encoding: 'jsonParsed' }),
      ])
      if (!Number.isSafeInteger(balance) || balance < 0) throw new Error('Invalid SOL balance')
      let usdc = 0n
      for (const entry of accounts) {
        const info = entry.account?.data?.parsed?.info
        const amount = info?.tokenAmount
        if (entry.account?.owner !== tokenProgram || info?.owner !== owner || info?.mint !== usdcMint ||
            amount?.decimals !== 6 || typeof amount.amount !== 'string' || !/^\d+$/.test(amount.amount)) {
          throw new Error('Invalid USDC account')
        }
        usdc += BigInt(amount.amount)
      }
      if (usdc > BigInt(Number.MAX_SAFE_INTEGER)) throw new Error('Invalid USDC balance')
      return { sol_lamports: balance, usdc_micros: Number(usdc) }
    }
    default:
      throw new Error('Unknown operation')
  }
}

async function main() {
  let buffer = ''
  process.stdin.setEncoding('utf8')
  for await (const chunk of process.stdin) {
    buffer += chunk
    if (Buffer.byteLength(buffer) > 16384) throw new Error('Request too large')
  }
  process.stdout.write(JSON.stringify(await run(JSON.parse(buffer))))
}

void main().catch(error => {
  if (['insufficient_sol', 'insufficient_usdc'].includes(error.code)) {
    process.stdout.write(JSON.stringify({ status: error.code }))
  } else {
    process.exitCode = 1
  }
})
