import assert from 'node:assert/strict'
import test from 'node:test'
import { decompileTransactionMessage, getAddressEncoder, getCompiledTransactionMessageDecoder, getTransactionDecoder } from '@solana/kit'
import { createSolanaBackup, maxBackupBytes, recoverSolanaAddress, signDirectDeposit, signSponsoredDeposit } from './wallet.mjs'
import { associatedTokenProgram, quoteTransactionWithoutSponsorFee, tokenProgram, usdcAccount, usdcMint, verifyDirectDeposit, verifyPartiallySignedDeposit } from './sponsored.mjs'

test('creates distinct Solana keys, then recovers using only the encrypted wallet and password', async () => {
  const password = 'a synthetic wallet password'
  const original = await createSolanaBackup(password)
  const second = await createSolanaBackup(password)
  assert.match(original.address, /^[1-9A-HJ-NP-Za-km-z]{32,44}$/)
  assert.notEqual(original.address, second.address)
  const stored = JSON.parse(original.backup)
  assert.deepEqual(Object.keys(stored).sort(), ['address', 'algorithm', 'ciphertext', 'iv', 'salt', 'schema'])
  assert.equal(stored.address, original.address)
  assert.equal(await recoverSolanaAddress(original.backup, password), original.address)
  await assert.rejects(recoverSolanaAddress(original.backup, 'not the right password'))
  await assert.rejects(recoverSolanaAddress(JSON.stringify({ ...stored, address: second.address }), password))
  await assert.rejects(recoverSolanaAddress(JSON.stringify({ ...stored, iterations: 1 }), password), /Unsupported/)
  await assert.rejects(recoverSolanaAddress('x'.repeat(maxBackupBytes + 1), password), /Invalid/)
})

test('partially signs an exact USDC transfer with the sponsor as Solana fee payer', async () => {
  const password = 'a synthetic wallet password'
  const { address: wallet, backup } = await createSolanaBackup(password)
  const { address: deposit } = await createSolanaBackup(password)
  const { address: sponsor } = await createSolanaBackup(password)
  const order = {
    version: 1, id: '0123456789abcdef0123456789abcdef', cluster: 'mainnet', wallet, deposit, sponsor,
    sponsorFeeAccount: sponsor, depositMicros: '1065005', networkFeeMicros: '10000', totalMicros: '1075005',
    expiresAt: Date.now() + 60_000, blockhash: deposit, lastValidBlockHeight: 123, createDepositAccount: true
  }
  const signed = await signSponsoredDeposit(backup,password,order)
  assert.equal(signed.id,order.id)
  assert.equal(await verifyPartiallySignedDeposit(order,signed),signed.transaction)
  const tx = getTransactionDecoder().decode(Buffer.from(signed.transaction,'base64'))
  assert.deepEqual(Object.keys(tx.signatures), [sponsor,wallet])
  assert.equal(tx.signatures[sponsor],null)
  const ownerKey = await crypto.subtle.importKey('raw',getAddressEncoder().encode(wallet),'Ed25519',false,['verify'])
  assert.equal(await crypto.subtle.verify('Ed25519',ownerKey,tx.signatures[wallet],tx.messageBytes),true)
  const compiled = getCompiledTransactionMessageDecoder().decode(tx.messageBytes)
  const message = decompileTransactionMessage(compiled)
  assert.equal(message.feePayer.address,sponsor)
  assert.deepEqual(message.instructions.map(ix=>ix.programAddress),[associatedTokenProgram,tokenProgram,tokenProgram])
  assert.deepEqual([...message.instructions[0].data],[1])
  assert.equal(message.instructions[0].accounts[0].address,sponsor)
  assert.equal(message.instructions[0].accounts[1].address,await usdcAccount(deposit))
  const expectedTargets = [await usdcAccount(deposit),await usdcAccount(sponsor)]
  for (const [index, ix] of message.instructions.slice(1).entries()) {
    assert.deepEqual(ix.accounts.map(account=>account.address),[await usdcAccount(wallet),usdcMint,expectedTargets[index],wallet])
    assert.equal(ix.data[0],12)
    assert.equal(ix.data[9],6)
    assert.equal(new DataView(ix.data.buffer,ix.data.byteOffset,ix.data.byteLength).getBigUint64(1,true),[1065005n,10000n][index])
  }
  await assert.rejects(signSponsoredDeposit(backup,password,{...order,totalMicros:'1065005'}),/differ/)
  await assert.rejects(signSponsoredDeposit(backup,password,{...order,wallet:deposit,deposit:wallet}),/backup does not match/)
  await assert.rejects(signSponsoredDeposit(backup,password,{...order,expiresAt:1}),/expired/)
  await assert.rejects(verifyPartiallySignedDeposit({...order,depositMicros:'1055005',totalMicros:'1065005'},signed),/does not match/)
  await assert.rejects(verifyPartiallySignedDeposit({...order,cluster:'devnet'},signed),/does not match/)
  const image = Buffer.from(signed.transaction,'base64')
  image[65] ^= 1
  await assert.rejects(verifyPartiallySignedDeposit(order,{...signed,transaction:image.toString('base64')}),/signature/i)
  const unsigned = getTransactionDecoder().decode(Buffer.from(await quoteTransactionWithoutSponsorFee(order),'base64'))
  assert(Object.values(unsigned.signatures).every(signature=>signature===null))
  assert.equal(getCompiledTransactionMessageDecoder().decode(unsigned.messageBytes).instructions.length,2)
})

test('signs only the approved USDC transfer with the wallet paying SOL fees', async () => {
  const password = 'a synthetic wallet password'
  const { address: wallet, backup } = await createSolanaBackup(password)
  const { address: deposit } = await createSolanaBackup(password)
  const order = {
    version: 1, feeMode: 'wallet', id: '0123456789abcdef0123456789abcdef',
    cluster: 'mainnet', wallet, deposit, depositMicros: '1065005', providerFeeMicros: '21044',
    feeLamports: '5000', accountLamports: '1488432', expectedSats: '1679',
    lightningAddress: 'treasury@example.com', expiresAt: Date.now() + 60_000,
    blockhash: deposit, lastValidBlockHeight: 123, createDepositAccount: true,
  }
  const signed = await signDirectDeposit(backup, password, order)
  assert.equal(await verifyDirectDeposit(order, signed), signed.transaction)
  const tx = getTransactionDecoder().decode(Buffer.from(signed.transaction, 'base64'))
  assert.deepEqual(Object.keys(tx.signatures), [wallet])
  const message = decompileTransactionMessage(getCompiledTransactionMessageDecoder().decode(tx.messageBytes))
  assert.equal(message.feePayer.address, wallet)
  const [create, transfer] = message.instructions
  assert.deepEqual(message.instructions.map(ix => ix.programAddress), [associatedTokenProgram, tokenProgram])
  assert.equal(create.accounts[0].address, wallet)
  assert.equal(create.accounts[1].address, await usdcAccount(deposit))
  assert.deepEqual(transfer.accounts.map(account => account.address), [await usdcAccount(wallet), usdcMint, await usdcAccount(deposit), wallet])
  assert.equal(new DataView(transfer.data.buffer, transfer.data.byteOffset, transfer.data.byteLength).getBigUint64(1, true), 1065005n)
  await assert.rejects(verifyDirectDeposit({ ...order, depositMicros: '1065006' }, signed), /does not match/)
  await assert.rejects(signDirectDeposit(backup, password, { ...order, accountLamports: '10000001' }), /costs/)
  const existingAccount = { ...order, createDepositAccount: false, accountLamports: '0' }
  const existingSigned = await signDirectDeposit(backup, password, existingAccount)
  const existingTx = getTransactionDecoder().decode(Buffer.from(existingSigned.transaction, 'base64'))
  assert.equal(getCompiledTransactionMessageDecoder().decode(existingTx.messageBytes).instructions.length, 1)
})
