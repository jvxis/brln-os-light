export type UtxoLike = {
  outpoint: string
  address?: string
  address_type?: string
  amount_sat?: number
}

export type PrivacyWarning = {
  severity: 'info' | 'warn'
  message: string
}

const DUST_VBYTES_PER_INPUT = 68
const DUST_RATIO_THRESHOLD = 0.05

export function computePrivacyWarnings(
  selection: UtxoLike[],
  options?: { mode?: 'spend' | 'consolidate'; satPerVbyte?: number }
): PrivacyWarning[] {
  if (selection.length < 2) return []

  const warnings: PrivacyWarning[] = []
  const addresses = new Set(selection.map((u) => (u.address || '').trim()).filter(Boolean))
  const addressTypes = new Set(selection.map((u) => (u.address_type || '').trim()).filter(Boolean))

  if (addresses.size > 1) {
    warnings.push({
      severity: 'warn',
      message: `Mixing UTXOs from ${addresses.size} addresses will link them on-chain (common-input ownership heuristic).`
    })
  }
  if (addressTypes.size > 1) {
    warnings.push({
      severity: 'warn',
      message: `Mixing ${addressTypes.size} address types (${Array.from(addressTypes).join(', ')}) reveals coin control.`
    })
  }

  if (options?.mode === 'consolidate' && options.satPerVbyte && options.satPerVbyte > 0) {
    const totalSat = selection.reduce((acc, u) => acc + (u.amount_sat || 0), 0)
    if (totalSat > 0) {
      const approxFee = DUST_VBYTES_PER_INPUT * selection.length * options.satPerVbyte
      const ratio = approxFee / totalSat
      if (ratio >= DUST_RATIO_THRESHOLD) {
        warnings.push({
          severity: 'warn',
          message: `Fee (~${approxFee.toLocaleString()} sats) would consume ${(ratio * 100).toFixed(1)}% of the consolidated value at ${options.satPerVbyte} sat/vB. Consider waiting for lower fees.`
        })
      }
    }
  }

  return warnings
}
