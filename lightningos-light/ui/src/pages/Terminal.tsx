import { useEffect, useState } from 'react'
import { getTerminalStatus } from '../api'

type TerminalStatus = {
  enabled: boolean
  credential?: string
  allow_write?: boolean
  port?: number
}

export default function Terminal() {
  const [status, setStatus] = useState<TerminalStatus | null>(null)
  const [statusMessage, setStatusMessage] = useState('')

  const copyToClipboard = async (value: string) => {
    if (!value) return
    try {
      await navigator.clipboard.writeText(value)
    } catch {
      // ignore copy failures
    }
  }

  const parseCredential = (raw?: string) => {
    if (!raw) return { user: '', pass: '' }
    const parts = raw.split(':')
    if (parts.length < 2) return { user: raw, pass: '' }
    const user = parts.shift() || ''
    const pass = parts.join(':')
    return { user, pass }
  }

  const credential = parseCredential(status?.credential)

  useEffect(() => {
    let mounted = true
    getTerminalStatus()
      .then((data: TerminalStatus) => {
        if (!mounted) return
        setStatus(data)
        setStatusMessage('')
      })
      .catch((err: any) => {
        if (!mounted) return
        setStatus(null)
        setStatusMessage(err?.message || 'Terminal status unavailable')
      })
    return () => {
      mounted = false
    }
  }, [])

  return (
    <div className="space-y-6">
      <div className="rounded-3xl border border-white/10 bg-ink/60 p-6 shadow-panel">
        <div className="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
          <div>
            <p className="text-sm uppercase tracking-[0.2em] text-fog/60">Terminal</p>
            <h2 className="text-2xl font-semibold text-white">LightningOS Terminal</h2>
            {statusMessage && (
              <p className="mt-2 text-sm text-brass">{statusMessage}</p>
            )}
            {status && (
              <div className="mt-4 space-y-2 text-sm text-fog/70">
                <div className="flex items-center gap-2">
                  <span className="text-fog/50">Status</span>
                  <span>{status.enabled ? 'Enabled' : 'Disabled'}</span>
                  {status.allow_write && (
                    <span className="rounded-full border border-amber-400/30 bg-amber-500/15 px-2 py-0.5 text-[11px] uppercase text-amber-200">
                      write
                    </span>
                  )}
                </div>
                {!status.enabled && (
                  <p className="text-brass">
                    Terminal disabled. Set `TERMINAL_ENABLED=1` in `/etc/lightningos/secrets.env`.
                  </p>
                )}
                {(status.credential || credential.pass) && (
                  <div className="flex flex-wrap items-center gap-4">
                    <div className="flex items-center gap-2">
                      <span className="text-fog/50">User</span>
                      <span className="font-mono text-fog/80">{credential.user || 'terminal'}</span>
                    </div>
                    {credential.pass && (
                      <div className="flex items-center gap-2">
                        <span className="text-fog/50">Password</span>
                        <span className="font-mono text-fog/80">{credential.pass}</span>
                        <button
                          className="text-fog/50 hover:text-fog"
                          onClick={() => copyToClipboard(credential.pass)}
                          title="Copy password"
                          aria-label="Copy password"
                        >
                          <svg viewBox="0 0 24 24" className="h-4 w-4" fill="none" stroke="currentColor" strokeWidth="1.6">
                            <rect x="9" y="9" width="11" height="11" rx="2" />
                            <rect x="4" y="4" width="11" height="11" rx="2" />
                          </svg>
                        </button>
                      </div>
                    )}
                  </div>
                )}
              </div>
            )}
          </div>
          <a className="btn-secondary" href="/terminal/" target="_blank" rel="noreferrer">
            Open in new tab
          </a>
        </div>
      </div>

      <div className="rounded-3xl border border-white/10 bg-ink/70 shadow-panel overflow-hidden">
        <iframe
          title="LightningOS Terminal"
          src="/terminal/"
          className="w-full h-[70vh] bg-black"
        />
      </div>
    </div>
  )
}
