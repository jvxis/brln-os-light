import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getApps } from '../api'
import { resolveStoredAppearance } from '../theme'

// Preserve the app origin and public-account storage within the LOS shell.
export default function HardwalletOpen() {
  const { t } = useTranslation()
  const [state, setState] = useState<'loading' | 'ready' | 'unavailable'>('loading')
  const [source, setSource] = useState('')
  useEffect(() => {
    let cancelled = false
    const appearance = () => {
      const target = new URL(window.location.origin)
      target.port = '4449'
      const root = document.documentElement.dataset
      let fallback = {visualTheme:'sovereign-grid',palette:'teal',mode:'dark'}
      try { fallback = resolveStoredAppearance(window.localStorage) } catch { /* Restricted storage must not block the app. */ }
      target.hash = new URLSearchParams({losTheme: root.visualTheme || fallback.visualTheme,
        losPalette: root.palette || fallback.palette, losMode: root.theme || fallback.mode}).toString()
      setSource(target.href)
    }
    const check = async () => {
      try {
        const apps = await getApps()
        if (cancelled) return
        const app = apps.find(item => item.id === 'brln-hardwallet')
        const launch = app?.external_url ? new URL(app.external_url, window.location.origin) : null
        setState(app?.installed && app.status === 'running' && launch?.origin === window.location.origin && launch.hash === '#hardwallet-open' ? 'ready' : 'unavailable')
      } catch { if (!cancelled) setState(previous => previous === 'ready' ? previous : 'unavailable') }
    }
    appearance()
    const observer = new MutationObserver(appearance)
    observer.observe(document.documentElement, {attributes:true, attributeFilter:['data-theme','data-palette','data-visual-theme']})
    void check()
    window.addEventListener('apps:changed', check)
    const timer = window.setInterval(check, 30000)
    return () => { cancelled=true; observer.disconnect(); clearInterval(timer); window.removeEventListener('apps:changed',check) }
  }, [])
  if (state !== 'ready') return <div className="section-card space-y-4">
    <h1 className="text-2xl font-semibold">BRLN Hardwallet</h1>
    <p>{t(state === 'loading' ? 'appStore.hardwallet.opening' : 'appStore.hardwallet.openFailed')}</p>
    <a className="btn-secondary" href="#apps">{t('nav.apps')}</a>
  </div>
  return <iframe title="BRLN Hardwallet" src={source}
    allow={`serial ${new URL(source).origin}; clipboard-write ${new URL(source).origin}`}
    referrerPolicy="no-referrer" className="w-full border-0 rounded-2xl"
    style={{height:'calc(100vh - 160px)', minHeight:640}} />
}
