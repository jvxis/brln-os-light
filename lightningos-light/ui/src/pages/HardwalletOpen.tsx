import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getApps } from '../api'

// Mounted only after the normal Manager login. No session or redirect target
// is passed in a URL, and the app remains protected by forward_auth.
export default function HardwalletOpen() {
  const { t } = useTranslation()
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    let cancelled = false
    void getApps().then(apps => {
      if (cancelled) return
      const app = apps.find(item => item.id === 'brln-hardwallet')
      const url = app?.external_url ? new URL(app.external_url, window.location.origin) : null
      if (app?.status !== 'running' || !url || url.origin !== window.location.origin || url.hash !== '#hardwallet-open') {
        setFailed(true)
        return
      }
      const target = new URL(window.location.origin)
      target.port = '4449'
      window.location.replace(target.href)
    }).catch(() => { if (!cancelled) setFailed(true) })
    return () => { cancelled = true }
  }, [])
  return <div className="section-card mx-auto my-10 max-w-xl space-y-4">
    <h1 className="text-2xl font-semibold">BR⚡LN Hardwallet</h1>
    <p>{t(failed ? 'appStore.hardwallet.openFailed' : 'appStore.hardwallet.opening')}</p>
    <a className="btn-secondary" href="#apps">{t('nav.apps')}</a>
  </div>
}
