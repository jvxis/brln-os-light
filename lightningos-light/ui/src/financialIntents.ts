export type FinancialIntent = {
  required: boolean
  intent_id: string
  scope: string
  method: string
  path: string
  payload: Record<string, unknown>
  summary: {
    operation: string
    destination?: string
    amount_sat?: number
    amount_msat?: string | number
    amount_mode?: string
    max_fee_sat?: number
    max_fee_msat?: string | number
    sat_per_vbyte?: number
    max_fee_rate_sat_vbyte?: number
    fee_mode?: string
    selected_input_count?: number
    selected_input_sat?: number
    payment_hash?: string
    details?: unknown
  }
  expires_at: string
}

type Transport = (path: string, options?: RequestInit) => Promise<any>
export type FinancialConfirmation = { intent: FinancialIntent; approve: () => void; cancel: () => void }
const queue: FinancialConfirmation[] = []
const listeners = new Set<(value: FinancialConfirmation | null) => void>()
const notify = () => listeners.forEach(listener => listener(queue[0] || null))
export const subscribeFinancialConfirmation = (listener: (value: FinancialConfirmation | null) => void) => {
  listeners.add(listener)
  listener(queue[0] || null)
  return () => { listeners.delete(listener) }
}

const financialError = (code: string, message: string) => Object.assign(new Error(message), { code })
const confirmIntent = (intent: FinancialIntent) => new Promise<void>((resolve, reject) => {
  const finish = (approved: boolean) => {
    const index = queue.indexOf(confirmation)
    if (index < 0) return
    queue.splice(index, 1)
    notify()
    if (approved) resolve()
    else reject(financialError('financial_intent_cancelled', 'Confirmation cancelled; no new operation was submitted.'))
  }
  const confirmation: FinancialConfirmation = { intent, approve: () => finish(true), cancel: () => finish(false) }
  queue.push(confirmation)
  notify()
})
export const cancelFinancialConfirmations = () => [...queue].forEach(item => item.cancel())

// Store only opaque intent references. Passwords, invoices, recovery packets,
// and normalized execution bodies never enter browser persistent storage.
const storageKey = 'los-financial-intent-references-v1'
export type FinancialReference = { key: string; id: string }
type Reference = FinancialReference
const references: Reference[] = (() => {
  try {
    const parsed = JSON.parse(sessionStorage.getItem(storageKey) || '[]')
    return Array.isArray(parsed) ? parsed.filter(item => /^[a-f0-9]{64}$/.test(item?.key) && /^[a-f0-9]{32}$/.test(item?.id)) : []
  } catch { return [] }
})()
const referenceListeners = new Set<(value: FinancialReference[]) => void>()
export const subscribeFinancialReferences = (listener: (value: FinancialReference[]) => void) => {
  referenceListeners.add(listener)
  listener([...references])
  return () => { referenceListeners.delete(listener) }
}
const saveReference = (key: string, id?: string) => {
  const next = references.filter(item => item.key !== key)
  if (id) {
    if (next.length >= 64) throw financialError('financial_intent_capacity', 'Review pending operations before preparing another transfer.')
    next.push({ key, id })
  }
  // Persistence precedes confirmation and native execution. A storage failure
  // must not turn a reload into an untracked new keysend or transfer.
  try { sessionStorage.setItem(storageKey, JSON.stringify(next)) } catch {
    throw financialError('financial_intent_storage_unavailable', 'Unable to save the operation reference. Review its status before retrying.')
  }
  references.splice(0, references.length, ...next)
  referenceListeners.forEach(listener => listener([...references]))
}
export const dismissFinancialReference = (id: string) => {
  const reference = references.find(item => item.id === id)
  if (reference) saveReference(reference.key)
}
const stableValue = (value: any): any => Array.isArray(value) ? value.map(stableValue) : value && typeof value === 'object'
  ? Object.fromEntries(Object.keys(value).filter(key => key.toLowerCase() !== 'confirm_password').sort().map(key => [key, stableValue(value[key])]))
  : value
const operationKey = async (path: string, method: string, body: unknown) => {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(JSON.stringify([method, path, stableValue(body)])))
  return Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, '0')).join('')
}
const inFlight = new Map<string, Promise<any>>()

export async function financialRequest(path: string, options: RequestInit | undefined, transport: Transport) {
  const method = String(options?.method || 'GET').toUpperCase()
  if (!['POST', 'PUT'].includes(method) || path.startsWith('/api/auth/') || path.startsWith('/api/financial/') || typeof options?.body !== 'string') return transport(path, options)
  let payload: Record<string, unknown>
  try { payload = stableValue(JSON.parse(options.body)) } catch { return transport(path, options) }
  const key = await operationKey(path, method, payload)
  const pending = inFlight.get(key)
  if (pending) return pending
  const run = async () => {
    let intent: FinancialIntent | undefined
    const prior = references.find(item => item.key === key)
    if (prior) {
      // A failed lookup remains unresolved. Never silently prepare a second
      // transfer after a restart, authentication error, or lost response.
      const state = await transport(`/api/financial/intents/${prior.id}`, { cache: 'no-store' })
      if (state.operation !== path || state.method !== method) throw financialError('financial_intent_mismatch', 'Saved operation does not match this request.')
      if (['completed', 'rejected', 'cancelled'].includes(state.state)) {
        saveReference(key)
        if (state.status >= 400) throw financialError(state.result?.code || 'financial_intent_rejected', state.result?.error || 'The original operation was rejected.')
        return state.result
      }
      if (state.state === 'executing' || state.state === 'uncertain') {
        const result = await transport(`/api/financial/intents/${prior.id}/reconcile`, { method: 'POST', body: '{}' })
        saveReference(key)
        return result
      }
      if (state.state === 'draft' && state.can_confirm && state.payload && state.summary) {
        intent = { ...state, required: true, path, intent_id: prior.id }
      } else {
        await transport(`/api/financial/intents/${prior.id}/cancel`, { method: 'POST', body: '{}' })
        saveReference(key)
      }
    }
    if (!intent) {
      try { return await transport(path, options) } catch (error: any) {
        if (error?.status !== 428 || !error.details?.requires_financial_intent) throw error
      }
      intent = await transport('/api/financial/intents', { method: 'POST', body: JSON.stringify({ method, path, payload }) }) as FinancialIntent
      if (!intent.required) return transport(path, { ...options, body: JSON.stringify(payload) })
      if (!/^[a-f0-9]{32}$/.test(intent.intent_id) || intent.path !== path || intent.method !== method) throw financialError('financial_intent_mismatch', 'Invalid operation confirmation response.')
      saveReference(key, intent.intent_id)
    }
    saveReference(key, intent.intent_id)
    try { await confirmIntent(intent) } catch (error) {
      await transport(`/api/financial/intents/${intent.intent_id}/cancel`, { method: 'POST', body: '{}' })
      saveReference(key)
      throw error
    }
    const headers = new Headers(options?.headers)
    headers.set('X-LOS-Financial-Intent', intent.intent_id)
    // The confirmed server snapshot is the sole execution body, even if the
    // underlying page's form changes while the confirmation is open.
    const result = await transport(path, { ...options, headers, body: JSON.stringify(intent.payload) })
    saveReference(key)
    return result
  }
  const promise = run()
  inFlight.set(key, promise)
  try { return await promise } finally { inFlight.delete(key) }
}
