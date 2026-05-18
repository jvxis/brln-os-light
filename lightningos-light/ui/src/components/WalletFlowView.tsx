import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import ReactFlow, {
  Background,
  Controls,
  type Edge,
  type Node,
  Position,
  ReactFlowProvider
} from 'reactflow'
import dagre from 'dagre'
import 'reactflow/dist/style.css'
import { getProvenanceGraph, getProvenanceStatus, rebuildProvenance } from '../api'
import TxNode, {
  MAX_BARS_PER_SIDE,
  MAX_BARS_PER_SIDE_LEAF,
  TX_NODE_WIDTH,
  barHeightFor,
  computeTxNodeHeight,
  type TxBar,
  type TxNodeData
} from '../components/TxNode'
import SankeyEdge from '../components/SankeyEdge'

const NODE_TYPES = { tx: TxNode }
const EDGE_TYPES = { sankey: SankeyEdge }

type ProvenanceTx = {
  txid: string
  block_height: number
  confirmations: number
  timestamp: number
  amount_sat: number
  fee_sat: number
  label?: string
  is_external: boolean
}

type ProvenanceOutput = {
  txid: string
  vout: number
  address?: string
  amount_sat: number
  is_ours: boolean
  spent_by_txid?: string
  spent_in_vin?: number
  is_current_utxo: boolean
}

type ProvenanceState = {
  last_sync_height: number
  last_sync_at: string
  last_error: string
  tx_count: number
  output_count: number
  ours_outputs: number
  in_flight: boolean
}

type GraphPayload = {
  state: ProvenanceState
  txs: ProvenanceTx[]
  outputs: ProvenanceOutput[]
}

function layout(nodes: Node[], edges: Edge[]): Node[] {
  const g = new dagre.graphlib.Graph()
  g.setDefaultEdgeLabel(() => ({}))
  g.setGraph({ rankdir: 'LR', nodesep: 24, ranksep: 180 })
  const sized = nodes.map((n) => {
    const h = (n.data as TxNodeData)?.inputs ? computeTxNodeHeight(n.data as TxNodeData) : 80
    const w = TX_NODE_WIDTH
    g.setNode(n.id, { width: w, height: h })
    return { ...n, _w: w, _h: h }
  })
  for (const e of edges) g.setEdge(e.source, e.target)
  dagre.layout(g)
  return sized.map((n) => {
    const pos = g.node(n.id)
    return {
      ...n,
      sourcePosition: Position.Right,
      targetPosition: Position.Left,
      position: { x: pos.x - n._w / 2, y: pos.y - n._h / 2 }
    }
  })
}

function shortTxid(txid: string) {
  if (!txid) return ''
  return `${txid.slice(0, 8)}…${txid.slice(-6)}`
}

function fmtSats(value: number) {
  return value.toLocaleString()
}

type WalletFlowViewProps = {
  activeSource?: string
  noTxIndexHint?: boolean
}

export default function WalletFlowView({ activeSource = '', noTxIndexHint = false }: WalletFlowViewProps = {}) {
  const { t } = useTranslation()
  const [graph, setGraph] = useState<GraphPayload | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [actionMsg, setActionMsg] = useState('')
  const [liveStatus, setLiveStatus] = useState<ProvenanceState | null>(null)
  const [refreshStartedAt, setRefreshStartedAt] = useState<string | null>(null)
  const inFlight = Boolean(liveStatus?.in_flight)
  const autoTriggeredRef = useRef(false)
  const [mode, setMode] = useState<'live' | 'ours' | 'all' | 'lineage'>('live')
  const [limit, setLimit] = useState<number>(20)
  const [rootTxid, setRootTxid] = useState<string>('')
  const [hops, setHops] = useState<number>(3)
  const [rootInputDraft, setRootInputDraft] = useState<string>('')
  const [includeExternal, setIncludeExternal] = useState<boolean>(false)

  const load = async () => {
    setError('')
    try {
      const params: Parameters<typeof getProvenanceGraph>[0] = { mode, limit }
      if (mode === 'lineage') {
        if (!rootTxid) {
          setGraph({
            state: liveStatus ?? graph?.state ?? ({} as ProvenanceState),
            txs: [],
            outputs: []
          })
          setLoading(false)
          return
        }
        params.root = rootTxid
        params.hops = hops
        if (includeExternal) params.include_external = true
      }
      const res: any = await getProvenanceGraph(params)
      setGraph(res)
    } catch (err: any) {
      setError(err?.message || t('walletFlow.errorLoadGraph'))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    let cancelled = false
    const doLoad = async () => {
      if (cancelled) return
      await load()
    }
    doLoad()
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mode, limit, rootTxid, hops, includeExternal])

  useEffect(() => {
    let cancelled = false
    const tick = async () => {
      try {
        const status: any = await getProvenanceStatus()
        if (cancelled) return
        setLiveStatus(status)
        if (
          refreshStartedAt &&
          !status?.in_flight &&
          status?.last_sync_at &&
          status.last_sync_at > refreshStartedAt
        ) {
          await load()
          setRefreshStartedAt(null)
          setActionMsg(t('walletFlow.statusRefreshComplete'))
        }
      } catch {
        // ignore transient errors
      }
    }
    tick()
    const timer = window.setInterval(tick, 2000)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [refreshStartedAt])

  const triggerRebuild = async (full: boolean) => {
    setActionMsg(full ? t('walletFlow.statusFullRebuildStarted') : t('walletFlow.statusBuilding'))
    setRefreshStartedAt(new Date().toISOString())
    try {
      await rebuildProvenance(full)
    } catch (err: any) {
      setActionMsg(err?.message || t('walletFlow.statusRefreshFailed'))
      setRefreshStartedAt(null)
    }
  }

  useEffect(() => {
    if (autoTriggeredRef.current) return
    if (loading) return
    if (inFlight) return
    if (liveStatus == null) return
    if (liveStatus.tx_count > 0) return
    if (graph && graph.txs.length > 0) return
    autoTriggeredRef.current = true
    void triggerRebuild(false)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loading, inFlight, liveStatus?.tx_count, graph?.txs?.length])

  const { nodes, edges } = useMemo(() => {
    if (!graph) return { nodes: [] as Node[], edges: [] as Edge[] }
    const txs = graph.txs ?? []
    const outputs = graph.outputs ?? []
    if (txs.length === 0) return { nodes: [] as Node[], edges: [] as Edge[] }

    const txMap = new Map<string, ProvenanceTx>()
    for (const t of txs) txMap.set(t.txid, t)

    // Outputs grouped by producer tx, and inputs (outputs spent by this tx)
    // grouped by consumer tx.
    const outsByProducer = new Map<string, ProvenanceOutput[]>()
    const insByConsumer = new Map<string, ProvenanceOutput[]>()
    let minAmt = Number.POSITIVE_INFINITY
    let maxAmt = 0
    for (const o of outputs) {
      if (o.amount_sat > 0) {
        if (o.amount_sat < minAmt) minAmt = o.amount_sat
        if (o.amount_sat > maxAmt) maxAmt = o.amount_sat
      }
      if (o.txid) {
        const list = outsByProducer.get(o.txid) ?? []
        list.push(o)
        outsByProducer.set(o.txid, list)
      }
      if (o.spent_by_txid) {
        const list = insByConsumer.get(o.spent_by_txid) ?? []
        list.push(o)
        insByConsumer.set(o.spent_by_txid, list)
      }
    }
    if (!Number.isFinite(minAmt)) minAmt = 0

    // Visible bars per tx are kept in these sets so we can skip edges that
    // would attach to a bar we collapsed into "+N more".
    const visibleInputs = new Map<string, Set<number>>()
    const visibleOutputs = new Map<string, Set<number>>()

    const rawNodes: Node[] = txs.map((t) => {
      const allOuts = (outsByProducer.get(t.txid) ?? []).slice()
      const allIns = (insByConsumer.get(t.txid) ?? []).slice()

      // Show the heaviest bars; hide the long tail behind a summary line.
      // Leaf txs (no known inputs visualized) use the smaller cap so the
      // external-ancestor column doesn't dominate vertically.
      const sortedIns = allIns.slice().sort((a, b) => b.amount_sat - a.amount_sat)
      const sortedOuts = allOuts.slice().sort((a, b) => b.amount_sat - a.amount_sat)
      const barCap = allIns.length === 0 ? MAX_BARS_PER_SIDE_LEAF : MAX_BARS_PER_SIDE
      const visibleIns = sortedIns.slice(0, barCap)
      const visibleOuts = sortedOuts.slice(0, barCap)
      const hiddenIns = sortedIns.slice(barCap)
      const hiddenOuts = sortedOuts.slice(barCap)

      const inVis = new Set<number>()
      for (const o of visibleIns) inVis.add(o.spent_in_vin ?? 0)
      visibleInputs.set(t.txid, inVis)

      const outVis = new Set<number>()
      for (const o of visibleOuts) outVis.add(o.vout)
      visibleOutputs.set(t.txid, outVis)

      const inputs: TxBar[] = visibleIns.map((o, i) => ({
        index: o.spent_in_vin ?? i,
        amount: o.amount_sat,
        address: o.address,
        isOurs: o.is_ours,
        refTxid: o.txid
      }))
      const outs: TxBar[] = visibleOuts.map((o) => ({
        index: o.vout,
        amount: o.amount_sat,
        address: o.address,
        isOurs: o.is_ours,
        isCurrentUtxo: o.is_current_utxo ?? (o.is_ours && !o.spent_by_txid)
      }))

      const data: TxNodeData = {
        txid: t.txid,
        blockHeight: t.block_height,
        confirmations: t.confirmations,
        timestamp: t.timestamp,
        amountSat: t.amount_sat,
        feeSat: t.fee_sat,
        label: t.label,
        isExternal: t.is_external,
        inputs,
        outputs: outs,
        minBarAmount: minAmt,
        maxBarAmount: maxAmt,
        hiddenInputCount: hiddenIns.length,
        hiddenInputSat: hiddenIns.reduce((s, o) => s + o.amount_sat, 0),
        hiddenOutputCount: hiddenOuts.length,
        hiddenOutputSat: hiddenOuts.reduce((s, o) => s + o.amount_sat, 0)
      }

      return {
        id: t.txid,
        type: 'tx',
        data,
        position: { x: 0, y: 0 }
      }
    })

    const rawEdges: Edge[] = []
    // Edges that go into a collapsed bar group are merged into a single
    // aggregate ribbon per (sourceHandle, targetTxId/out-more) pair so the
    // flow visually terminates at the "+N more" row instead of orphaning.
    const aggregates = new Map<string, { amount: number; isOurs: boolean }>()
    for (const o of outputs) {
      if (!o.spent_by_txid) continue
      if (!txMap.has(o.txid) || !txMap.has(o.spent_by_txid)) continue
      const srcVis = visibleOutputs.get(o.txid)
      const tgtVis = visibleInputs.get(o.spent_by_txid)
      const sourceHandle = srcVis && !srcVis.has(o.vout) ? 'out-more' : `out-${o.vout}`
      const targetHandle =
        tgtVis && !tgtVis.has(o.spent_in_vin ?? 0) ? 'in-more' : `in-${o.spent_in_vin ?? 0}`

      // If either endpoint resolves to an aggregate handle, fold the edge
      // into a per-(source,target,handles) bucket so we draw one fat ribbon
      // instead of N tiny ones piled on the same anchor.
      if (sourceHandle === 'out-more' || targetHandle === 'in-more') {
        const key = `${o.txid}|${sourceHandle}|${o.spent_by_txid}|${targetHandle}`
        const cur = aggregates.get(key) ?? { amount: 0, isOurs: false }
        cur.amount += o.amount_sat
        cur.isOurs = cur.isOurs || o.is_ours
        aggregates.set(key, cur)
        continue
      }

      const baseColor = o.is_ours ? '#14b8a6' : '#64748b'
      const ribbonHeight = barHeightFor(o.amount_sat, minAmt, maxAmt)
      rawEdges.push({
        id: `${o.txid}:${o.vout}->${o.spent_by_txid}:${o.spent_in_vin ?? '?'}`,
        source: o.txid,
        sourceHandle,
        target: o.spent_by_txid,
        targetHandle,
        type: 'sankey',
        data: {
          height: ribbonHeight,
          color: baseColor,
          opacity: o.is_ours ? 0.85 : 0.45
        }
      })
    }
    for (const [key, agg] of aggregates) {
      const [srcTx, srcHandle, tgtTx, tgtHandle] = key.split('|')
      const baseColor = agg.isOurs ? '#14b8a6' : '#64748b'
      rawEdges.push({
        id: `agg-${key}`,
        source: srcTx,
        sourceHandle: srcHandle,
        target: tgtTx,
        targetHandle: tgtHandle,
        type: 'sankey',
        data: {
          height: barHeightFor(agg.amount, minAmt, maxAmt),
          color: baseColor,
          opacity: agg.isOurs ? 0.6 : 0.35
        }
      })
    }

    // Declutter: drop external tx nodes whose only contribution to the view
    // is feeding the "+N more" aggregate handle of some consumer. They show
    // up as a row of identical little blocks on the left feeding one bundled
    // ribbon into the bottom of the consumer — visual noise.
    const keepTxs = new Set<string>()
    for (const t of txs) {
      if (!t.is_external) {
        keepTxs.add(t.txid)
        continue
      }
      const tOuts = outsByProducer.get(t.txid) ?? []
      for (const o of tOuts) {
        if (o.is_current_utxo) {
          keepTxs.add(t.txid)
          break
        }
        if (!o.spent_by_txid) continue
        // The consumer must be in the rendered slice. If we don't render the
        // consumer, the external tx has no on-screen ribbon target and would
        // appear as a dangling block with a curve flying off into nowhere.
        if (!txMap.has(o.spent_by_txid)) continue
        const tgtVis = visibleInputs.get(o.spent_by_txid)
        // Keep the external tx only when its output lands on a VISIBLE input
        // bar of the consumer (not the "+N more" aggregate).
        if (tgtVis && tgtVis.has(o.spent_in_vin ?? 0)) {
          keepTxs.add(t.txid)
          break
        }
      }
    }

    const filteredNodes = rawNodes.filter((n) => keepTxs.has(n.id))
    const filteredEdges = rawEdges.filter((e) => keepTxs.has(e.source) && keepTxs.has(e.target))

    // Drop noise nodes:
    //   - In lineage mode: hide every node with no rendered edges (including
    //     ours-side orphans) so the trace stays focused on actual flow.
    //   - In live/ours/all modes: only hide *external* isolated nodes — keep
    //     ours-side orphans visible so coinbase live UTXOs still render even
    //     though they have no real parents.
    // Root of the trace is always preserved.
    const connected = new Set<string>()
    for (const e of filteredEdges) {
      connected.add(e.source)
      connected.add(e.target)
    }
    if (mode === 'lineage' && rootTxid) connected.add(rootTxid)
    const isLineage = mode === 'lineage'
    const finalNodes = filteredNodes.filter((n) => {
      if (connected.has(n.id)) return true
      return isLineage ? false : !(n.data as TxNodeData).isExternal
    })
    const finalIds = new Set(finalNodes.map((n) => n.id))
    const finalEdges = filteredEdges.filter((e) => finalIds.has(e.source) && finalIds.has(e.target))

    return { nodes: layout(finalNodes, finalEdges), edges: finalEdges }
  }, [graph, mode, rootTxid])

  return (
    <section className="space-y-4">
      {noTxIndexHint && (
        <div className="rounded-2xl border border-brass/40 bg-brass/10 px-4 py-3 text-xs text-brass">
          <b>{t('walletFlow.txIndexHintLead')}</b>{' '}
          {t('walletFlow.txIndexHintBefore')} <code>txindex=1</code>{' '}
          {t('walletFlow.txIndexHintMiddle')} <code>bitcoin.conf</code>{' '}
          {t('walletFlow.txIndexHintAfter')}
        </div>
      )}
      <div className="section-card">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <div className="flex items-center gap-2">
              <p className="text-xs uppercase tracking-[0.3em] text-fog/50">{t('walletFlow.kicker')}</p>
              {activeSource && (
                <span
                  className={
                    'rounded-full border px-2 py-0.5 text-[10px] uppercase tracking-wider ' +
                    (activeSource.startsWith('public:')
                      ? 'border-ember/40 bg-ember/10 text-ember'
                      : 'border-brass/40 bg-brass/10 text-brass')
                  }
                  title={
                    activeSource.startsWith('public:')
                      ? t('walletFlow.privacyAmber')
                      : t('walletFlow.sourceTooltip', { source: activeSource })
                  }
                >
                  {activeSource}
                </span>
              )}
            </div>
            <h2 className="mt-2 text-2xl font-semibold">{t('walletFlow.title')}</h2>
            <p className="mt-1 text-sm text-fog/65">
              {t('walletFlow.subtitle')}
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <select
              className="input-field text-xs py-1 px-2"
              value={mode}
              onChange={(e) => setMode(e.target.value as typeof mode)}
              title={t('walletFlow.modeSelectTooltip')}
            >
              <option value="live">{t('walletFlow.mode.live')}</option>
              <option value="ours">{t('walletFlow.mode.ours')}</option>
              <option value="all">{t('walletFlow.mode.all')}</option>
              <option value="lineage">{t('walletFlow.mode.lineage')}</option>
            </select>
            <label className="flex items-center gap-1 text-xs text-fog/70">
              {t('walletFlow.limit')}
              <input
                className="input-field w-16 text-xs py-1 px-2"
                inputMode="numeric"
                value={limit}
                onChange={(e) => {
                  const v = Number(e.target.value.replace(/[^\d]/g, '')) || 0
                  setLimit(Math.min(Math.max(v, 1), 2000))
                }}
              />
            </label>
            <button
              className="btn-secondary text-xs px-3 py-2"
              onClick={() => triggerRebuild(false)}
              disabled={inFlight}
            >
              {inFlight ? t('walletFlow.refreshing') : t('walletFlow.refresh')}
            </button>
            <button
              className="btn-secondary text-xs px-3 py-2"
              onClick={() => triggerRebuild(true)}
              disabled={inFlight}
              title={t('walletFlow.fullRebuildTooltip')}
            >
              {t('walletFlow.fullRebuild')}
            </button>
            <button
              className="btn-primary text-xs px-3 py-2"
              title={t('walletFlow.traceBiggestTooltip')}
              onClick={() => {
                if (!graph) return
                let biggestTxid = ''
                let biggestSat = 0
                for (const o of graph.outputs ?? []) {
                  if (o.is_current_utxo && o.amount_sat > biggestSat) {
                    biggestSat = o.amount_sat
                    biggestTxid = o.txid
                  }
                }
                if (biggestTxid) {
                  setMode('lineage')
                  setRootTxid(biggestTxid)
                  setHops(3)
                }
              }}
            >
              {t('walletFlow.traceBiggest')}
            </button>
          </div>
        </div>
        {mode === 'lineage' && (
          <div className="mt-3 flex flex-wrap items-center gap-2 rounded-2xl border border-white/10 bg-ink/40 p-3">
            <span className="text-xs text-fog/70">{t('walletFlow.traceFrom')}</span>
            <input
              className="input-field text-xs py-1 px-2 w-72"
              placeholder={t('walletFlow.pasteHint')}
              value={rootInputDraft || rootTxid}
              onChange={(e) => setRootInputDraft(e.target.value.trim())}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  const raw = rootInputDraft.trim()
                  if (raw) {
                    setRootTxid(raw.split(':')[0].toLowerCase())
                    setRootInputDraft('')
                  }
                }
              }}
            />
            <button
              className="btn-secondary text-xs px-3 py-1"
              onClick={() => {
                const raw = rootInputDraft.trim()
                if (!raw) return
                setRootTxid(raw.split(':')[0].toLowerCase())
                setRootInputDraft('')
              }}
            >
              {t('walletFlow.trace')}
            </button>
            <label className="flex items-center gap-1 text-xs text-fog/70">
              {t('walletFlow.hops')}
              <input
                className="input-field w-14 text-xs py-1 px-2"
                inputMode="numeric"
                value={hops}
                onChange={(e) => {
                  const v = Number(e.target.value.replace(/[^\d]/g, '')) || 1
                  setHops(Math.min(Math.max(v, 1), 20))
                }}
              />
            </label>
            <label className="flex items-center gap-1 text-xs text-fog/70" title={t('walletFlow.includeExternalTooltip')}>
              <input
                type="checkbox"
                checked={includeExternal}
                onChange={(e) => setIncludeExternal(e.target.checked)}
              />
              {t('walletFlow.includeExternal')}
            </label>
            {rootTxid && (
              <span className="text-xs text-fog/55 font-mono">
                {t('walletFlow.rootSummary', { txidShort: `${rootTxid.slice(0, 10)}…`, hops })}
              </span>
            )}
            {!rootTxid && (
              <span className="text-xs text-fog/55">
                {t('walletFlow.rootEmptyHint')}
              </span>
            )}
          </div>
        )}
        {(liveStatus || graph?.state) && (
          <p className="mt-3 text-xs text-fog/55">
            {t('walletFlow.syncStatus', {
              height: (liveStatus ?? graph?.state)?.last_sync_height ?? 0,
              txCount: (liveStatus ?? graph?.state)?.tx_count ?? 0,
              liveUtxos: (liveStatus ?? graph?.state)?.ours_outputs ?? 0,
              when:
                (liveStatus ?? graph?.state)?.last_sync_at &&
                (liveStatus ?? graph?.state)?.last_sync_at !== '0001-01-01T00:00:00Z'
                  ? new Date(((liveStatus ?? graph?.state) as ProvenanceState).last_sync_at).toLocaleString()
                  : t('walletFlow.never')
            })}
          </p>
        )}
        {inFlight && (
          <div className="mt-3 flex items-center gap-3 rounded-2xl border border-brass/40 bg-brass/10 px-3 py-2">
            <span className="inline-block h-3 w-3 animate-pulse rounded-full bg-brass" />
            <p className="text-xs text-brass">
              {t('walletFlow.walkInProgress', { count: liveStatus?.tx_count ?? 0 })}
            </p>
          </div>
        )}
        {liveStatus?.last_error && !inFlight && (
          <p className="mt-2 text-xs text-ember">{liveStatus.last_error}</p>
        )}
        {actionMsg && !inFlight && <p className="mt-2 text-xs text-brass">{actionMsg}</p>}
        {error && <p className="mt-2 text-xs text-ember">{error}</p>}
      </div>

      <div className="relative section-card p-0 overflow-hidden" style={{ height: '70vh', minHeight: 480 }}>
        {loading ? (
          <div className="flex h-full items-center justify-center">
            <div className="flex items-center gap-3 text-sm text-fog/70">
              <span className="inline-block h-4 w-4 animate-spin rounded-full border-2 border-brass border-t-transparent" />
              {t('walletFlow.loading')}
            </div>
          </div>
        ) : nodes.length > 500 ? (
          <div className="flex h-full items-center justify-center p-6">
            <div className="max-w-md text-center text-sm text-fog/80 space-y-2">
              <p className="text-brass font-semibold">{t('walletFlow.tooManyNodes', { count: nodes.length })}</p>
              <p>
                {t('walletFlow.tooManyNodesHintBefore')} <b>{t('walletFlow.mode.live')}</b>{t('walletFlow.tooManyNodesHintAfter')}
              </p>
            </div>
          </div>
        ) : nodes.length === 0 ? (
          <div className="flex h-full items-center justify-center p-6">
            <div className="flex flex-col items-center gap-3 text-center">
              {inFlight ? (
                <>
                  <span className="inline-block h-6 w-6 animate-spin rounded-full border-2 border-brass border-t-transparent" />
                  <p className="text-sm text-fog/80">
                    {t('walletFlow.buildingGraph')}
                  </p>
                  <p className="text-xs text-fog/55">
                    {t('walletFlow.txsScannedHint', { count: liveStatus?.tx_count ?? 0 })}
                  </p>
                </>
              ) : (
                <>
                  <p className="text-sm text-fog/80">
                    {t('walletFlow.emptyStateBefore')} <b>{t('walletFlow.refresh')}</b>{t('walletFlow.emptyStateAfter')}
                  </p>
                  <button
                    type="button"
                    className="btn-primary text-xs px-4 py-2"
                    onClick={() => triggerRebuild(false)}
                  >
                    {t('walletFlow.startNow')}
                  </button>
                </>
              )}
            </div>
          </div>
        ) : (
          <ReactFlowProvider>
            <ReactFlow
              nodes={nodes}
              edges={edges}
              nodeTypes={NODE_TYPES}
              edgeTypes={EDGE_TYPES}
              fitView
              proOptions={{ hideAttribution: true }}
              onNodeClick={(_, node) => {
                setMode('lineage')
                setRootTxid(node.id)
              }}
              nodesDraggable={false}
            >
              <Background gap={20} color="#1e293b" />
              <Controls />
            </ReactFlow>
          </ReactFlowProvider>
        )}
        {inFlight && nodes.length > 0 && (
          <div className="pointer-events-none absolute inset-0 flex items-center justify-center bg-ink/40 backdrop-blur-[1px]">
            <div className="flex items-center gap-3 rounded-2xl border border-white/15 bg-slate/85 px-4 py-3 shadow-panel">
              <span className="inline-block h-4 w-4 animate-spin rounded-full border-2 border-brass border-t-transparent" />
              <span className="text-xs text-fog/85">
                {t('walletFlow.refreshingOverlay', { count: liveStatus?.tx_count ?? 0 })}
              </span>
            </div>
          </div>
        )}
      </div>
    </section>
  )
}
