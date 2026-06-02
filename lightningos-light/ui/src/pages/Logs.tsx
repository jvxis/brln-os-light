import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getApps, getLogs } from '../api'

type AppInfo = {
  id: string
  installed: boolean
}

const baseServices = [
  { labelKey: 'logs.services.lnd', value: 'lnd' },
  { labelKey: 'logs.services.bitcoin', value: 'bitcoin' },
  { labelKey: 'logs.services.autofee', value: 'autofee' },
  { labelKey: 'logs.services.lndUpgrade', value: 'lnd-upgrade' },
  { labelKey: 'logs.services.appUpgrade', value: 'app-upgrade' },
  { labelKey: 'logs.services.manager', value: 'lightningos-manager' },
  { labelKey: 'logs.services.elements', value: 'lightningos-elements' },
  { labelKey: 'logs.services.peerswapd', value: 'lightningos-peerswapd' },
  { labelKey: 'logs.services.psweb', value: 'lightningos-psweb' },
  { labelKey: 'logs.services.postgres', value: 'postgresql' }
]

const fedimintServices = [
  { appID: 'fedimint-guardian', labelKey: 'logs.services.fedimintGuardian', value: 'fedimint-guardian' },
  { appID: 'fedimint-gateway', labelKey: 'logs.services.fedimintGateway', value: 'fedimint-gateway' }
]

export default function Logs() {
  const { t } = useTranslation()
  const [service, setService] = useState('lnd')
  const [lines, setLines] = useState(200)
  const [data, setData] = useState<string[]>([])
  const [status, setStatus] = useState('')
  const [loading, setLoading] = useState(false)
  const [installedFedimintServices, setInstalledFedimintServices] = useState<typeof fedimintServices>([])
  const logContainerRef = useRef<HTMLDivElement | null>(null)
  const services = useMemo(
    () => [...baseServices, ...installedFedimintServices.map(({ labelKey, value }) => ({ labelKey, value }))],
    [installedFedimintServices]
  )

  useEffect(() => {
    let mounted = true

    const loadApps = async () => {
      try {
        const apps = await getApps() as AppInfo[]
        if (!mounted) return
        const installed = new Set((Array.isArray(apps) ? apps : []).filter((app) => app.installed).map((app) => app.id))
        setInstalledFedimintServices(fedimintServices.filter((item) => installed.has(item.appID)))
      } catch {
        if (mounted) {
          setInstalledFedimintServices([])
        }
      }
    }

    void loadApps()
    window.addEventListener('apps:changed', loadApps)
    return () => {
      mounted = false
      window.removeEventListener('apps:changed', loadApps)
    }
  }, [])

  useEffect(() => {
    if (!services.some((item) => item.value === service)) {
      setService('lnd')
    }
  }, [service, services])

  const load = useCallback(async () => {
    setLoading(true)
    setStatus(t('logs.loading'))
    try {
      const res = await getLogs(service, lines)
      setData(Array.isArray(res?.lines) ? res.lines : [])
      setStatus('')
    } catch (err: any) {
      setStatus(err?.message || t('logs.fetchFailed'))
    } finally {
      setLoading(false)
    }
  }, [service, lines, t])

  useEffect(() => {
    void load()
  }, [load])

  useEffect(() => {
    const container = logContainerRef.current
    if (!container) return
    container.scrollTop = container.scrollHeight
  }, [data])

  return (
    <section className="space-y-6">
      <div className="section-card">
        <h2 className="text-2xl font-semibold">{t('logs.title')}</h2>
        <p className="text-fog/60">{t('logs.subtitle')}</p>
      </div>

      <div className="section-card space-y-4">
        <div className="flex flex-wrap gap-3">
          {services.map((item) => (
            <button
              key={item.value}
              className={service === item.value ? 'btn-primary' : 'btn-secondary'}
              onClick={() => setService(item.value)}
            >
              {t(item.labelKey)}
            </button>
          ))}
          <select className="input-field max-w-[140px]" value={lines} onChange={(e) => setLines(Number(e.target.value))}>
            {[200, 500, 1000].map((value) => (
              <option key={value} value={value}>{t('logs.linesOption', { count: value })}</option>
            ))}
          </select>
          <button className="btn-secondary" type="button" onClick={() => void load()} disabled={loading}>
            {t('common.refresh')}
          </button>
        </div>
        {status && <p className="text-sm text-brass">{status}</p>}
        <div
          ref={logContainerRef}
          className="bg-ink/70 border border-white/10 rounded-2xl p-4 text-xs font-mono whitespace-pre-wrap min-h-[320px] h-[60vh] max-h-[720px] overflow-y-auto"
        >
          {data.length ? data.join('\n') : t('logs.noLogs')}
        </div>
      </div>
    </section>
  )
}
