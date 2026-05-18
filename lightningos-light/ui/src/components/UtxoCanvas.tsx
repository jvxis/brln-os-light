import { useEffect, useMemo, useRef, useState } from 'react'
import clsx from '../utils/clsx'

export type CanvasUtxo = {
  outpoint: string
  txid: string
  vout: number
  address: string
  address_type: string
  amount_sat: number
  confirmations: number
  pk_script?: string
  label?: string
  tag?: string
  color?: string
  group_id?: string
  locked?: boolean
  lease_expiration?: number
}

export type CanvasGroup = {
  id: string
  name: string
  color: string
}

type Props = {
  utxos: CanvasUtxo[]
  groups: CanvasGroup[]
  selected: Set<string>
  onSelectionChange: (next: Set<string>) => void
  onAssignToGroup: (groupId: string, outpoints: string[]) => void
  onCreateGroupWith: (outpoints: string[], suggestedName?: string) => void
  formatSats: (value: number) => string
}

const MIN_SIDE_PX = 56
const MAX_SIDE_PX = 220
const GROUP_PALETTE = ['#14b8a6', '#f59e0b', '#a855f7', '#ec4899', '#38bdf8', '#f97316', '#84cc16', '#facc15']

function sideForAmount(amount: number, minAmount: number, maxAmount: number) {
  if (amount <= 0) return MIN_SIDE_PX
  if (maxAmount === minAmount) return (MIN_SIDE_PX + MAX_SIDE_PX) / 2
  const sqrtMin = Math.sqrt(minAmount)
  const sqrtMax = Math.sqrt(maxAmount)
  const sqrtNow = Math.sqrt(amount)
  const t = (sqrtNow - sqrtMin) / (sqrtMax - sqrtMin)
  return Math.round(MIN_SIDE_PX + t * (MAX_SIDE_PX - MIN_SIDE_PX))
}

function colorForUtxo(utxo: CanvasUtxo, groupMap: Map<string, CanvasGroup>) {
  if (utxo.group_id && groupMap.has(utxo.group_id)) {
    const g = groupMap.get(utxo.group_id)!
    if (g.color) return g.color
    const idx = Math.abs(hashCode(g.id)) % GROUP_PALETTE.length
    return GROUP_PALETTE[idx]
  }
  if (utxo.color) return utxo.color
  return '#64748b'
}

function hashCode(s: string) {
  let h = 0
  for (let i = 0; i < s.length; i += 1) {
    h = (h << 5) - h + s.charCodeAt(i)
    h |= 0
  }
  return h
}

export default function UtxoCanvas({
  utxos,
  groups,
  selected,
  onSelectionChange,
  onAssignToGroup,
  onCreateGroupWith,
  formatSats
}: Props) {
  const groupMap = useMemo(() => new Map(groups.map((g) => [g.id, g])), [groups])
  const { minAmount, maxAmount } = useMemo(() => {
    if (utxos.length === 0) return { minAmount: 0, maxAmount: 0 }
    let lo = Infinity
    let hi = 0
    for (const u of utxos) {
      if (u.amount_sat < lo) lo = u.amount_sat
      if (u.amount_sat > hi) hi = u.amount_sat
    }
    return { minAmount: lo, maxAmount: hi }
  }, [utxos])

  const containerRef = useRef<HTMLDivElement | null>(null)
  const [dragState, setDragState] = useState<{
    sourceOutpoint: string
    sources: string[]
    pointerX: number
    pointerY: number
    hoverOutpoint: string | null
  } | null>(null)

  const toggleSelect = (outpoint: string, additive: boolean) => {
    const next = new Set(additive ? selected : [])
    if (next.has(outpoint)) next.delete(outpoint)
    else next.add(outpoint)
    onSelectionChange(next)
  }

  useEffect(() => {
    if (!dragState) return
    const onMove = (e: PointerEvent) => {
      const target = document.elementFromPoint(e.clientX, e.clientY)
      const card = target?.closest('[data-utxo-outpoint]') as HTMLElement | null
      const hover = card?.dataset.utxoOutpoint || null
      setDragState((prev) => prev ? { ...prev, pointerX: e.clientX, pointerY: e.clientY, hoverOutpoint: hover } : prev)
    }
    const onUp = () => {
      setDragState((prev) => {
        if (!prev) return null
        const targetOutpoint = prev.hoverOutpoint
        if (targetOutpoint && targetOutpoint !== prev.sourceOutpoint && !prev.sources.includes(targetOutpoint)) {
          const target = utxos.find((u) => u.outpoint === targetOutpoint)
          const allOutpoints = Array.from(new Set([...prev.sources, targetOutpoint]))
          if (target?.group_id) {
            onAssignToGroup(target.group_id, allOutpoints)
          } else {
            const sourceGroupIds = new Set(
              prev.sources
                .map((op) => utxos.find((u) => u.outpoint === op)?.group_id)
                .filter((id): id is string => Boolean(id))
            )
            if (sourceGroupIds.size === 1) {
              const [gid] = Array.from(sourceGroupIds)
              onAssignToGroup(gid, allOutpoints)
            } else {
              onCreateGroupWith(allOutpoints)
            }
          }
        }
        return null
      })
    }
    window.addEventListener('pointermove', onMove)
    window.addEventListener('pointerup', onUp)
    window.addEventListener('pointercancel', onUp)
    return () => {
      window.removeEventListener('pointermove', onMove)
      window.removeEventListener('pointerup', onUp)
      window.removeEventListener('pointercancel', onUp)
    }
  }, [dragState, utxos, onAssignToGroup, onCreateGroupWith])

  const beginDrag = (e: React.PointerEvent, outpoint: string) => {
    if (e.button !== 0) return
    const sources = selected.has(outpoint) && selected.size > 1 ? Array.from(selected) : [outpoint]
    setDragState({
      sourceOutpoint: outpoint,
      sources,
      pointerX: e.clientX,
      pointerY: e.clientY,
      hoverOutpoint: null
    })
  }

  if (utxos.length === 0) {
    return <p className="text-sm text-fog/60">No UTXOs to display.</p>
  }

  return (
    <div
      ref={containerRef}
      className="relative flex flex-wrap gap-2 select-none"
      onPointerDown={(e) => {
        if ((e.target as HTMLElement).closest('[data-utxo-outpoint]')) return
        onSelectionChange(new Set())
      }}
    >
      {utxos.map((utxo) => {
        const side = sideForAmount(utxo.amount_sat, minAmount, maxAmount)
        const color = colorForUtxo(utxo, groupMap)
        const isSelected = selected.has(utxo.outpoint)
        const isDragSource = dragState?.sources.includes(utxo.outpoint)
        const isDropTarget = dragState && dragState.hoverOutpoint === utxo.outpoint && !isDragSource
        const groupName = utxo.group_id ? groupMap.get(utxo.group_id)?.name : ''
        return (
          <div
            key={utxo.outpoint}
            data-utxo-outpoint={utxo.outpoint}
            onPointerDown={(e) => {
              const additive = e.shiftKey || e.metaKey || e.ctrlKey
              toggleSelect(utxo.outpoint, additive)
              if (!utxo.locked) beginDrag(e, utxo.outpoint)
            }}
            style={{
              width: side,
              height: side,
              backgroundColor: `${color}22`,
              borderColor: isSelected ? '#fafafa' : color,
              boxShadow: isSelected ? `0 0 0 2px ${color} inset` : undefined,
              opacity: isDragSource ? 0.55 : 1,
              outline: isDropTarget ? '3px dashed #fafafa' : undefined
            }}
            className={clsx(
              'rounded-2xl border-2 p-2 flex flex-col justify-between cursor-pointer transition relative overflow-hidden',
              utxo.confirmations === 0 && 'animate-pulse',
              utxo.locked && 'opacity-70'
            )}
            title={`${utxo.outpoint}\n${utxo.address}`}
          >
            <div className="flex items-start justify-between gap-1 text-[10px] uppercase tracking-wide text-white/80">
              <span className="truncate" style={{ maxWidth: side - 36 }}>
                {utxo.label || groupName || utxo.address_type || ''}
              </span>
              <span className="flex items-center gap-1">
                {utxo.locked && <span title="locked">🔒</span>}
                {utxo.confirmations === 0 && <span title="unconfirmed">⏳</span>}
              </span>
            </div>
            <div className="text-sm font-semibold text-white text-right leading-tight">
              {formatSats(utxo.amount_sat)}
              <div className="text-[10px] font-normal text-white/70">sats</div>
            </div>
          </div>
        )
      })}
      {dragState && (
        <div
          className="pointer-events-none fixed z-50 -translate-x-1/2 -translate-y-1/2 rounded-xl border border-white/40 bg-ink/80 px-3 py-1 text-xs text-white shadow-lg"
          style={{ left: dragState.pointerX, top: dragState.pointerY }}
        >
          {dragState.sources.length > 1 ? `${dragState.sources.length} UTXOs` : 'Drop onto another UTXO to group'}
        </div>
      )}
    </div>
  )
}
