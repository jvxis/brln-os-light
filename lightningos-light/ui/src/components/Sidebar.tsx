import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getChatInbox } from '../api'
import clsx from '../utils/clsx'

type RouteItem = {
  key: string
  label: string
  element: JSX.Element
}

type MenuConfig = {
  favorites: string[]
  hidden: string[]
}

type SidebarProps = {
  routes: RouteItem[]
  allRoutes: RouteItem[]
  menuConfig: MenuConfig
  onMenuConfigChange: (config: MenuConfig) => void
  current: string
  open: boolean
  onClose: () => void
}

const lastReadKey = 'chat:lastRead'

const readLastReadMap = () => {
  try {
    const raw = localStorage.getItem(lastReadKey)
    if (!raw) return {}
    const parsed = JSON.parse(raw)
    if (parsed && typeof parsed === 'object') {
      return parsed as Record<string, number>
    }
  } catch {
    // ignore storage errors
  }
  return {}
}

export default function Sidebar({ routes, allRoutes, menuConfig, onMenuConfigChange, current, open, onClose }: SidebarProps) {
  const { t } = useTranslation()
  const [version, setVersion] = useState('')
  const [unreadChats, setUnreadChats] = useState(0)
  const [editing, setEditing] = useState(false)
  const [draftConfig, setDraftConfig] = useState<MenuConfig>(menuConfig)

  useEffect(() => {
    let active = true
    fetch('/version.txt', { cache: 'no-store' })
      .then((res) => (res.ok ? res.text() : ''))
      .then((text) => {
        if (!active) return
        setVersion(text.trim())
      })
      .catch(() => {
        if (!active) return
        setVersion('')
      })
    return () => {
      active = false
    }
  }, [])

  useEffect(() => {
    let mounted = true
    const loadUnread = async () => {
      try {
        const inboxRes = await getChatInbox()
        if (!mounted) return
        const items = Array.isArray(inboxRes?.items)
          ? inboxRes.items
          : []
        const lastReadMap = readLastReadMap()
        const unread = new Set<string>()
        for (const item of items) {
          const peerKey = typeof item?.peer_pubkey === 'string' ? item.peer_pubkey : ''
          if (!peerKey) continue
          const ts = new Date(item.last_inbound_at).getTime()
          if (!ts || Number.isNaN(ts)) continue
          const lastRead = lastReadMap[peerKey] || 0
          if (ts > lastRead) {
            unread.add(peerKey)
          }
        }
        setUnreadChats(unread.size)
      } catch {
        if (!mounted) return
        setUnreadChats(0)
      }
    }

    loadUnread()
    const timer = window.setInterval(loadUnread, 12000)
    return () => {
      mounted = false
      window.clearInterval(timer)
    }
  }, [])

  useEffect(() => {
    if (!editing) {
      setDraftConfig(menuConfig)
    }
  }, [editing, menuConfig])

  const unreadLabel = unreadChats === 1
    ? t('chat.unreadSingle')
    : t('chat.unreadMultiple', { count: unreadChats })

  const routeMap = useMemo(() => new Map(allRoutes.map((route) => [route.key, route])), [allRoutes])
  const hiddenSet = useMemo(() => new Set(draftConfig.hidden), [draftConfig.hidden])
  const favoriteSet = useMemo(() => new Set(draftConfig.favorites), [draftConfig.favorites])
  const orderedFavorites = draftConfig.favorites
    .map((key) => routeMap.get(key))
    .filter((route): route is RouteItem => Boolean(route))
  const visibleCount = allRoutes.length - hiddenSet.size

  const handleToggleFavorite = (key: string) => {
    setDraftConfig((currentConfig) => {
      const isFavorite = currentConfig.favorites.includes(key)
      if (isFavorite) {
        return {
          favorites: currentConfig.favorites.filter((item) => item !== key),
          hidden: currentConfig.hidden
        }
      }
      return {
        favorites: [...currentConfig.favorites, key],
        hidden: currentConfig.hidden.filter((item) => item !== key)
      }
    })
  }

  const handleToggleHidden = (key: string) => {
    setDraftConfig((currentConfig) => {
      const isHidden = currentConfig.hidden.includes(key)
      const visibleCountLocal = allRoutes.length - currentConfig.hidden.length
      if (!isHidden && visibleCountLocal <= 1) {
        return currentConfig
      }
      if (isHidden) {
        return {
          favorites: currentConfig.favorites,
          hidden: currentConfig.hidden.filter((item) => item !== key)
        }
      }
      const nextHidden = [...currentConfig.hidden, key]
      const nextFavorites = currentConfig.favorites.filter((item) => item !== key)
      return {
        favorites: nextFavorites,
        hidden: nextHidden
      }
    })
  }

  const handleMoveFavorite = (key: string, direction: 'up' | 'down') => {
    setDraftConfig((currentConfig) => {
      const index = currentConfig.favorites.indexOf(key)
      if (index === -1) return currentConfig
      const targetIndex = direction === 'up' ? index - 1 : index + 1
      if (targetIndex < 0 || targetIndex >= currentConfig.favorites.length) {
        return currentConfig
      }
      const nextFavorites = [...currentConfig.favorites]
      const [moved] = nextFavorites.splice(index, 1)
      nextFavorites.splice(targetIndex, 0, moved)
      return {
        favorites: nextFavorites,
        hidden: currentConfig.hidden
      }
    })
  }

  const handleSave = () => {
    onMenuConfigChange(draftConfig)
    setEditing(false)
  }

  const handleCancel = () => {
    setDraftConfig(menuConfig)
    setEditing(false)
  }

  const handleReset = () => {
    setDraftConfig({ favorites: [], hidden: [] })
  }

  return (
    <aside
      id="app-sidebar"
      className={clsx(
        'fixed inset-y-0 left-0 z-40 w-72 max-w-[85vw] h-full bg-ink/80 border-b lg:border-b-0 lg:border-r border-white/10 px-6 py-8 flex flex-col overflow-y-auto transition-transform duration-300 ease-out',
        open ? 'translate-x-0' : '-translate-x-full',
        'lg:translate-x-0 lg:sticky lg:top-0 lg:h-screen lg:w-72'
      )}
    >
      <div className="flex items-center justify-between lg:justify-start gap-3">
        <div className="h-10 w-10 rounded-2xl bg-glow/20 border border-glow/30 flex items-center justify-center gap-0.5 text-glow font-semibold">
          <svg className="h-2.5 w-2.5 shrink-0" viewBox="0 0 64 64" fill="currentColor" aria-hidden="true">
            <path d="M34 10 27 21h5.1l-2.4 7L38 16.7h-5.2L34 10z" />
          </svg>
          <span className="text-[0.8rem] leading-none font-bold">Los</span>
        </div>
        <div className="flex-1">
          <p className="text-lg font-semibold">{t('topbar.productName')}</p>
          <p className="text-xs text-fog/60">{t('topbar.mainnetOnly')}</p>
        </div>
        <button
          type="button"
          className="lg:hidden inline-flex items-center justify-center rounded-full border border-white/15 bg-ink/60 h-9 w-9 text-fog/70 hover:text-white hover:border-white/40 transition"
          onClick={onClose}
          aria-label={t('sidebar.closeMenu')}
        >
          <svg viewBox="0 0 24 24" className="h-4 w-4" fill="none" stroke="currentColor" strokeWidth="1.8">
            <path d="M6 6l12 12M18 6l-12 12" />
          </svg>
        </button>
      </div>
      {editing ? (
        <div className="mt-8 flex-1 flex flex-col">
          <div className="flex-1 space-y-6 overflow-y-auto pr-1">
            <div>
              <p className="text-xs uppercase tracking-[0.2em] text-fog/50">{t('sidebar.editMenu')}</p>
              <p className="mt-2 text-sm text-fog/70">{t('sidebar.editMenuHint')}</p>
            </div>
            <div>
              <p className="text-xs uppercase tracking-[0.2em] text-fog/50">{t('sidebar.favorites')}</p>
              {orderedFavorites.length === 0 ? (
                <p className="mt-3 text-sm text-fog/60">{t('sidebar.favoritesEmpty')}</p>
              ) : (
                <div className="mt-3 space-y-2">
                  {orderedFavorites.map((route, index) => (
                    <div
                      key={route.key}
                      className="flex items-center gap-2 rounded-2xl border border-white/10 bg-white/5 px-3 py-2"
                    >
                      <span className="flex-1 text-sm text-fog">{route.label}</span>
                      <button
                        type="button"
                        className={clsx(
                          'h-7 w-7 rounded-full border border-white/15 text-fog/70 transition',
                          index === 0 ? 'opacity-40 cursor-not-allowed' : 'hover:text-white hover:border-white/40'
                        )}
                        onClick={() => handleMoveFavorite(route.key, 'up')}
                        disabled={index === 0}
                        aria-label={t('sidebar.moveUp')}
                        title={t('sidebar.moveUp')}
                      >
                        <svg viewBox="0 0 24 24" className="mx-auto h-3 w-3" fill="currentColor">
                          <path d="M12 8l-6 6h12l-6-6z" />
                        </svg>
                      </button>
                      <button
                        type="button"
                        className={clsx(
                          'h-7 w-7 rounded-full border border-white/15 text-fog/70 transition',
                          index === orderedFavorites.length - 1
                            ? 'opacity-40 cursor-not-allowed'
                            : 'hover:text-white hover:border-white/40'
                        )}
                        onClick={() => handleMoveFavorite(route.key, 'down')}
                        disabled={index === orderedFavorites.length - 1}
                        aria-label={t('sidebar.moveDown')}
                        title={t('sidebar.moveDown')}
                      >
                        <svg viewBox="0 0 24 24" className="mx-auto h-3 w-3" fill="currentColor">
                          <path d="M12 16l6-6H6l6 6z" />
                        </svg>
                      </button>
                      <button
                        type="button"
                        className="h-7 w-7 rounded-full border border-ember/60 text-ember/80 transition hover:text-white hover:border-ember"
                        onClick={() => handleToggleFavorite(route.key)}
                        aria-label={t('sidebar.removeFavorite')}
                        title={t('sidebar.removeFavorite')}
                      >
                        <svg viewBox="0 0 24 24" className="mx-auto h-3 w-3" fill="currentColor">
                          <path d="M12 3l2.7 5.5 6 0.9-4.4 4.3 1 6L12 17l-5.3 2.8 1-6L3.3 9.4l6-0.9L12 3z" />
                        </svg>
                      </button>
                    </div>
                  ))}
                </div>
              )}
            </div>
            <div className="border-t border-white/10 pt-4">
              <p className="text-xs uppercase tracking-[0.2em] text-fog/50">{t('sidebar.allItems')}</p>
              <div className="mt-3 space-y-2">
                {allRoutes.map((route) => {
                  const isFavorite = favoriteSet.has(route.key)
                  const isHidden = hiddenSet.has(route.key)
                  const canHide = isHidden || visibleCount > 1
                  return (
                    <div
                      key={route.key}
                      className={clsx(
                        'flex items-center justify-between rounded-2xl border border-white/10 px-3 py-2',
                        isHidden ? 'text-fog/40' : 'text-fog/80',
                        isHidden ? 'bg-white/5' : 'bg-transparent'
                      )}
                    >
                      <div className="flex items-center gap-2">
                        <button
                          type="button"
                          className={clsx(
                            'h-7 w-7 rounded-full border border-white/15 transition',
                            isFavorite
                              ? 'text-amber-200 border-amber-400/40 hover:text-amber-200'
                              : 'text-fog/60 hover:text-white hover:border-white/40'
                          )}
                          onClick={() => handleToggleFavorite(route.key)}
                          aria-label={isFavorite ? t('sidebar.removeFavorite') : t('sidebar.addFavorite')}
                          title={isFavorite ? t('sidebar.removeFavorite') : t('sidebar.addFavorite')}
                        >
                          {isFavorite ? (
                            <svg viewBox="0 0 24 24" className="mx-auto h-3 w-3" fill="currentColor">
                              <path d="M12 3l2.7 5.5 6 0.9-4.4 4.3 1 6L12 17l-5.3 2.8 1-6L3.3 9.4l6-0.9L12 3z" />
                            </svg>
                          ) : (
                            <svg viewBox="0 0 24 24" className="mx-auto h-3 w-3" fill="none" stroke="currentColor" strokeWidth="1.6">
                              <path d="M12 3l2.7 5.5 6 0.9-4.4 4.3 1 6L12 17l-5.3 2.8 1-6L3.3 9.4l6-0.9L12 3z" />
                            </svg>
                          )}
                        </button>
                        <span className="text-sm">{route.label}</span>
                      </div>
                      <button
                        type="button"
                        className={clsx(
                          'rounded-full border px-3 py-1 text-xs transition',
                          canHide ? 'border-white/20 text-fog/70 hover:text-white hover:border-white/40' : 'border-white/10 text-fog/40',
                          !canHide && 'cursor-not-allowed'
                        )}
                        onClick={() => handleToggleHidden(route.key)}
                        disabled={!canHide}
                        aria-label={isHidden ? t('sidebar.showItem') : t('sidebar.hideItem')}
                      >
                        {isHidden ? t('sidebar.showItem') : t('sidebar.hideItem')}
                      </button>
                    </div>
                  )
                })}
              </div>
            </div>
          </div>
          <div className="pt-6">
            <div className="flex flex-wrap gap-3">
              <button className="btn-primary text-xs px-3 py-2" onClick={handleSave}>{t('common.save')}</button>
              <button className="btn-secondary text-xs px-3 py-2" onClick={handleCancel}>{t('common.cancel')}</button>
            </div>
            <button
              type="button"
              className="mt-4 text-xs text-fog/60 hover:text-white transition"
              onClick={handleReset}
            >
              {t('sidebar.resetMenu')}
            </button>
          </div>
        </div>
      ) : (
        <nav className="mt-8 space-y-2 flex-1">
          {routes.map((route) => (
            <a
              key={route.key}
              href={`#${route.key}`}
              className={clsx(
                'block px-4 py-3 rounded-2xl text-sm transition',
                current === route.key
                  ? 'bg-white/10 text-white shadow-panel'
                  : 'text-fog/70 hover:text-white hover:bg-white/5'
              )}
              onClick={onClose}
            >
              {route.key === 'chat' ? (
                <span className="inline-flex items-center gap-2">
                  <span>{route.label}</span>
                  {unreadChats > 0 && (
                    <span className="rounded-full bg-ember px-2 py-0.5 text-[10px] font-semibold text-white" title={unreadLabel}>
                      {unreadChats}
                    </span>
                  )}
                </span>
              ) : (
                route.label
              )}
            </a>
          ))}
        </nav>
      )}
      <div className="mt-6 border-t border-white/10 pt-4 text-xs text-fog/60">
        <div className="flex items-center justify-between gap-3">
          <p>
            {t('sidebar.versionLabel')}{' '}
            <span className="text-fog">{version || t('common.unavailable')}</span>
          </p>
          {!editing && (
            <button
              type="button"
              className="inline-flex h-7 w-7 items-center justify-center rounded-full border border-white/15 text-fog/70 transition hover:text-white hover:border-white/40"
              onClick={() => setEditing(true)}
              aria-label={t('sidebar.editMenu')}
              title={t('sidebar.editMenu')}
            >
              <svg viewBox="0 0 24 24" className="h-3.5 w-3.5" fill="none" stroke="currentColor" strokeWidth="1.6">
                <path d="M12 20h9" />
                <path d="M16.5 3.5l4 4-10 10H6.5v-4l10-10z" />
              </svg>
            </button>
          )}
        </div>
        <a
          className="mt-2 inline-flex text-fog/70 hover:text-white transition"
          href="https://br-ln.com"
          target="_blank"
          rel="noreferrer"
        >
          {t('sidebar.poweredBy')}
        </a>
      </div>
    </aside>
  )
}
