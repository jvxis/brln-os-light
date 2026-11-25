import assert from 'node:assert/strict'
import { LiteSVM } from 'litesvm'
import { getAddressDecoder, getAddressEncoder, getTransactionDecoder } from '@solana/kit'
import { createSolanaBackup, signDirectDeposit, signSponsoredDeposit } from './wallet.mjs'
import { devnetUsdcMint, tokenProgram, usdcAccount, verifyDirectDeposit, verifyPartiallySignedDeposit } from './sponsored.mjs'

const svm = new LiteSVM()
const password = 'synthetic zero-lamport wallet'
const { address: wallet, backup } = await createSolanaBackup(password)
const { address: deposit } = await createSolanaBackup(password)
const payer = await crypto.subtle.generateKey('Ed25519',true,['sign','verify'])
const sponsor = getAddressDecoder().decode(new Uint8Array(await crypto.subtle.exportKey('raw',payer.publicKey)))
const mint = devnetUsdcMint
const [source,destination,fee] = await Promise.all([usdcAccount(wallet,mint),usdcAccount(deposit,mint),usdcAccount(sponsor,mint)])
svm.airdrop(sponsor,1000000000n)

const mintData = Buffer.alloc(82)
mintData.writeBigUInt64LE(5000000n,36)
mintData[44] = 6
mintData[45] = 1
function put(addr,data) {
  svm.setAccount({address:addr,data,executable:false,lamports:svm.minimumBalanceForRentExemption(BigInt(data.length)),programAddress:tokenProgram,space:BigInt(data.length)})
}
put(mint,mintData)
function tokenData(owner,micros) {
  const data = Buffer.alloc(165)
  data.set(getAddressEncoder().encode(mint),0)
  data.set(getAddressEncoder().encode(owner),32)
  data.writeBigUInt64LE(micros,64)
  data[108] = 1
  return data
}
put(source,tokenData(wallet,5000000n))
put(fee,tokenData(sponsor,0n))
assert.equal(svm.getBalance(wallet)??0n,0n)
const plan={version:1,id:'0123456789abcdef0123456789abcdef',cluster:'devnet',wallet,deposit,sponsor,sponsorFeeAccount:sponsor,
  depositMicros:'1065005',networkFeeMicros:'10000',totalMicros:'1075005',expiresAt:Date.now()+60000,blockhash:svm.latestBlockhash(),
  lastValidBlockHeight:123,createDepositAccount:true}
const partial=await signSponsoredDeposit(backup,password,plan)
const encoded = await verifyPartiallySignedDeposit(plan, partial)
const decoded = getTransactionDecoder().decode(Buffer.from(encoded, 'base64'))
const sig=new Uint8Array(await crypto.subtle.sign('Ed25519',payer.privateKey,decoded.messageBytes))
const full={...decoded,signatures:{...decoded.signatures,[sponsor]:sig}}
const result=svm.sendTransaction(full)
if ('err' in result) throw Error('SVM rejected: '+result.err()+' '+JSON.stringify(result.meta().logs()))
const amount=key=>Buffer.from(svm.getAccount(key).data).readBigUInt64LE(64)
assert.equal(amount(source),3924995n)
assert.equal(amount(destination),1065005n)
assert.equal(amount(fee),10000n)
assert.equal(svm.getBalance(wallet)??0n,0n)
assert(svm.getBalance(sponsor)<1000000000n)
console.log('PASS: LiteSVM executed a dual-signed SPL transfer, created the destination ATA, charged the displayed USDC fee, and spent zero SOL from the user.')

const { address: directDeposit } = await createSolanaBackup(password)
const directTarget = await usdcAccount(directDeposit, mint)
const accountReserve = svm.minimumBalanceForRentExemption(165n)
svm.airdrop(wallet, 10_000_000n)
const directPlan = {
  version: 1, feeMode: 'wallet', id: 'fd23456789abcdef0123456789abcdef',
  cluster: 'devnet', wallet, deposit: directDeposit, depositMicros: '1065005',
  feeLamports: '5000', accountLamports: String(accountReserve), expectedSats: '1679',
  lightningAddress: 'treasury@example.com', expiresAt: Date.now() + 60_000,
  blockhash: svm.latestBlockhash(), lastValidBlockHeight: 123, createDepositAccount: true,
}
const directlySigned = await signDirectDeposit(backup, password, directPlan)
const directlyEncoded = await verifyDirectDeposit(directPlan, directlySigned)
const directResult = svm.sendTransaction(getTransactionDecoder().decode(Buffer.from(directlyEncoded, 'base64')))
if ('err' in directResult) throw Error('SVM rejected the direct deposit: ' + directResult.err())
assert.equal(amount(source), 2_859_990n)
assert.equal(amount(directTarget), 1_065_005n)
assert.equal(amount(fee), 10_000n)
assert.equal(svm.getBalance(wallet), 10_000_000n - 5_000n - accountReserve)
console.log('PASS: LiteSVM executed a one-signature SPL transfer and charged the wallet the exact SOL network fee and ATA deposit.')
