import assert from 'node:assert/strict'
import test from 'node:test'
import { createBaseBackup, maximumBackupBytes, restoreBaseAddress } from '../src/wallet.js'

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
