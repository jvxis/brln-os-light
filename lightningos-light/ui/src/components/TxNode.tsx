import { Handle, Position, type NodeProps } from 'reactflow'
import { useTranslation } from 'react-i18next'
import { PALETTE, barGlow, fillBarGradient } from '../utils/utxoStyles'

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
  if (!bar.isOurs) return PALETTE.external
  if (side === 'out' && bar.isCurrentUtxo) return PALETTE.oursLive
  return PALETTE.oursSpent
}

function fmtSats(value: number) {
  return value.toLocaleString()
}

function fmtRelTime(ts: number, t: (key: string, opts?: any) => string) {
  if (!ts || ts <= 0) return ''
  const now = Date.now() / 1000
  const diff = now - ts
  if (diff < 60) return t('walletFlow.txNode.timeJustNow')
  if (diff < 3600) return t('walletFlow.txNode.timeMinAgo', { n: Math.floor(diff / 60) })
  if (diff < 86400) return t('walletFlow.txNode.timeHourAgo', { n: Math.floor(diff / 3600) })
  if (diff < 30 * 86400) return t('walletFlow.txNode.timeDayAgo', { n: Math.floor(diff / 86400) })
  if (diff < 365 * 86400) return t('walletFlow.txNode.timeMonthAgo', { n: Math.floor(diff / (30 * 86400)) })
  return t('walletFlow.txNode.timeYearAgo', { n: Math.floor(diff / (365 * 86400)) })
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

// Max bars rendered before collapsing the long tail into "+N more".
//   - Outputs cap at 10 for internal txs, 5 for leaf txs (no known inputs).
//   - Inputs cap at 5 universally; this auto-prunes the left-column
//     ancestor-box stack since externals feeding hidden input bars get
//     culled by the keepTxs filter below.
export const MAX_OUTPUT_BARS = 10
export const MAX_OUTPUT_BARS_LEAF = 5
export const MAX_INPUT_BARS = 5

// Legacy aliases kept so anything else importing these still builds.
export const MAX_BARS_PER_SIDE = MAX_OUTPUT_BARS
export const MAX_BARS_PER_SIDE_LEAF = MAX_OUTPUT_BARS_LEAF

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
  const { t } = useTranslation()
  const shortTxid = `${data.txid.slice(0, 8)}…${data.txid.slice(-6)}`
  const heightHint =
    data.blockHeight > 0
      ? t('walletFlow.txNode.heightHint', { height: data.blockHeight.toLocaleString() })
      : t('walletFlow.txNode.mempool')

  const liveCount = data.outputs.filter((o) => o.isCurrentUtxo).length
  const headerBorder = data.isExternal
    ? PALETTE.external
    : liveCount > 0
    ? PALETTE.oursLive
    : PALETTE.oursSpent
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
    const title = `${
      bar.amount > 0
        ? t('walletFlow.txNode.titleAmountSat', { amount: fmtSats(bar.amount) })
        : t('walletFlow.txNode.titleAmountUnknown')
    }${bar.address ? '\n' + bar.address : ''}${bar.isOurs ? '\n' + t('walletFlow.txNode.titleYours') : ''}`

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
                  background: fillBarGradient(color),
                  boxShadow: barGlow(color, bar.isOurs)
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
                  background: fillBarGradient(color),
                  boxShadow: barGlow(color, bar.isOurs)
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
          {side === 'in' ? t('walletFlow.txNode.noInputs') : t('walletFlow.txNode.noOutputs')}
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
          title={
            hiddenSat > 0
              ? t('walletFlow.txNode.collapsedTooltipWithSats', { count: hiddenCount, sats: fmtSats(hiddenSat) })
              : t('walletFlow.txNode.collapsedTooltip', { count: hiddenCount })
          }
        >
          {hiddenSat > 0
            ? t('walletFlow.txNode.moreWithSats', { count: hiddenCount, sats: fmtSats(hiddenSat) })
            : t('walletFlow.txNode.more', { count: hiddenCount })}
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
            {data.isExternal ? t('walletFlow.txNode.externalTx') : heightHint}
            {data.timestamp > 0 ? ` · ${fmtRelTime(data.timestamp, t)}` : ''}
            {data.feeSat > 0
              ? ' · ' + t('walletFlow.txNode.feeHint', { fee: fmtSats(data.feeSat) })
              : ''}
          </div>
        </div>
        {!data.isExternal && (
          <div className="text-right text-[10px]">
            {liveCount > 0 ? (
              <span className="text-glow">
                {t('walletFlow.txNode.liveSummary', {
                  count: liveCount,
                  sats: fmtSats(data.outputs.filter((o) => o.isCurrentUtxo).reduce((s, o) => s + o.amount, 0))
                })}
              </span>
            ) : (
              <span className="text-brass">{t('walletFlow.txNode.spent')}</span>
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
