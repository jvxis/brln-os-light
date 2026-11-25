import {
  AccountRole, address, appendTransactionMessageInstructions, blockhash,
  compileTransaction, createTransactionMessage, getAddressEncoder,
  getProgramDerivedAddress, getTransactionDecoder, getTransactionEncoder, setTransactionMessageFeePayer,
  setTransactionMessageLifetimeUsingBlockhash
} from '@solana/kit'

export const usdcMint = address('EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v')
export const devnetUsdcMint = address('4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU')
export const tokenProgram = address('TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA')
export const associatedTokenProgram = address('ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL')
const systemProgram = address('11111111111111111111111111111111')
const encoder = getAddressEncoder()

function amount(value) {
  if (typeof value !== 'string' || !/^(0|[1-9][0-9]{0,8})$/.test(value)) {
    throw new Error('Invalid USDC amount')
  }
  return BigInt(value)
}

export function reviewSponsoredDeposit(order, now = Date.now()) {
  if (order?.version !== 1 || !/^[a-f0-9]{32}$/.test(order.id) ||
      !['mainnet', 'devnet'].includes(order.cluster) ||
      !Number.isSafeInteger(order.expiresAt) || order.expiresAt <= now || order.expiresAt - now > 600_000 ||
      !Number.isSafeInteger(order.lastValidBlockHeight) || order.lastValidBlockHeight <= 0 ||
      typeof order.createDepositAccount !== 'boolean') {
    throw new Error('Invalid or expired Solana order')
  }
  const wallet = address(order.wallet)
  const deposit = address(order.deposit)
  const sponsor = address(order.sponsor)
  const sponsorFeeAccount = address(order.sponsorFeeAccount)
  const usdc = amount(order.depositMicros)
  const fee = amount(order.networkFeeMicros)
  const total = amount(order.totalMicros)
  if (wallet === deposit || sponsor === wallet || sponsor === deposit ||
      sponsorFeeAccount === wallet || sponsorFeeAccount === deposit ||
      usdc <= 0n || total !== usdc + fee || total > 100_000_000n) {
    throw new Error('USDC amounts or recipients differ from the approved order')
  }
  return {
    id: order.id, wallet, deposit, sponsor, sponsorFeeAccount, usdc, fee, total,
    cluster: order.cluster, mint: order.cluster === 'devnet' ? devnetUsdcMint : usdcMint,
    expiresAt: order.expiresAt, createDepositAccount: order.createDepositAccount,
    lifetime: { blockhash: blockhash(order.blockhash), lastValidBlockHeight: BigInt(order.lastValidBlockHeight) }
  }
}

export function reviewDirectDeposit(order, now = Date.now()) {
  if (order?.feeMode !== 'wallet' || order.version !== 1 || !/^[a-f0-9]{32}$/.test(order.id) ||
      !['mainnet', 'devnet'].includes(order.cluster) ||
      !Number.isSafeInteger(order.expiresAt) || order.expiresAt <= now || order.expiresAt - now > 600_000 ||
      !Number.isSafeInteger(order.lastValidBlockHeight) || order.lastValidBlockHeight <= 0 ||
      typeof order.createDepositAccount !== 'boolean' ||
      typeof order.lightningAddress !== 'string' ||
      !/^[a-z0-9_.-]{1,64}@[a-z0-9.-]{1,200}$/i.test(order.lightningAddress)) {
    throw new Error('Invalid or expired Solana order')
  }
  const wallet = address(order.wallet)
  const deposit = address(order.deposit)
  const usdc = amount(order.depositMicros)
  const feeLamports = amount(order.feeLamports)
  const accountLamports = amount(order.accountLamports)
  const expectedSats = amount(order.expectedSats)
  const providerFee = order.providerFeeMicros === undefined ? undefined : amount(order.providerFeeMicros)
  if (wallet === deposit || usdc <= 0n || usdc > 100_000_000n || expectedSats <= 0n ||
      feeLamports <= 0n || (accountLamports > 0n) !== order.createDepositAccount ||
      feeLamports + accountLamports > 10_000_000n || (providerFee !== undefined && providerFee >= usdc)) {
    throw new Error('Invalid deposit or Solana costs')
  }
  return {
    id: order.id, wallet, deposit, usdc, feeLamports, accountLamports, expectedSats, providerFee,
    lightningAddress: order.lightningAddress,
    cluster: order.cluster, mint: order.cluster === 'devnet' ? devnetUsdcMint : usdcMint,
    expiresAt: order.expiresAt, createDepositAccount: order.createDepositAccount,
    lifetime: { blockhash: blockhash(order.blockhash), lastValidBlockHeight: BigInt(order.lastValidBlockHeight) }
  }
}

export async function usdcAccount(owner, mint = usdcMint) {
  const [ata] = await getProgramDerivedAddress({
    programAddress: associatedTokenProgram,
    seeds: [encoder.encode(owner), encoder.encode(tokenProgram), encoder.encode(mint)]
  })
  return ata
}

function transfer(source, target, owner, amount, mint) {
  const data = new Uint8Array(10)
  data[0] = 12 // SPL Token TransferChecked
  new DataView(data.buffer).setBigUint64(1, amount, true)
  data[9] = 6
  return {
    programAddress: tokenProgram,
    accounts: [
      { address: source, role: AccountRole.WRITABLE },
      { address: mint, role: AccountRole.READONLY },
      { address: target, role: AccountRole.WRITABLE },
      { address: owner, role: AccountRole.READONLY_SIGNER },
    ],
    data
  }
}

export async function compileSponsoredDeposit(reviewed) {
  const { source, instructions } = await depositInstructions(reviewed, reviewed.sponsor)
  if (reviewed.fee > 0n) {
    const feeRecipient = await usdcAccount(reviewed.sponsorFeeAccount, reviewed.mint)
    instructions.push(transfer(source, feeRecipient, reviewed.wallet, reviewed.fee, reviewed.mint))
  }
  return compileDepositMessage(reviewed, reviewed.sponsor, instructions)
}

export async function compileDirectDeposit(reviewed) {
  const { instructions } = await depositInstructions(reviewed, reviewed.wallet)
  return compileDepositMessage(reviewed, reviewed.wallet, instructions)
}

async function depositInstructions(reviewed, payer) {
  const [source, deposit] = await Promise.all([
    usdcAccount(reviewed.wallet, reviewed.mint), usdcAccount(reviewed.deposit, reviewed.mint)
  ])
  const instructions = []
  if (reviewed.createDepositAccount) {
    instructions.push({
      programAddress: associatedTokenProgram,
      accounts: [
        { address: payer, role: AccountRole.WRITABLE_SIGNER },
        { address: deposit, role: AccountRole.WRITABLE },
        { address: reviewed.deposit, role: AccountRole.READONLY },
        { address: reviewed.mint, role: AccountRole.READONLY },
        { address: systemProgram, role: AccountRole.READONLY },
        { address: tokenProgram, role: AccountRole.READONLY },
      ],
      data: new Uint8Array([1]) // Idempotent creation.
    })
  }
  instructions.push(transfer(source, deposit, reviewed.wallet, reviewed.usdc, reviewed.mint))
  return { source, instructions }
}

function compileDepositMessage(reviewed, payer, instructions) {
  let message = createTransactionMessage({ version: 'legacy' })
  message = setTransactionMessageFeePayer(payer, message)
  message = setTransactionMessageLifetimeUsingBlockhash(reviewed.lifetime, message)
  return compileTransaction(appendTransactionMessageInstructions(instructions, message))
}

export function encodeSolanaTransaction(transaction) {
  return btoa(String.fromCharCode(...getTransactionEncoder().encode(transaction)))
}

export async function quoteTransactionWithoutSponsorFee(order) {
  const reviewed = reviewSponsoredDeposit(order)
  const compiled = await compileSponsoredDeposit({ ...reviewed, fee: 0n, total: reviewed.usdc })
  return encodeSolanaTransaction(compiled)
}

// Run at the receiving end of the file handoff. Derive the message from the
// stored quote, so a user-supplied transaction cannot choose a different
// fee payer, mint, transfer, authority, destination or fee.
export async function verifyPartiallySignedDeposit(order, signed) {
  const reviewed = reviewSponsoredDeposit(order)
  return verifySignedMessage(reviewed, signed, compileSponsoredDeposit, reviewed.sponsor)
}

export async function verifyDirectDeposit(order, signed) {
  const reviewed = reviewDirectDeposit(order)
  return verifySignedMessage(reviewed, signed, compileDirectDeposit)
}

async function verifySignedMessage(reviewed, signed, compile, unsignedSponsor) {
  const submitted = signed?.transaction
  if (signed?.version !== 1 || signed.id !== reviewed.id || signed.cluster !== reviewed.cluster ||
      typeof submitted !== 'string' || submitted.length > 1644) {
    throw new Error('Signed transaction does not match this Solana order')
  }
  const bytes = Uint8Array.from(atob(submitted), char => char.charCodeAt(0))
  if (bytes.length > 1232) throw new Error('Invalid Solana transaction size')
  const transaction = getTransactionDecoder().decode(bytes)
  const expected = await compile(reviewed)
  const exactMessage = expected.messageBytes.length === transaction.messageBytes.length &&
    expected.messageBytes.every((byte, index) => byte === transaction.messageBytes[index])
  const signature = transaction.signatures[reviewed.wallet]
  if (!exactMessage || encodeSolanaTransaction(transaction) !== submitted || !signature ||
      (unsignedSponsor && transaction.signatures[unsignedSponsor] !== null)) {
    throw new Error('Signed transaction does not match this Solana order')
  }
  const publicKey = await crypto.subtle.importKey('raw', encoder.encode(reviewed.wallet), 'Ed25519', false, ['verify'])
  if (!await crypto.subtle.verify('Ed25519', publicKey, signature, transaction.messageBytes)) {
    throw new Error('Invalid signature from the wallet')
  }
  return submitted
}
