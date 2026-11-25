const alphabet = '123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz'
const iterations = 600_000
const algorithm = 'ed25519-aes256-gcm-pbkdf2-sha256'
const schema = 'lightningos-solana-v1'
const encoder = new TextEncoder()
const recoveryCheck = encoder.encode('LightningOS Solana recovery check v1')
export const maxBackupBytes = 4096

function base58(bytes) {
  let number = BigInt('0x' + hex(bytes))
  let address = ''
  while (number > 0n) {
    address = alphabet[Number(number % 58n)] + address
    number /= 58n
  }
  for (const byte of bytes) {
    if (byte !== 0) break
    address = '1' + address
  }
  return address
}

function publicBytes(address) {
  if (typeof address !== 'string' || address.length < 32 || address.length > 44) {
    throw new Error('Invalid Solana address')
  }
  let number = 0n
  for (const char of address) {
    const digit = alphabet.indexOf(char)
    if (digit < 0) throw new Error('Invalid Solana address')
    number = number * 58n + BigInt(digit)
  }
  if (number <= 0n || number >= 1n << 256n) throw new Error('Invalid Solana address')
  const bytes = unhex(number.toString(16).padStart(64, '0'), 32)
  if (base58(bytes) !== address) throw new Error('Invalid Solana address')
  return bytes
}

function hex(bytes) {
  return Array.from(new Uint8Array(bytes), byte => byte.toString(16).padStart(2, '0')).join('')
}

function unhex(value, size) {
  if (typeof value !== 'string' || value.length !== 2 * size || !/^[0-9a-f]+$/.test(value)) {
    throw new Error('Invalid encrypted wallet')
  }
  const result = new Uint8Array(size)
  for (let i = 0; i < size; i++) result[i] = Number.parseInt(value.slice(i * 2, i * 2 + 2), 16)
  return result
}

async function backupKey(password, salt) {
  if (typeof password !== 'string' || password.length < 14 || password.length > 128) {
    throw new Error('Invalid backup password')
  }
  const encoded = encoder.encode(password)
  try {
    const source = await crypto.subtle.importKey('raw', encoded, 'PBKDF2', false, ['deriveKey'])
    return await crypto.subtle.deriveKey({
      name: 'PBKDF2', hash: 'SHA-256', salt, iterations
    }, source, { name: 'AES-GCM', length: 256 }, false, ['encrypt', 'decrypt'])
  } finally {
    encoded.fill(0)
  }
}

const cipherParameters = (iv, address) => ({
  name: 'AES-GCM', iv, additionalData: encoder.encode(`${schema}:${algorithm}:${address}`), tagLength: 128
})

export async function createSolanaBackup(password) {
  const salt = crypto.getRandomValues(new Uint8Array(16))
  const iv = crypto.getRandomValues(new Uint8Array(12))
  const key = await backupKey(password, salt)
  const pair = await crypto.subtle.generateKey('Ed25519', true, ['sign', 'verify'])
  const address = base58(new Uint8Array(await crypto.subtle.exportKey('raw', pair.publicKey)))
  const secret = new Uint8Array(await crypto.subtle.exportKey('pkcs8', pair.privateKey))
  try {
    const encrypted = await crypto.subtle.encrypt(cipherParameters(iv, address), key, secret)
    return {
      address,
      backup: JSON.stringify({ schema, algorithm, address, salt: hex(salt), iv: hex(iv), ciphertext: hex(encrypted) })
    }
  } finally {
    secret.fill(0)
  }
}

async function withSolanaWallet(backup, password, use) {
  if (typeof backup !== 'string' || encoder.encode(backup).byteLength > maxBackupBytes) {
    throw new Error('Invalid wallet backup')
  }
  const value = JSON.parse(backup)
  if (value?.schema !== schema || value.algorithm !== algorithm ||
      Object.keys(value).length !== 6 || typeof value.ciphertext !== 'string' ||
      value.ciphertext.length < 128 || value.ciphertext.length > 288 || value.ciphertext.length % 2 !== 0) {
    throw new Error('Unsupported wallet backup')
  }
  const publicKey = await crypto.subtle.importKey('raw', publicBytes(value.address), 'Ed25519', false, ['verify'])
  const key = await backupKey(password, unhex(value.salt, 16))
  const secret = new Uint8Array(await crypto.subtle.decrypt(
    cipherParameters(unhex(value.iv, 12), value.address), key, unhex(value.ciphertext, value.ciphertext.length / 2)
  ))
  try {
    const privateKey = await crypto.subtle.importKey('pkcs8', secret, 'Ed25519', false, ['sign'])
    const signature = await crypto.subtle.sign('Ed25519', privateKey, recoveryCheck)
    if (!await crypto.subtle.verify('Ed25519', publicKey, signature, recoveryCheck)) {
      throw new Error('Backup does not match the Solana address')
    }
    return await use(value.address, privateKey)
  } finally {
    secret.fill(0)
  }
}
import { getTransactionEncoder } from '@solana/kit'
import { compileSponsoredDeposit, reviewSponsoredDeposit } from './sponsored.mjs'


export function recoverSolanaAddress(backup, password) {
  return withSolanaWallet(backup, password, address => address)
}

export async function signSponsoredDeposit(backup, password, order) {
  const reviewed = reviewSponsoredDeposit(order)
  return withSolanaWallet(backup, password, async (address, privateKey) => {
    if (reviewed.wallet !== address || Date.now() >= reviewed.expiresAt) {
      throw new Error('The backup does not match the order or the quote expired')
    }
    const transaction = await compileSponsoredDeposit(reviewed)
    const signature = new Uint8Array(await crypto.subtle.sign('Ed25519', privateKey, transaction.messageBytes))
    const bytes = getTransactionEncoder().encode({
      ...transaction, signatures: { ...transaction.signatures, [address]: signature }
    })
    return { version: 1, id: reviewed.id, cluster: reviewed.cluster, transaction: btoa(String.fromCharCode(...bytes)) }
  })
}
