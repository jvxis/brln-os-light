import { useEffect, useState } from 'react'
import { getLndConfig, updateLndConfig, updateLndRawConfig } from '../api'

export default function LndConfig() {
  const [config, setConfig] = useState<any>(null)
  const [alias, setAlias] = useState('')
  const [minChan, setMinChan] = useState('')
  const [maxChan, setMaxChan] = useState('')
  const [raw, setRaw] = useState('')
  const [advanced, setAdvanced] = useState(false)
  const [status, setStatus] = useState('')

  useEffect(() => {
    getLndConfig().then((data: any) => {
      setConfig(data)
      setAlias(data.current.alias || '')
      setMinChan(data.current.min_channel_size_sat?.toString() || '')
      setMaxChan(data.current.max_channel_size_sat?.toString() || '')
      setRaw(data.raw_user_conf || '')
    }).catch(() => null)
  }, [])

  const handleSave = async () => {
    setStatus('Saving...')
    try {
      await updateLndConfig({
        alias,
        min_channel_size_sat: Number(minChan || 0),
        max_channel_size_sat: Number(maxChan || 0),
        apply_now: true
      })
      setStatus('Saved and applied.')
    } catch {
      setStatus('Save failed.')
    }
  }

  const handleSaveRaw = async () => {
    setStatus('Saving advanced config...')
    try {
      await updateLndRawConfig({ raw_user_conf: raw, apply_now: true })
      setStatus('Advanced config applied.')
    } catch {
      setStatus('Advanced config failed.')
    }
  }

  return (
    <section className="space-y-6">
      <div className="section-card">
        <h2 className="text-2xl font-semibold">LND configuration</h2>
        <p className="text-fog/60">Edit supported values in /data/lnd/lnd.conf.</p>
        <p className="text-fog/50 text-sm">Advanced editor rewrites the full lnd.conf.</p>
        {status && <p className="text-sm text-brass mt-4">{status}</p>}
      </div>

      <div className="section-card space-y-4">
        <div className="flex items-center justify-between">
          <h3 className="text-lg font-semibold">Basic settings</h3>
          <button className="btn-secondary" onClick={() => setAdvanced((v) => !v)}>
            {advanced ? 'Hide advanced' : 'Show advanced'}
          </button>
        </div>
        <div className="grid gap-4 lg:grid-cols-3">
          <input className="input-field" placeholder="Alias" value={alias} onChange={(e) => setAlias(e.target.value)} />
          <input className="input-field" placeholder="Min channel size (sat)" value={minChan} onChange={(e) => setMinChan(e.target.value)} />
          <input className="input-field" placeholder="Max channel size (sat)" value={maxChan} onChange={(e) => setMaxChan(e.target.value)} />
        </div>
        <button className="btn-primary" onClick={handleSave}>Save and restart LND</button>
      </div>

      {advanced && (
        <div className="section-card space-y-4">
          <h3 className="text-lg font-semibold">Advanced editor</h3>
          <textarea className="input-field min-h-[180px]" value={raw} onChange={(e) => setRaw(e.target.value)} />
          <button className="btn-secondary" onClick={handleSaveRaw}>Apply advanced config</button>
        </div>
      )}

      {!config && <p className="text-fog/60">Loading config...</p>}
    </section>
  )
}
