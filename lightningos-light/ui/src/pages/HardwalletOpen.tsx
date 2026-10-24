import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getApps } from '../api'
import { resolveStoredAppearance } from '../theme'

type Display = { visualTheme: string; palette: string; mode: string; lang: string }

function currentDisplay(language: string): Display {
  const root = document.documentElement.dataset
  let fallback = { visualTheme: 'sovereign-grid', palette: 'teal', mode: 'dark' }
  try { fallback = resolveStoredAppearance(window.localStorage) } catch { /* Restricted storage must not block the app. */ }
  return { visualTheme: root.visualTheme || fallback.visualTheme, palette: root.palette || fallback.palette,
    mode: root.theme || fallback.mode, lang: language === 'pt-BR' ? 'pt-BR' : 'en' }
}

// The scroll container of the LOS shell (falls back to the document).
function scroller(element: HTMLElement | null): Element {
  for (let node = element?.parentElement; node; node = node.parentElement) {
    const overflow = getComputedStyle(node).overflowY
    if ((overflow === 'auto' || overflow === 'scroll') && node.scrollHeight > node.clientHeight) return node
  }
  return document.scrollingElement || document.documentElement
}

// Preserve the app origin and public-account storage within the LOS shell.
export default function HardwalletOpen() {
  const { t, i18n } = useTranslation()
  const frame = useRef<HTMLIFrameElement>(null)
  const [state, setState] = useState<'loading' | 'ready' | 'unavailable'>('loading')
  const [source, setSource] = useState('')
  const [height, setHeight] = useState(900)
  const appOrigin = (() => { const target = new URL(window.location.origin); target.port = '4449'; return target.origin })()

  // The initial display travels in the fragment; later changes are sent as messages,
  // so changing theme or language never reloads the app or drops its USB session.
  useEffect(() => {
    const display = currentDisplay(i18n.language)
    const target = new URL(appOrigin)
    target.hash = new URLSearchParams({ losTheme: display.visualTheme, losPalette: display.palette, losMode: display.mode, losLang: display.lang }).toString()
    setSource(previous => previous || target.href)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    const send = () => frame.current?.contentWindow?.postMessage({ type: 'brln-hardwallet:display', ...currentDisplay(i18n.language) }, appOrigin)
    send()
    const observer = new MutationObserver(send)
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme', 'data-palette', 'data-visual-theme'] })
    return () => observer.disconnect()
  }, [i18n.language, appOrigin, state])

  useEffect(() => {
    const receive = (event: MessageEvent) => {
      if (event.origin !== appOrigin || event.source !== frame.current?.contentWindow) return
      const data = event.data as { type?: string; height?: unknown; top?: unknown } | null
      if (data?.type === 'brln-hardwallet:height' && typeof data.height === 'number' && Number.isFinite(data.height))
        setHeight(Math.min(20000, Math.max(480, Math.ceil(data.height))))
      if (data?.type === 'brln-hardwallet:reveal' && typeof data.top === 'number' && Number.isFinite(data.top) && frame.current) {
        const container = scroller(frame.current)
        const containerTop = container === document.scrollingElement ? 0 : container.getBoundingClientRect().top
        const offset = frame.current.getBoundingClientRect().top - containerTop + Math.max(0, data.top) - 96
        container.scrollBy({ top: offset, behavior: 'smooth' })
      }
    }
    window.addEventListener('message', receive)
    return () => window.removeEventListener('message', receive)
  }, [appOrigin])

  useEffect(() => {
    let cancelled = false
    const check = async () => {
      try {
        const apps = await getApps()
        if (cancelled) return
        const app = apps.find(item => item.id === 'brln-hardwallet')
        const launch = app?.external_url ? new URL(app.external_url, window.location.origin) : null
        setState(app?.installed && app.status === 'running' && launch?.origin === window.location.origin && launch.hash === '#hardwallet-open' ? 'ready' : 'unavailable')
      } catch { if (!cancelled) setState(previous => previous === 'ready' ? previous : 'unavailable') }
    }
    void check()
    window.addEventListener('apps:changed', check)
    const timer = window.setInterval(check, 30000)
    return () => { cancelled=true; clearInterval(timer); window.removeEventListener('apps:changed',check) }
  }, [])

  if (state !== 'ready' || !source) return <div className="section-card space-y-4">
    <h1 className="text-2xl font-semibold">BR⚡LN Hardwallet</h1>
    <p>{t(state === 'loading' ? 'appStore.hardwallet.opening' : 'appStore.hardwallet.openFailed')}</p>
    <a className="btn-secondary" href="#apps">{t('nav.apps')}</a>
  </div>
  // The app reports its content height, so only the LightningOS page scrolls.
  return <iframe ref={frame} title="BR⚡LN Hardwallet" src={source} scrolling="no"
    allow={`serial ${appOrigin}; clipboard-write ${appOrigin}`}
    referrerPolicy="no-referrer" className="block w-full border-0 rounded-2xl"
    onLoad={() => frame.current?.contentWindow?.postMessage({ type: 'brln-hardwallet:display', ...currentDisplay(i18n.language) }, appOrigin)}
    style={{ height }} />
}
