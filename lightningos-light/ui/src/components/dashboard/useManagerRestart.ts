import { useEffect, useState } from 'react'
import { APIError, getAuthState, restartService } from '../../api'

type RestartState = 'idle' | 'restarting' | 'available' | 'failed' | 'timeout'

export default function useManagerRestart() {
  const [state, setState] = useState<RestartState>('idle')
  const busy = state === 'restarting'

  useEffect(() => {
    if (!busy) return
    const controller = new AbortController()
    let pollTimer: number | undefined
    let availableChecks = 0
    const deadline = window.setTimeout(() => finish('timeout'), 90000)

    function finish(next: RestartState) {
      if (controller.signal.aborted) return
      controller.abort()
      window.clearTimeout(deadline)
      window.clearTimeout(pollTimer)
      setState(next)
    }

    const poll = async () => {
      try {
        // Auth state is lightweight and public even if restarting ended the session.
        // Checking Manager availability must not depend on LND/Bitcoin health.
        const auth = await getAuthState(controller.signal)
        if (controller.signal.aborted) return
        if (typeof auth?.enabled !== 'boolean' || typeof auth?.authenticated !== 'boolean') {
          throw new Error('Invalid Manager response')
        }
        availableChecks += 1
        if (availableChecks >= 2) {
          finish('available')
          if (auth.enabled && !auth.authenticated) {
            window.dispatchEvent(new CustomEvent('auth:required'))
          }
          return
        }
      } catch {
        // Temporary connection failures are expected while the process restarts.
        availableChecks = 0
      }
      if (!controller.signal.aborted) pollTimer = window.setTimeout(() => void poll(), 2000)
    }

    const start = async () => {
      try {
        await restartService({ service: 'lightningos-manager' }, controller.signal)
      } catch (error) {
        if (controller.signal.aborted) return
        if (error instanceof APIError) {
          finish('failed')
          return
        }
        // The process may stop before replying. Do not resend the restart command.
      }
      // The broker queues the restart. Give the old process time to stop before
      // checking availability; two responses also cover very fast restarts.
      if (!controller.signal.aborted) pollTimer = window.setTimeout(() => void poll(), 5000)
    }
    void start()

    return () => {
      controller.abort()
      window.clearTimeout(deadline)
      window.clearTimeout(pollTimer)
    }
  }, [busy])

  return { state, busy, start: () => setState('restarting') }
}
