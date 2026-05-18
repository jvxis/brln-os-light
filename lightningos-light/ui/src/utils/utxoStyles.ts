// Shared palette + style helpers used by both the wallet-flow TxNode bars and
// the coin-selector UtxoCanvas blocks. Keep style decisions for "ours" /
// "external" / group coloring in here so the two views never drift visually.

export const PALETTE = {
  oursLive: '#14b8a6', // teal — ours still unspent
  oursSpent: '#f59e0b', // amber — ours, moved on
  external: '#64748b', // slate — not ours
  locked: '#94a3b8', // muted slate — locked from coinselect
  /** color cycle for user-created UTXO groups */
  groupCycle: [
    '#14b8a6',
    '#f59e0b',
    '#a855f7',
    '#ec4899',
    '#38bdf8',
    '#f97316',
    '#84cc16',
    '#facc15'
  ]
} as const

export type BarKind = 'ours-live' | 'ours-spent' | 'external' | 'locked'

/** Pick the base color for a bar/block. Group color wins if present. */
export function pickBarColor(kind: BarKind, groupColor?: string): string {
  if (groupColor) return groupColor
  switch (kind) {
    case 'ours-live':
      return PALETTE.oursLive
    case 'ours-spent':
      return PALETTE.oursSpent
    case 'locked':
      return PALETTE.locked
    default:
      return PALETTE.external
  }
}

/** Deterministic palette pick from a group id, used when group.color isn't set. */
export function colorFromGroupId(id: string): string {
  let h = 0
  for (let i = 0; i < id.length; i += 1) {
    h = (h << 5) - h + id.charCodeAt(i)
    h |= 0
  }
  return PALETTE.groupCycle[Math.abs(h) % PALETTE.groupCycle.length]
}

/**
 * Fill gradient for *small* color-filled bars (the per-vin / per-vout bars
 * inside TxNode). Top of the bar is solid color, bottom slightly transparent.
 */
export function fillBarGradient(color: string): string {
  return `linear-gradient(180deg, ${color} 0%, ${color}aa 100%)`
}

/**
 * Body gradient for *larger* blocks that frame their content (UtxoCanvas
 * cards). Dark navy → slate; the color is conveyed via border + accent
 * stripes, not by filling the whole block.
 */
export const CARD_BODY_GRADIENT = 'linear-gradient(180deg, #0b1220 0%, #0f172a 100%)'

/** Glow for a TxNode bar — only applied to "ours" bars. */
export function barGlow(color: string, isOurs: boolean): string | undefined {
  return isOurs ? `0 0 0 1px ${color}, 0 0 8px ${color}55` : undefined
}

/** Box shadow for a UtxoCanvas card — always glowing softly, brighter when selected. */
export function cardShadow(color: string, isSelected: boolean): string {
  return isSelected
    ? `0 0 0 2px ${color} inset, 0 0 14px ${color}aa`
    : `0 0 10px ${color}55, 0 4px 14px rgba(0,0,0,0.45)`
}

/** Decorative accent stripe for the top edge of a UtxoCanvas card. */
export function accentStripeTop(color: string): string {
  return `linear-gradient(90deg, ${color} 0%, ${color}aa 50%, ${color}55 100%)`
}

/** Decorative accent stripe for the left edge of a UtxoCanvas card. */
export function accentStripeLeft(color: string): string {
  return `linear-gradient(180deg, ${color}cc 0%, ${color}55 100%)`
}
