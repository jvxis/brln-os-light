import assert from 'node:assert/strict'
import test from 'node:test'
import { Interface } from 'ethers/abi'
import { Transaction } from 'ethers/transaction'
import { baseUSDC, createBaseBackup, maximumBackupBytes, restoreBaseAddress, signBaseUSDCTransfer } from '../src/wallet.js'

test('recovers the original address only with the correct backup password', async () => {
  const password = 'synthetic-test-password'
  const { address, backup } = await createBaseBackup(password)
  assert.match(address, /^0x[0-9a-f]{40}$/i)
  const json = JSON.parse(backup)
  assert.equal(json.version, 3)
  assert.equal(json.address, address.slice(2).toLowerCase())
  assert.match((json.Crypto ?? json.crypto).ciphertext, /^[0-9a-f]{64}$/i)
  assert.equal(json['x-ethers'], undefined)
  assert.equal(await restoreBaseAddress(backup, password), address)
  await assert.rejects(restoreBaseAddress(backup, 'incorrect-test-password'))
})

test('rejects untrusted KDF parameters, extra recovery data and oversized files', async () => {
  const password = 'synthetic-test-password'
  const { backup } = await createBaseBackup(password)
  const modified = JSON.parse(backup)
  const cipher = modified.Crypto ?? modified.crypto
  cipher.kdfparams.n *= 2
  await assert.rejects(restoreBaseAddress(JSON.stringify(modified), password), /Unsupported backup format/)
  const extension = { ...JSON.parse(backup), 'x-ethers': {} }
  await assert.rejects(restoreBaseAddress(JSON.stringify(extension), password), /Unsupported backup format/)
  await assert.rejects(restoreBaseAddress('a'.repeat(maximumBackupBytes + 1), password), /Invalid backup/)
  await assert.rejects(createBaseBackup('short'), /Invalid password length/)
})

test('signs exactly the approved transfer of native USDC on Base', async () => {
  const password = 'synthetic-test-password'
  const { address, backup } = await createBaseBackup(password)
  const details = {
    from: address,
    recipient: '0x0000000000000000000000000000000000001234',
    micros: '881932',
    chainId: 8453,
    expiresAt: Date.now() + 60_000,
    nonce: 7,
    gasLimit: '70000',
    maxFeePerGas: '10000000',
    maxPriorityFeePerGas: '1000000',
    maximumGasWei: '800000000000',
  }
  const raw = await signBaseUSDCTransfer(backup, password, details)
  const transaction = Transaction.from(raw)
  assert.equal(transaction.type, 2)
  assert.equal(transaction.chainId, 8453n)
  assert.equal(transaction.from, address)
  assert.equal(transaction.to, baseUSDC)
  assert.equal(transaction.value, 0n)
  assert.equal(transaction.nonce, 7)
  assert.equal(transaction.gasLimit * transaction.maxFeePerGas, 700000000000n)
  const abi = new Interface(['function transfer(address recipient, uint256 amount)'])
  assert.deepEqual([...abi.decodeFunctionData('transfer', transaction.data)], [details.recipient, 881932n])

  await assert.rejects(signBaseUSDCTransfer(backup, password, { ...details, chainId: 1 }), /Invalid or expired/)
  await assert.rejects(signBaseUSDCTransfer(backup, password, { ...details, expiresAt: 1 }), /Invalid or expired/)
  await assert.rejects(signBaseUSDCTransfer(backup, password, { ...details, maximumGasWei: '1' }), /exceeds/)
  await assert.rejects(signBaseUSDCTransfer(backup, password, { ...details, from: details.recipient, recipient: address }), /does not match/)
})
