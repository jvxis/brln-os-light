import { type EdgeProps } from 'reactflow'

type SankeyEdgeData = {
  // ribbon width in pixels at both endpoints (we keep source/target equal
  // because Bitcoin outpoints conserve value end-to-end)
  height: number
  // primary color used for the ribbon gradient anchors
  color: string
  // 0..1 opacity multiplier; ours-flows brighter than external
  opacity?: number
}

// SankeyEdge renders a filled cubic-bezier ribbon between two reactflow
// handles — the mempool.space-style "thick flow" look. The ribbon's
// height is constant from source to target (Bitcoin doesn't lose value
// in transit). The top and bottom edges of the ribbon are mirror cubics
// so the silhouette is symmetric.
export default function SankeyEdge({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  data,
  selected
}: EdgeProps<SankeyEdgeData>) {
  const h = Math.max(4, data?.height ?? 8)
  const halfH = h / 2
  const dx = targetX - sourceX
  const sweep = Math.max(40, Math.abs(dx) * 0.5)
  const cpx1 = sourceX + sweep
  const cpx2 = targetX - sweep

  // Top edge of the ribbon: source-top → target-top
  const top = `M ${sourceX},${sourceY - halfH} C ${cpx1},${sourceY - halfH} ${cpx2},${targetY - halfH} ${targetX},${targetY - halfH}`
  // Down the target side, back along bottom edge, close to source
  const bottom = `L ${targetX},${targetY + halfH} C ${cpx2},${targetY + halfH} ${cpx1},${sourceY + halfH} ${sourceX},${sourceY + halfH} Z`
  const path = `${top} ${bottom}`

  const gradId = `sankey-grad-${id.replace(/[^a-zA-Z0-9_-]/g, '_')}`
  const color = data?.color ?? '#14b8a6'
  const accent = '#22d3ee'
  const baseOpacity = (data?.opacity ?? 0.85) * (selected ? 1.1 : 1)

  return (
    <g>
      <defs>
        <linearGradient id={gradId} x1="0%" y1="0%" x2="100%" y2="0%">
          <stop offset="0%" stopColor={color} stopOpacity={Math.min(1, baseOpacity)} />
          <stop offset="50%" stopColor={accent} stopOpacity={Math.min(1, baseOpacity + 0.05)} />
          <stop offset="100%" stopColor={color} stopOpacity={Math.min(1, baseOpacity)} />
        </linearGradient>
      </defs>
      <path
        d={path}
        fill={`url(#${gradId})`}
        stroke={color}
        strokeOpacity={0.45}
        strokeWidth={0.5}
        style={{ pointerEvents: 'auto' }}
      />
    </g>
  )
}
