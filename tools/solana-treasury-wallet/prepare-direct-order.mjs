import { address } from '@solana/kit'
import { pathToFileURL } from 'node:url'
import { compileDirectDeposit, reviewDirectDeposit, tokenProgram, usdcAccount, usdcMint } from './sponsored.mjs'

export async function directOrderRpc(method, ...params) {
  const response = await fetch('https://api.mainnet-beta.solana.com', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ jsonrpc: '2.0', id: 1, method, params }),
    signal: AbortSignal.timeout(10_000),
  })
  if (!response.ok) throw new Error(`Solana RPC unavailable (${method})`)
  const body = await response.json()
  if (body.error || body.result === undefined) throw new Error(`Solana RPC rejected ${method}`)
  return body.result
}

function accountTokenInfo(account, owner) {
  const info = account?.data?.parsed?.info
  if (account?.owner !== tokenProgram || info?.owner !== owner || info.mint !== usdcMint ||
      info.state !== 'initialized' || info.tokenAmount?.decimals !== 6 ||
      typeof info.tokenAmount.amount !== 'string' || !/^[0-9]+$/.test(info.tokenAmount.amount)) {
    throw new Error('Unexpected USDC account')
  }
  return info.tokenAmount.amount
}

export async function prepareDirectOrder(quote) {
  const wallet = address(quote.wallet)
  const deposit = address(quote.deposit)
  const [source, target] = await Promise.all([usdcAccount(wallet), usdcAccount(deposit)])
  const [
    { value: [sourceAccount, targetAccount] }, { value: balance },
    { value: latestBlock }, systemReserve,
  ] = await Promise.all([
    directOrderRpc('getMultipleAccounts', [source, target], { commitment: 'confirmed', encoding: 'jsonParsed' }),
    directOrderRpc('getBalance', wallet, { commitment: 'confirmed' }),
    directOrderRpc('getLatestBlockhash', { commitment: 'confirmed' }),
    directOrderRpc('getMinimumBalanceForRentExemption', 0, { commitment: 'confirmed' }),
  ])
  if (!sourceAccount || BigInt(accountTokenInfo(sourceAccount, wallet)) < BigInt(quote.depositMicros)) {
    throw Object.assign(new Error('Wallet has insufficient USDC'), { code: 'insufficient_usdc' })
  }
  if (targetAccount) accountTokenInfo(targetAccount, deposit)
  const accountLamports = targetAccount ? 0 : await directOrderRpc('getMinimumBalanceForRentExemption', 165, { commitment: 'confirmed' })
  const order = {
    version: 1, feeMode: 'wallet', cluster: 'mainnet', id: quote.id,
    wallet, deposit, depositMicros: quote.depositMicros, providerFeeMicros: quote.providerFeeMicros,
    expectedSats: quote.expectedSats, lightningAddress: quote.lightningAddress,
    expiresAt: Math.min(quote.expiresAt, Date.now() + 60_000),
    ...latestBlock, createDepositAccount: !targetAccount, accountLamports: String(accountLamports),
    feeLamports: '1',
  }
  const transaction = await compileDirectDeposit(reviewDirectDeposit(order))
  const { value: fee } = await directOrderRpc('getFeeForMessage', Buffer.from(transaction.messageBytes).toString('base64'), { commitment: 'confirmed' })
  if (!Number.isSafeInteger(fee)) throw new Error('Network fee could not be estimated')
  if (balance < fee + accountLamports + systemReserve) {
    throw Object.assign(new Error('Wallet has insufficient SOL'), { code: 'insufficient_sol' })
  }
  order.feeLamports = String(fee)
  reviewDirectDeposit(order)
  return order
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    let input = ''
    process.stdin.setEncoding('utf8')
    for await (const chunk of process.stdin) {
      input += chunk
      if (input.length > 4096) throw new Error('Quote exceeds the maximum size')
    }
    const quote = JSON.parse(input)
    process.stdout.write(JSON.stringify(await prepareDirectOrder(quote)) + '\n')
  } catch (error) {
    process.stderr.write(`${error.message}\n`)
    process.exitCode = 1
  }
}
