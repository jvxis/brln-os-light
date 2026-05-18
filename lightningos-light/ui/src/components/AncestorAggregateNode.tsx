import { Handle, Position } from 'reactflow'
import { useTranslation } from 'react-i18next'

export type AncestorAggregateData = {
  count: number
  totalSat: number
}

export const ANCESTOR_AGG_WIDTH = 180

// Pseudo-node that stands in for a bunch of dropped external ancestors
// feeding the same consumer. One ribbon comes out the right side and
// lands on the consumer's `in-more` aggregate input handle.
export default function AncestorAggregateNode({ data }: { data: AncestorAggregateData }) {
  const { t } = useTranslation()
  const sats = data.totalSat.toLocaleString()
  return (
    <div
      className="rounded-2xl border border-dashed border-brass/50 bg-brass/5 text-fog/80 px-3 py-2 shadow-panel"
      style={{ width: ANCESTOR_AGG_WIDTH }}
    >
      <p className="text-[10px] uppercase tracking-[0.2em] text-brass/80">
        {t('walletFlow.ancestorAggKicker', { defaultValue: 'Ancestors' })}
      </p>
      <p className="mt-1 text-sm font-semibold">
        {t('walletFlow.ancestorAggCount', { count: data.count, defaultValue: '+{{count}} more' })}
      </p>
      <p className="mt-1 text-xs text-fog/55 font-mono">{sats} sat</p>
      <Handle
        id="out"
        type="source"
        position={Position.Right}
        style={{ background: '#b08642', width: 8, height: 8 }}
      />
    </div>
  )
}
