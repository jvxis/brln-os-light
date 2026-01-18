import { useEffect, useState } from 'react'
import { getLogs } from '../api'

const services = [
  { label: 'LND', value: 'lnd' },
  { label: 'Manager', value: 'lightningos-manager' },
  { label: 'Elements', value: 'lightningos-elements' },
  { label: 'Postgres', value: 'postgresql' }
]

export default function Logs() {
  const [service, setService] = useState('lnd')
  const [lines, setLines] = useState(200)
  const [data, setData] = useState<string[]>([])
  const [status, setStatus] = useState('')

  const load = async () => {
    setStatus('Loading logs...')
    try {
      const res = await getLogs(service, lines)
      setData(Array.isArray(res?.lines) ? res.lines : [])
      setStatus('')
    } catch (err: any) {
      setStatus(err?.message || 'Log fetch failed.')
    }
  }

  useEffect(() => {
    load()
  }, [service, lines])

  return (
    <section className="space-y-6">
      <div className="section-card">
        <h2 className="text-2xl font-semibold">Logs</h2>
        <p className="text-fog/60">Tail recent logs for systemd services.</p>
      </div>

      <div className="section-card space-y-4">
        <div className="flex flex-wrap gap-3">
          {services.map((item) => (
            <button
              key={item.value}
              className={service === item.value ? 'btn-primary' : 'btn-secondary'}
              onClick={() => setService(item.value)}
            >
              {item.label}
            </button>
          ))}
          <select className="input-field max-w-[140px]" value={lines} onChange={(e) => setLines(Number(e.target.value))}>
            {[200, 500, 1000].map((value) => (
              <option key={value} value={value}>{value} lines</option>
            ))}
          </select>
        </div>
        {status && <p className="text-sm text-brass">{status}</p>}
        <div className="bg-ink/70 border border-white/10 rounded-2xl p-4 text-xs font-mono whitespace-pre-wrap min-h-[280px]">
          {data.length ? data.join('\n') : 'No logs yet.'}
        </div>
      </div>
    </section>
  )
}
