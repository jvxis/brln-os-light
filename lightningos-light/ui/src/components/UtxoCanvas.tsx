import { useEffect, useMemo, useRef, useState } from 'react'
import {
  forceCenter,
  forceCollide,
  forceManyBody,
  forceSimulation,
  type Simulation,
  type SimulationNodeDatum
} from 'd3-force'
import clsx from '../utils/clsx'
import {
  CARD_BODY_GRADIENT,
  PALETTE,
  accentStripeLeft,
  accentStripeTop,
  cardShadow,
  colorFromGroupId
} from '../utils/utxoStyles'

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

const MIN_SIDE_PX = 96
const MAX_SIDE_PX = 160
const CANVAS_MIN_HEIGHT = 560
const BOUNDS_MARGIN_PX = 6

// A click-vs-drag threshold so a tiny pointer wobble while clicking doesn't
// register as a drag and pin the node in place.
const DRAG_THRESHOLD_PX = 4

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
    return colorFromGroupId(g.id)
  }
  if (utxo.color) return utxo.color
  if (utxo.locked) return PALETTE.locked
  return PALETTE.oursLive
}

// Internal node datum the simulation mutates in place.
type SimNode = SimulationNodeDatum & {
  outpoint: string
  side: number
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
  const [size, setSize] = useState({ width: 800, height: CANVAS_MIN_HEIGHT })

  // Observe container width; height is fixed by CSS so we don't recompute.
  useEffect(() => {
    if (!containerRef.current) return
    const ro = new ResizeObserver((entries) => {
      const cr = entries[0].contentRect
      setSize({ width: cr.width || 800, height: Math.max(cr.height, CANVAS_MIN_HEIGHT) })
    })
    ro.observe(containerRef.current)
    return () => ro.disconnect()
  }, [])

  // Simulation persists across renders; we only rebuild it when the UTXO set
  // changes. Mutating its nodes array directly is intentional — d3 expects it.
  const simRef = useRef<Simulation<SimNode, undefined> | null>(null)
  const nodesRef = useRef<SimNode[]>([])
  // Tick counter forces React to re-render after each sim tick; cheap with <100 nodes.
  const [, setTick] = useState(0)

  useEffect(() => {
    const cx = size.width / 2
    const cy = size.height / 2
    // Preserve positions for nodes that already exist so an incremental add
    // doesn't slingshot everything from scratch.
    const prevByOutpoint = new Map<string, SimNode>()
    for (const n of nodesRef.current) prevByOutpoint.set(n.outpoint, n)

    const nodes: SimNode[] = utxos.map((u) => {
      const prev = prevByOutpoint.get(u.outpoint)
      const side = sideForAmount(u.amount_sat, minAmount, maxAmount)
      return prev
        ? { ...prev, side }
        : {
            outpoint: u.outpoint,
            side,
            x: cx + (Math.random() - 0.5) * 40,
            y: cy + (Math.random() - 0.5) * 40,
            vx: 0,
            vy: 0
          }
    })
    nodesRef.current = nodes

    if (simRef.current) simRef.current.stop()
    simRef.current = forceSimulation(nodes)
      .force('center', forceCenter(cx, cy).strength(0.18))
      .force('charge', forceManyBody().strength(-16))
      .force('collide', forceCollide<SimNode>().radius((d) => d.side / 2 + 6).iterations(2))
      // Settle in ~15 ticks instead of ~300 so the browser stops re-rendering
      // quickly. velocityDecay = friction; high alphaDecay/alphaMin cuts the
      // simulation off when the layout is "good enough" instead of perfect.
      .alphaDecay(0.12)
      .alphaMin(0.04)
      .velocityDecay(0.55)
      .on('tick', () => {
        // Hard-clamp positions inside the container box. d3 only knows about
        // forces; without this, large repulsion can shove nodes off-screen,
        // especially when a node is being dragged or when the cluster is
        // bigger than the container.
        const w = size.width
        const h = size.height
        for (const n of nodes) {
          const r = n.side / 2 + BOUNDS_MARGIN_PX
          if (n.x != null) {
            if (n.x < r) n.x = r
            else if (n.x > w - r) n.x = w - r
          }
          if (n.y != null) {
            if (n.y < r) n.y = r
            else if (n.y > h - r) n.y = h - r
          }
        }
        setTick((t) => t + 1)
      })

    return () => {
      simRef.current?.stop()
    }
  }, [utxos, minAmount, maxAmount, size.width, size.height])

  // Drag/click state ----------------------------------------------------
  const dragRef = useRef<{
    outpoint: string
    startX: number
    startY: number
    moved: boolean
    additive: boolean
    pointerId: number
  } | null>(null)
  const [hoverDropOutpoint, setHoverDropOutpoint] = useState<string | null>(null)

  const toggleSelect = (outpoint: string, additive: boolean) => {
    const next = new Set(additive ? selected : [])
    if (selected.has(outpoint)) next.delete(outpoint)
    else next.add(outpoint)
    onSelectionChange(next)
  }

  const onNodePointerDown = (e: React.PointerEvent, utxo: CanvasUtxo) => {
    if (e.button !== 0) return
    const node = nodesRef.current.find((n) => n.outpoint === utxo.outpoint)
    if (!node) return
    dragRef.current = {
      outpoint: utxo.outpoint,
      startX: e.clientX,
      startY: e.clientY,
      moved: false,
      additive: e.shiftKey || e.metaKey || e.ctrlKey,
      pointerId: e.pointerId
    }
    ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
  }

  const onNodePointerMove = (e: React.PointerEvent, utxo: CanvasUtxo) => {
    const drag = dragRef.current
    if (!drag || drag.outpoint !== utxo.outpoint) return
    const dx = e.clientX - drag.startX
    const dy = e.clientY - drag.startY
    if (!drag.moved && Math.hypot(dx, dy) < DRAG_THRESHOLD_PX) return
    drag.moved = true
    const node = nodesRef.current.find((n) => n.outpoint === drag.outpoint)
    if (!node || !containerRef.current) return
    const rect = containerRef.current.getBoundingClientRect()
    // Pin the node under the cursor while dragging, clamped to bounds so the
    // user can't fling it off the canvas.
    const r = node.side / 2 + BOUNDS_MARGIN_PX
    const px = e.clientX - rect.left
    const py = e.clientY - rect.top
    node.fx = Math.max(r, Math.min(size.width - r, px))
    node.fy = Math.max(r, Math.min(size.height - r, py))
    // Only nudge the simulation briefly while dragging so it stays responsive
    // around the dragged node without re-energising the whole cluster.
    simRef.current?.alphaTarget(0.15).restart()
    // Hover detection for drop-on-target grouping.
    const target = document.elementFromPoint(e.clientX, e.clientY)
    const card = target?.closest('[data-utxo-outpoint]') as HTMLElement | null
    const hover = card?.dataset.utxoOutpoint || null
    setHoverDropOutpoint(hover && hover !== drag.outpoint ? hover : null)
  }

  const finalizeDrag = (e: React.PointerEvent, utxo: CanvasUtxo) => {
    const drag = dragRef.current
    if (!drag || drag.outpoint !== utxo.outpoint) return
    ;(e.currentTarget as HTMLElement).releasePointerCapture?.(e.pointerId)
    const node = nodesRef.current.find((n) => n.outpoint === drag.outpoint)
    if (!drag.moved) {
      toggleSelect(utxo.outpoint, drag.additive)
    } else if (hoverDropOutpoint) {
      const sources =
        selected.has(drag.outpoint) && selected.size > 1
          ? Array.from(selected)
          : [drag.outpoint]
      const allOutpoints = Array.from(new Set([...sources, hoverDropOutpoint]))
      const target = utxos.find((u) => u.outpoint === hoverDropOutpoint)
      if (target?.group_id) {
        onAssignToGroup(target.group_id, allOutpoints)
      } else {
        const sourceGroupIds = new Set(
          sources
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
    // Release the node so the simulation can re-equilibrate.
    if (node) {
      node.fx = null
      node.fy = null
    }
    // Stop the simulation hard on release; the cluster is already where the
    // user wants it. A small alphaDecay then resettles in a few ticks.
    simRef.current?.alphaTarget(0).alpha(0.15)
    setHoverDropOutpoint(null)
    dragRef.current = null
  }

  if (utxos.length === 0) {
    return <p className="text-sm text-fog/60">No UTXOs to display.</p>
  }

  return (
    <div
      ref={containerRef}
      className="relative select-none rounded-2xl"
      style={{ height: CANVAS_MIN_HEIGHT, background: 'radial-gradient(circle at 50% 50%, rgba(20,184,166,0.05) 0%, transparent 60%)' }}
      onPointerDown={(e) => {
        // Empty-area click clears selection.
        if ((e.target as HTMLElement).closest('[data-utxo-outpoint]')) return
        onSelectionChange(new Set())
      }}
    >
      {utxos.map((utxo) => {
        const node = nodesRef.current.find((n) => n.outpoint === utxo.outpoint)
        const side = node?.side ?? sideForAmount(utxo.amount_sat, minAmount, maxAmount)
        const x = node?.x ?? size.width / 2
        const y = node?.y ?? size.height / 2
        const color = colorForUtxo(utxo, groupMap)
        const isSelected = selected.has(utxo.outpoint)
        const isHoverDrop = hoverDropOutpoint === utxo.outpoint
        const groupName = utxo.group_id ? groupMap.get(utxo.group_id)?.name : ''
        const shortAddr = utxo.address
          ? utxo.address.length > 14
            ? `${utxo.address.slice(0, 6)}…${utxo.address.slice(-6)}`
            : utxo.address
          : ''
        return (
          <div
            key={utxo.outpoint}
            data-utxo-outpoint={utxo.outpoint}
            onPointerDown={(e) => onNodePointerDown(e, utxo)}
            onPointerMove={(e) => onNodePointerMove(e, utxo)}
            onPointerUp={(e) => finalizeDrag(e, utxo)}
            onPointerCancel={(e) => finalizeDrag(e, utxo)}
            style={{
              position: 'absolute',
              width: side,
              height: side,
              transform: `translate3d(${x - side / 2}px, ${y - side / 2}px, 0)`,
              background: CARD_BODY_GRADIENT,
              borderColor: isSelected ? '#fafafa' : color,
              boxShadow: cardShadow(color, isSelected),
              outline: isHoverDrop ? '3px dashed #fafafa' : undefined,
              cursor: 'grab',
              willChange: 'transform',
              transition: 'box-shadow 120ms ease, outline 120ms ease'
            }}
            className={clsx(
              'rounded-2xl border-2 overflow-hidden',
              utxo.confirmations === 0 && 'animate-pulse',
              utxo.locked && 'opacity-70'
            )}
            title={`${utxo.outpoint}\n${utxo.address}\n${utxo.amount_sat.toLocaleString()} sat`}
          >
            <div
              className="absolute left-0 right-0 top-0 h-1"
              style={{ background: accentStripeTop(color) }}
            />
            <div
              className="absolute left-0 top-0 bottom-0 w-1"
              style={{ background: accentStripeLeft(color) }}
            />
            <div className="flex h-full flex-col justify-between p-2 pl-3 pointer-events-none">
              <div className="flex items-start justify-between gap-1 text-[10px] uppercase tracking-wide text-white/75">
                <span className="truncate" style={{ maxWidth: side - 40 }}>
                  {utxo.label || groupName || utxo.address_type || ''}
                </span>
                <span className="flex items-center gap-1">
                  {utxo.locked && <span title="locked">🔒</span>}
                  {utxo.confirmations === 0 && <span title="unconfirmed">⏳</span>}
                </span>
              </div>
              <div className="leading-tight text-right">
                <div className="font-mono text-[12px] font-semibold text-white whitespace-nowrap">
                  {formatSats(utxo.amount_sat)}
                </div>
                <div className="text-[9px] font-mono text-white/55 truncate">{shortAddr || 'sats'}</div>
              </div>
            </div>
          </div>
        )
      })}
    </div>
  )
}
