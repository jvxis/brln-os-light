import { Handle, Position, type NodeProps } from 'reactflow'

export type TxBar = {
  // For inputs this is the *vin index*; for outputs it is the vout index.
  // Used to compose the reactflow Handle id.
  index: number
  amount: number
  address?: string
  isOurs: boolean
  isCurrentUtxo?: boolean
  // For inputs: the prior txid (placeholder for click-to-trace later).
  refTxid?: string
}

export type TxNodeData = {
  txid: string
  blockHeight: number
  confirmations: number
  timestamp: number
  amountSat: number
  feeSat: number
  label?: string
  isExternal: boolean
  inputs: TxBar[]
  outputs: TxBar[]
  // shared scale across the whole graph so bars are comparable
  minBarAmount: number
  maxBarAmount: number
  // counts of bars hidden from the visible inputs/outputs columns
  hiddenInputCount?: number
  hiddenInputSat?: number
  hiddenOutputCount?: number
  hiddenOutputSat?: number
}

export const TX_NODE_WIDTH = 340

// Mempool-style: bar height is itself proportional to sat amount (no fixed
// slot). Each bar contributes its own height to the total node height.
const BAR_MIN_HEIGHT = 14
const BAR_MAX_HEIGHT = 64
const BAR_GAP = 6
const ADDRESS_ROW_HEIGHT = 12 // text label under each bar
const HEADER_HEIGHT = 56
const COL_PADDING = 12

export function barHeightFor(amount: number, min: number, max: number) {
  return barHeight(amount, min, max)
}

function barHeight(amount: number, min: number, max: number) {
  if (amount <= 0) return BAR_MIN_HEIGHT
  if (min === max || max <= 0) return Math.round((BAR_MIN_HEIGHT + BAR_MAX_HEIGHT) / 2)
  const sqrtMin = Math.sqrt(Math.max(min, 1))
  const sqrtMax = Math.sqrt(max)
  const sqrtNow = Math.sqrt(Math.max(amount, 1))
  const t = (sqrtNow - sqrtMin) / (sqrtMax - sqrtMin)
  return Math.round(BAR_MIN_HEIGHT + t * (BAR_MAX_HEIGHT - BAR_MIN_HEIGHT))
}

function barColor(bar: TxBar, side: 'in' | 'out') {
  if (!bar.isOurs) return '#475569' // slate-600 — external
  if (side === 'out' && bar.isCurrentUtxo) return '#14b8a6' // teal — still live
  return '#f59e0b' // amber — ours moved on
}

function fmtSats(value: number) {
  return value.toLocaleString()
}

function fmtRelTime(ts: number) {
  if (!ts || ts <= 0) return ''
  const now = Date.now() / 1000
  const diff = now - ts
  if (diff < 60) return 'just now'
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`
  if (diff < 30 * 86400) return `${Math.floor(diff / 86400)}d ago`
  if (diff < 365 * 86400) return `${Math.floor(diff / (30 * 86400))}mo ago`
  return `${Math.floor(diff / (365 * 86400))}y ago`
}

function fmtAbsDate(ts: number) {
  if (!ts || ts <= 0) return ''
  const d = new Date(ts * 1000)
  return d.toLocaleString(undefined, {
    year: '2-digit',
    month: 'short',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit'
  })
}

function shortAddr(addr?: string) {
  if (!addr) return ''
  if (addr.length <= 14) return addr
  return `${addr.slice(0, 6)}…${addr.slice(-6)}`
}

function totalColumnHeight(bars: TxBar[], min: number, max: number) {
  if (bars.length === 0) return BAR_MIN_HEIGHT + ADDRESS_ROW_HEIGHT
  let total = COL_PADDING
  for (const b of bars) {
    total += barHeight(b.amount, min, max) + ADDRESS_ROW_HEIGHT + BAR_GAP
  }
  return total + COL_PADDING
}

export function computeTxNodeHeight(data: TxNodeData) {
  const footerSlot = 16
  const inH =
    totalColumnHeight(data.inputs, data.minBarAmount, data.maxBarAmount) +
    ((data.hiddenInputCount ?? 0) > 0 ? footerSlot : 0)
  const outH =
    totalColumnHeight(data.outputs, data.minBarAmount, data.maxBarAmount) +
    ((data.hiddenOutputCount ?? 0) > 0 ? footerSlot : 0)
  return HEADER_HEIGHT + Math.max(inH, outH)
}

// Max bars rendered per side; the rest get summarized as "+N more".
export const MAX_BARS_PER_SIDE = 25

// Compute the y-offset (from top of the bars block) where the *center* of
// the bar at index `i` sits. Used by edges so handles attach to the bar's
// midline rather than a fixed slot.
function barCenterOffsets(bars: TxBar[], min: number, max: number): number[] {
  const offsets: number[] = []
  let y = COL_PADDING
  for (const b of bars) {
    const h = barHeight(b.amount, min, max)
    offsets.push(y + h / 2)
    y += h + ADDRESS_ROW_HEIGHT + BAR_GAP
  }
  return offsets
}

export default function TxNode({ data, selected }: NodeProps<TxNodeData>) {
  const shortTxid = `${data.txid.slice(0, 8)}…${data.txid.slice(-6)}`
  const heightHint = data.blockHeight > 0 ? `h ${data.blockHeight.toLocaleString()}` : 'mempool'

  const liveCount = data.outputs.filter((o) => o.isCurrentUtxo).length
  const headerBorder = data.isExternal ? '#475569' : liveCount > 0 ? '#14b8a6' : '#f59e0b'
  const headerGradient = data.isExternal
    ? 'linear-gradient(180deg, rgba(71,85,105,0.20) 0%, rgba(71,85,105,0.06) 100%)'
    : liveCount > 0
    ? 'linear-gradient(180deg, rgba(20,184,166,0.22) 0%, rgba(20,184,166,0.05) 100%)'
    : 'linear-gradient(180deg, rgba(245,158,11,0.18) 0%, rgba(245,158,11,0.04) 100%)'

  const inOffsets = barCenterOffsets(data.inputs, data.minBarAmount, data.maxBarAmount)
  const outOffsets = barCenterOffsets(data.outputs, data.minBarAmount, data.maxBarAmount)

  const renderBar = (bar: TxBar, side: 'in' | 'out', offsetY: number) => {
    const h = barHeight(bar.amount, data.minBarAmount, data.maxBarAmount)
    const color = barColor(bar, side)
    const handleId = `${side}-${bar.index}`
    const handleStyle: React.CSSProperties = {
      background: color,
      width: 4,
      height: Math.max(h * 0.6, 8),
      border: '0 none',
      top: offsetY,
      borderRadius: 1
    }
    const title = `${bar.amount > 0 ? fmtSats(bar.amount) + ' sat' : 'amount unknown'}${
      bar.address ? '\n' + bar.address : ''
    }${bar.isOurs ? '\n(yours)' : ''}`

    return (
      <div
        key={`${side}-${bar.index}`}
        className="relative"
        style={{ marginBottom: BAR_GAP }}
        title={title}
      >
        {side === 'in' ? (
          <>
            <Handle id={handleId} type="target" position={Position.Left} style={handleStyle} />
            <div className="flex items-center gap-2 pl-2">
              <div
                className="rounded-md"
                style={{
                  width: 18,
                  height: h,
                  background: `linear-gradient(180deg, ${color} 0%, ${color}aa 100%)`,
                  boxShadow: bar.isOurs ? `0 0 0 1px ${color}, 0 0 8px ${color}55` : undefined
                }}
              />
              <div className="flex flex-col leading-tight min-w-0">
                <span className="text-[10px] font-mono text-white truncate" style={{ maxWidth: 120 }}>
                  {bar.amount > 0 ? fmtSats(bar.amount) : '—'}
                </span>
                <span className="text-[9px] font-mono text-white/45 truncate" style={{ maxWidth: 120 }}>
                  {shortAddr(bar.address)}
                </span>
              </div>
            </div>
          </>
        ) : (
          <>
            <div className="flex items-center justify-end gap-2 pr-2">
              <div className="flex flex-col items-end leading-tight min-w-0">
                <span className="text-[10px] font-mono text-white truncate" style={{ maxWidth: 120 }}>
                  {bar.amount > 0 ? fmtSats(bar.amount) : '—'}
                </span>
                <span className="text-[9px] font-mono text-white/45 truncate" style={{ maxWidth: 120 }}>
                  {shortAddr(bar.address)}
                </span>
              </div>
              <div
                className="rounded-md"
                style={{
                  width: 18,
                  height: h,
                  background: `linear-gradient(180deg, ${color} 0%, ${color}aa 100%)`,
                  boxShadow: bar.isOurs ? `0 0 0 1px ${color}, 0 0 8px ${color}55` : undefined
                }}
              />
            </div>
            <Handle id={handleId} type="source" position={Position.Right} style={handleStyle} />
          </>
        )}
      </div>
    )
  }

  // Build a stacked column where each bar contributes its own height + an
  // address label slot. We render bars sequentially and let the flex column
  // accumulate height naturally.
  const renderColumn = (
    bars: TxBar[],
    side: 'in' | 'out',
    offsets: number[],
    hiddenCount: number,
    hiddenSat: number
  ) => (
    <div className="flex-1 relative" style={{ padding: `${COL_PADDING}px 0` }}>
      {bars.length === 0 ? (
        <div
          className={`text-[10px] text-white/35 px-2 ${side === 'out' ? 'text-right' : ''}`}
        >
          {side === 'in' ? 'no known inputs' : 'no outputs'}
        </div>
      ) : (
        bars.map((b, i) => renderBar(b, side, offsets[i] ?? 0))
      )}
      {hiddenCount > 0 && (
        <div
          className={`relative mt-1 text-[10px] text-white/55 px-2 py-1 ${side === 'out' ? 'text-right' : ''}`}
          style={{
            background: 'rgba(148,163,184,0.10)',
            borderRadius: 6
          }}
          title={`${hiddenCount} bar(s) collapsed${hiddenSat > 0 ? ` · ${fmtSats(hiddenSat)} sat total` : ''}`}
        >
          + {hiddenCount} more {hiddenSat > 0 ? `· ${fmtSats(hiddenSat)} sat` : ''}
          {side === 'in' ? (
            <Handle
              id="in-more"
              type="target"
              position={Position.Left}
              style={{
                background: '#94a3b8',
                width: 6,
                height: 12,
                border: '0 none',
                borderRadius: 1
              }}
            />
          ) : (
            <Handle
              id="out-more"
              type="source"
              position={Position.Right}
              style={{
                background: '#94a3b8',
                width: 6,
                height: 12,
                border: '0 none',
                borderRadius: 1
              }}
            />
          )}
        </div>
      )}
    </div>
  )

  return (
    <div
      style={{
        width: TX_NODE_WIDTH,
        background: 'linear-gradient(180deg, #0b1220 0%, #0f172a 100%)',
        border: `2px solid ${selected ? '#fafafa' : headerBorder}`,
        borderRadius: 14,
        overflow: 'hidden',
        boxShadow: '0 12px 30px rgba(0,0,0,0.45)'
      }}
    >
      <div
        className="flex items-center justify-between px-3 py-2"
        style={{ background: headerGradient, borderBottom: `1px solid ${headerBorder}` }}
      >
        <div className="leading-tight">
          <div
            className="font-mono text-[11px] text-white"
            title={data.timestamp > 0 ? fmtAbsDate(data.timestamp) : undefined}
          >
            {shortTxid}
          </div>
          <div className="text-[9px] text-white/55">
            {data.isExternal ? 'external tx' : heightHint}
            {data.timestamp > 0 ? ` · ${fmtRelTime(data.timestamp)}` : ''}
            {data.feeSat > 0 ? ` · fee ${fmtSats(data.feeSat)}s` : ''}
          </div>
        </div>
        {!data.isExternal && (
          <div className="text-right text-[10px]">
            {liveCount > 0 ? (
              <span className="text-glow">
                {liveCount} live · {fmtSats(data.outputs.filter((o) => o.isCurrentUtxo).reduce((s, o) => s + o.amount, 0))}s
              </span>
            ) : (
              <span className="text-brass">spent</span>
            )}
          </div>
        )}
      </div>
      <div className="flex relative">
        {renderColumn(
          data.inputs,
          'in',
          inOffsets,
          data.hiddenInputCount ?? 0,
          data.hiddenInputSat ?? 0
        )}
        <div
          className="absolute top-0 bottom-0 left-1/2 -translate-x-1/2 pointer-events-none"
          style={{ width: 1, background: 'rgba(148,163,184,0.10)' }}
        />
        {renderColumn(
          data.outputs,
          'out',
          outOffsets,
          data.hiddenOutputCount ?? 0,
          data.hiddenOutputSat ?? 0
        )}
      </div>
    </div>
  )
}
