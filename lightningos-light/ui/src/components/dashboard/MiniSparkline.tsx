import { useId } from 'react'
import { Area, AreaChart, ResponsiveContainer } from 'recharts'
import type { Tone } from './types'

type SparklineDatum = {
  value: number
}

type MiniSparklineProps = {
  data: SparklineDatum[]
  tone?: Tone
}

const toneColors: Record<Tone, string> = {
  ok: '#34d399',
  warn: '#f59e0b',
  danger: '#f87171',
  muted: '#94a3b8',
  info: '#38bdf8',
}

export default function MiniSparkline({ data, tone = 'info' }: MiniSparklineProps) {
  const gradientId = useId().replace(/:/g, '')
  const safeData = data.length > 0 ? data : [{ value: 0 }, { value: 0 }]
  const color = toneColors[tone]

  return (
    <div className="h-14 w-full">
      <ResponsiveContainer width="100%" height="100%">
        <AreaChart data={safeData} margin={{ top: 6, right: 0, left: 0, bottom: 0 }}>
          <defs>
            <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor={color} stopOpacity={0.55} />
              <stop offset="100%" stopColor={color} stopOpacity={0.04} />
            </linearGradient>
          </defs>
          <Area
            type="monotone"
            dataKey="value"
            stroke={color}
            strokeWidth={2}
            fill={`url(#${gradientId})`}
            fillOpacity={1}
            isAnimationActive={false}
          />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  )
}
