import { useEffect, useMemo, useState } from 'react'
import { closeChannel, connectPeer, disconnectPeer, getLnChannels, getLnPeers, openChannel, updateChannelFees } from '../api'

type Channel = {
  channel_point: string
  channel_id: number
  remote_pubkey: string
  peer_alias: string
  active: boolean
  private: boolean
  capacity_sat: number
  local_balance_sat: number
  remote_balance_sat: number
}

type Peer = {
  pub_key: string
  alias: string
  address: string
  inbound: boolean
  bytes_sent: number
  bytes_recv: number
  sat_sent: number
  sat_recv: number
  ping_time: number
  sync_type: string
  last_error: string
}

export default function LightningOps() {
  const [channels, setChannels] = useState<Channel[]>([])
  const [activeCount, setActiveCount] = useState(0)
  const [inactiveCount, setInactiveCount] = useState(0)
  const [status, setStatus] = useState('')
  const [filter, setFilter] = useState<'all' | 'active' | 'inactive'>('all')
  const [search, setSearch] = useState('')
  const [minCapacity, setMinCapacity] = useState('')
  const [sortBy, setSortBy] = useState<'capacity' | 'local' | 'remote' | 'alias'>('capacity')
  const [sortDir, setSortDir] = useState<'desc' | 'asc'>('desc')
  const [showPrivate, setShowPrivate] = useState(true)

  const [peerAddress, setPeerAddress] = useState('')
  const [peerTemporary, setPeerTemporary] = useState(false)
  const [peerStatus, setPeerStatus] = useState('')
  const [peers, setPeers] = useState<Peer[]>([])
  const [peerListStatus, setPeerListStatus] = useState('')
  const [peerActionStatus, setPeerActionStatus] = useState('')

  const [openPubkey, setOpenPubkey] = useState('')
  const [openAmount, setOpenAmount] = useState('')
  const [openPush, setOpenPush] = useState('')
  const [openPrivate, setOpenPrivate] = useState(false)
  const [openStatus, setOpenStatus] = useState('')

  const [closePoint, setClosePoint] = useState('')
  const [closeForce, setCloseForce] = useState(false)
  const [closeStatus, setCloseStatus] = useState('')

  const [feeScopeAll, setFeeScopeAll] = useState(true)
  const [feeChannelPoint, setFeeChannelPoint] = useState('')
  const [baseFeeMsat, setBaseFeeMsat] = useState('')
  const [feeRatePpm, setFeeRatePpm] = useState('')
  const [timeLockDelta, setTimeLockDelta] = useState('')
  const [feeStatus, setFeeStatus] = useState('')

  const load = async () => {
    setStatus('Loading channels...')
    setPeerListStatus('Loading peers...')
    const [channelsResult, peersResult] = await Promise.allSettled([
      getLnChannels(),
      getLnPeers()
    ])
    if (channelsResult.status === 'fulfilled') {
      const res = channelsResult.value
      const list = Array.isArray(res?.channels) ? res.channels : []
      setChannels(list)
      setActiveCount(res?.active_count ?? 0)
      setInactiveCount(res?.inactive_count ?? 0)
      setStatus('')
    } else {
      const message = (channelsResult.reason as any)?.message || 'Failed to load channels.'
      setStatus(message)
    }
    if (peersResult.status === 'fulfilled') {
      const res = peersResult.value
      setPeers(Array.isArray(res?.peers) ? res.peers : [])
      setPeerListStatus('')
    } else {
      const message = (peersResult.reason as any)?.message || 'Failed to load peers.'
      setPeerListStatus(message)
    }
  }

  useEffect(() => {
    load()
  }, [])

  const filteredChannels = useMemo(() => {
    let list = channels
    if (filter === 'active') {
      list = list.filter((ch) => ch.active)
    }
    if (filter === 'inactive') {
      list = list.filter((ch) => !ch.active)
    }
    if (!showPrivate) {
      list = list.filter((ch) => !ch.private)
    }
    if (search.trim()) {
      const query = search.trim().toLowerCase()
      list = list.filter((ch) => {
        return (
          ch.peer_alias?.toLowerCase().includes(query) ||
          ch.remote_pubkey?.toLowerCase().includes(query) ||
          ch.channel_point?.toLowerCase().includes(query)
        )
      })
    }
    const minCap = Number(minCapacity || 0)
    if (minCap > 0) {
      list = list.filter((ch) => ch.capacity_sat >= minCap)
    }
    const sorted = [...list]
    const direction = sortDir === 'desc' ? -1 : 1
    sorted.sort((a, b) => {
      if (sortBy === 'alias') {
        const aVal = (a.peer_alias || a.remote_pubkey || '').toLowerCase()
        const bVal = (b.peer_alias || b.remote_pubkey || '').toLowerCase()
        return aVal.localeCompare(bVal) * direction
      }
      const aVal = sortBy === 'capacity'
        ? a.capacity_sat
        : sortBy === 'local'
          ? a.local_balance_sat
          : a.remote_balance_sat
      const bVal = sortBy === 'capacity'
        ? b.capacity_sat
        : sortBy === 'local'
          ? b.local_balance_sat
          : b.remote_balance_sat
      return (aVal - bVal) * direction
    })
    return sorted
  }, [channels, filter, minCapacity, search, showPrivate, sortBy, sortDir])

  const handleConnectPeer = async () => {
    setPeerStatus('Connecting...')
    try {
      await connectPeer({ address: peerAddress, perm: !peerTemporary })
      setPeerStatus('Peer connected.')
      setPeerAddress('')
      setPeerTemporary(false)
      load()
    } catch (err: any) {
      setPeerStatus(err?.message || 'Peer connection failed.')
    }
  }

  const handleDisconnect = async (pubkey: string) => {
    const confirmed = window.confirm('Disconnect this peer?')
    if (!confirmed) return
    setPeerActionStatus('Disconnecting peer...')
    try {
      await disconnectPeer({ pubkey })
      setPeerActionStatus('Peer disconnected.')
      load()
    } catch (err: any) {
      setPeerActionStatus(err?.message || 'Disconnect failed.')
    }
  }

  const handleOpenChannel = async () => {
    setOpenStatus('Opening channel...')
    const localFunding = Number(openAmount || 0)
    const push = Number(openPush || 0)
    if (!openPubkey.trim()) {
      setOpenStatus('Pubkey required.')
      return
    }
    if (localFunding < 20000) {
      setOpenStatus('Minimum channel size is 20000 sat.')
      return
    }
    try {
      const res = await openChannel({
        pubkey: openPubkey.trim(),
        local_funding_sat: localFunding,
        push_sat: push > 0 ? push : 0,
        private: openPrivate
      })
      setOpenStatus(`Channel opening: ${res?.channel_point || 'submitted'}`)
      setOpenAmount('')
      setOpenPush('')
      load()
    } catch (err: any) {
      setOpenStatus(err?.message || 'Channel open failed.')
    }
  }

  const handleCloseChannel = async () => {
    setCloseStatus('Closing channel...')
    if (!closePoint) {
      setCloseStatus('Select a channel to close.')
      return
    }
    try {
      await closeChannel({ channel_point: closePoint, force: closeForce })
      setCloseStatus('Close initiated.')
      load()
    } catch (err: any) {
      setCloseStatus(err?.message || 'Close failed.')
    }
  }

  const handleUpdateFees = async () => {
    setFeeStatus('Updating fees...')
    const base = Number(baseFeeMsat || 0)
    const ppm = Number(feeRatePpm || 0)
    const delta = Number(timeLockDelta || 0)
    if (!feeScopeAll && !feeChannelPoint) {
      setFeeStatus('Select a channel or apply to all.')
      return
    }
    if (base === 0 && ppm === 0 && delta === 0) {
      setFeeStatus('Set at least one fee value.')
      return
    }
    try {
      await updateChannelFees({
        apply_all: feeScopeAll,
        channel_point: feeScopeAll ? undefined : feeChannelPoint,
        base_fee_msat: base,
        fee_rate_ppm: ppm,
        time_lock_delta: delta
      })
      setFeeStatus('Fees updated.')
      load()
    } catch (err: any) {
      setFeeStatus(err?.message || 'Fee update failed.')
    }
  }

  const channelOptions = useMemo(() => {
    return channels.map((ch) => ({
      value: ch.channel_point,
      label: `${ch.peer_alias || ch.remote_pubkey.slice(0, 12)} • ${ch.channel_point}`
    }))
  }, [channels])

  return (
    <section className="space-y-6">
      <div className="section-card">
        <div className="flex flex-col lg:flex-row lg:items-center lg:justify-between gap-4">
          <div>
            <h2 className="text-2xl font-semibold">Lightning Ops</h2>
            <p className="text-fog/60">Manage peers, channels, and routing fees.</p>
          </div>
          <div className="flex items-center gap-3">
            <div className="rounded-full border border-white/10 bg-ink/60 px-4 py-2 text-xs text-fog/70">
              Active: <span className="text-fog">{activeCount}</span>
            </div>
            <div className="rounded-full border border-white/10 bg-ink/60 px-4 py-2 text-xs text-fog/70">
              Inactive: <span className="text-fog">{inactiveCount}</span>
            </div>
            <button className="btn-secondary text-xs px-3 py-2" onClick={load}>
              Refresh
            </button>
          </div>
        </div>
        {status && <p className="mt-4 text-sm text-brass">{status}</p>}
      </div>

      <div className="grid gap-6 lg:grid-cols-2">
        <div className="section-card space-y-4">
          <h3 className="text-lg font-semibold">Add peer</h3>
          <input
            className="input-field"
            placeholder="pubkey@host:port"
            value={peerAddress}
            onChange={(e) => setPeerAddress(e.target.value)}
          />
          <label className="flex items-center gap-2 text-sm text-fog/70">
            <input
              type="checkbox"
              checked={peerTemporary}
              onChange={(e) => setPeerTemporary(e.target.checked)}
            />
            Temporary peer (no auto-reconnect)
          </label>
          <button className="btn-primary" onClick={handleConnectPeer}>Connect peer</button>
          {peerStatus && <p className="text-sm text-brass">{peerStatus}</p>}
        </div>

        <div className="section-card space-y-4">
          <h3 className="text-lg font-semibold">Open channel</h3>
          <input
            className="input-field"
            placeholder="Peer pubkey (hex)"
            value={openPubkey}
            onChange={(e) => setOpenPubkey(e.target.value)}
          />
          <div className="grid gap-4 lg:grid-cols-2">
            <input
              className="input-field"
              placeholder="Funding amount (sat)"
              type="number"
              min={20000}
              value={openAmount}
              onChange={(e) => setOpenAmount(e.target.value)}
            />
            <input
              className="input-field"
              placeholder="Push amount (sat, optional)"
              type="number"
              min={0}
              value={openPush}
              onChange={(e) => setOpenPush(e.target.value)}
            />
          </div>
          <label className="flex items-center gap-2 text-sm text-fog/70">
            <input type="checkbox" checked={openPrivate} onChange={(e) => setOpenPrivate(e.target.checked)} />
            Private channel
          </label>
          <button className="btn-primary" onClick={handleOpenChannel}>Open channel</button>
          <p className="text-xs text-fog/50">Minimum funding is 20000 sat. Opening a channel can take a few blocks.</p>
          {openStatus && <p className="text-sm text-brass">{openStatus}</p>}
        </div>
      </div>

      <div className="grid gap-6 lg:grid-cols-2">
        <div className="section-card space-y-4">
          <h3 className="text-lg font-semibold">Close channel</h3>
          <select className="input-field" value={closePoint} onChange={(e) => setClosePoint(e.target.value)}>
            <option value="">Select channel</option>
            {channelOptions.map((opt) => (
              <option key={opt.value} value={opt.value}>{opt.label}</option>
            ))}
          </select>
          <label className="flex items-center gap-2 text-sm text-fog/70">
            <input type="checkbox" checked={closeForce} onChange={(e) => setCloseForce(e.target.checked)} />
            Force close (not recommended)
          </label>
          <button className="btn-secondary" onClick={handleCloseChannel}>Close channel</button>
          {closeStatus && <p className="text-sm text-brass">{closeStatus}</p>}
        </div>

        <div className="section-card space-y-4">
          <h3 className="text-lg font-semibold">Update channel fees</h3>
          <div className="flex flex-wrap gap-3 text-sm">
            <button
              className={feeScopeAll ? 'btn-primary' : 'btn-secondary'}
              onClick={() => setFeeScopeAll(true)}
            >
              Apply to all
            </button>
            <button
              className={!feeScopeAll ? 'btn-primary' : 'btn-secondary'}
              onClick={() => setFeeScopeAll(false)}
            >
              Apply to one
            </button>
          </div>
          {!feeScopeAll && (
            <select className="input-field" value={feeChannelPoint} onChange={(e) => setFeeChannelPoint(e.target.value)}>
              <option value="">Select channel</option>
              {channelOptions.map((opt) => (
                <option key={opt.value} value={opt.value}>{opt.label}</option>
              ))}
            </select>
          )}
          <div className="grid gap-4 lg:grid-cols-3">
            <input
              className="input-field"
              placeholder="Base fee (msat)"
              type="number"
              min={0}
              value={baseFeeMsat}
              onChange={(e) => setBaseFeeMsat(e.target.value)}
            />
            <input
              className="input-field"
              placeholder="Fee rate (ppm)"
              type="number"
              min={0}
              value={feeRatePpm}
              onChange={(e) => setFeeRatePpm(e.target.value)}
            />
            <input
              className="input-field"
              placeholder="Timelock delta"
              type="number"
              min={0}
              value={timeLockDelta}
              onChange={(e) => setTimeLockDelta(e.target.value)}
            />
          </div>
          <button className="btn-secondary" onClick={handleUpdateFees}>Update fees</button>
          {feeStatus && <p className="text-sm text-brass">{feeStatus}</p>}
        </div>
      </div>

      <div className="section-card space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h3 className="text-lg font-semibold">Channels</h3>
          <div className="flex flex-wrap gap-2 text-xs">
            <button className={filter === 'all' ? 'btn-primary' : 'btn-secondary'} onClick={() => setFilter('all')}>All</button>
            <button className={filter === 'active' ? 'btn-primary' : 'btn-secondary'} onClick={() => setFilter('active')}>Active</button>
            <button className={filter === 'inactive' ? 'btn-primary' : 'btn-secondary'} onClick={() => setFilter('inactive')}>Inactive</button>
          </div>
        </div>
        <div className="grid gap-3 lg:grid-cols-4">
          <input
            className="input-field"
            placeholder="Search alias, pubkey, or channel point"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
          <input
            className="input-field"
            placeholder="Min capacity (sat)"
            type="number"
            min={0}
            value={minCapacity}
            onChange={(e) => setMinCapacity(e.target.value)}
          />
          <select className="input-field" value={sortBy} onChange={(e) => setSortBy(e.target.value as any)}>
            <option value="capacity">Sort by capacity</option>
            <option value="local">Sort by local balance</option>
            <option value="remote">Sort by remote balance</option>
            <option value="alias">Sort by peer</option>
          </select>
          <div className="flex items-center gap-2">
            <button className="btn-secondary text-xs px-3 py-2" onClick={() => setSortDir(sortDir === 'desc' ? 'asc' : 'desc')}>
              {sortDir === 'desc' ? 'Desc' : 'Asc'}
            </button>
            <label className="flex items-center gap-2 text-xs text-fog/70">
              <input type="checkbox" checked={showPrivate} onChange={(e) => setShowPrivate(e.target.checked)} />
              Show private
            </label>
          </div>
        </div>
        {filteredChannels.length ? (
          <div className="grid gap-3">
            {filteredChannels.map((ch) => (
              <div key={ch.channel_point} className="rounded-2xl border border-white/10 bg-ink/60 p-4">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <div>
                    <p className="text-sm text-fog/60">{ch.peer_alias || ch.remote_pubkey}</p>
                    <p className="text-xs text-fog/50">Point: {ch.channel_point}</p>
                  </div>
                  <span className={`rounded-full px-3 py-1 text-xs ${ch.active ? 'bg-glow/20 text-glow' : 'bg-ember/20 text-ember'}`}>
                    {ch.active ? 'Active' : 'Inactive'}
                  </span>
                </div>
                <div className="mt-3 grid gap-3 lg:grid-cols-3 text-xs text-fog/70">
                  <div>Capacity: <span className="text-fog">{ch.capacity_sat} sat</span></div>
                  <div>Local: <span className="text-fog">{ch.local_balance_sat} sat</span></div>
                  <div>Remote: <span className="text-fog">{ch.remote_balance_sat} sat</span></div>
                </div>
                <div className="mt-2 text-xs text-fog/50">
                  {ch.private ? 'Private channel' : 'Public channel'}
                </div>
              </div>
            ))}
          </div>
        ) : (
          <p className="text-sm text-fog/60">No channels found.</p>
        )}
      </div>

      <div className="section-card space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h3 className="text-lg font-semibold">Peers</h3>
          <span className="text-xs text-fog/60">Connected: {peers.length}</span>
        </div>
        {peerActionStatus && <p className="text-sm text-brass">{peerActionStatus}</p>}
        {peerListStatus && <p className="text-sm text-brass">{peerListStatus}</p>}
        {peers.length ? (
          <div className="grid gap-3">
            {peers.map((peer) => (
              <div key={peer.pub_key} className="rounded-2xl border border-white/10 bg-ink/60 p-4">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <div>
                    <p className="text-sm text-fog/60">{peer.alias || peer.pub_key}</p>
                    <p className="text-xs text-fog/50">{peer.address || 'address unknown'}</p>
                  </div>
                  <div className="flex items-center gap-2">
                    <span className="rounded-full px-3 py-1 text-xs bg-white/10 text-fog/70">
                      {peer.inbound ? 'Inbound' : 'Outbound'}
                    </span>
                    <button className="btn-secondary text-xs px-3 py-1.5" onClick={() => handleDisconnect(peer.pub_key)}>
                      Disconnect
                    </button>
                  </div>
                </div>
                {peer.alias && (
                  <p className="mt-2 text-xs text-fog/50">Pubkey: {peer.pub_key}</p>
                )}
                <div className="mt-3 grid gap-3 lg:grid-cols-3 text-xs text-fog/70">
                  <div>Sat sent: <span className="text-fog">{peer.sat_sent}</span></div>
                  <div>Sat recv: <span className="text-fog">{peer.sat_recv}</span></div>
                  <div>Ping: <span className="text-fog">{peer.ping_time} ms</span></div>
                </div>
                <div className="mt-2 grid gap-3 lg:grid-cols-2 text-xs text-fog/60">
                  <div>Bytes sent: <span className="text-fog">{peer.bytes_sent}</span></div>
                  <div>Bytes recv: <span className="text-fog">{peer.bytes_recv}</span></div>
                </div>
                {peer.sync_type && (
                  <p className="mt-2 text-xs text-fog/50">Sync: {peer.sync_type}</p>
                )}
                {peer.last_error && (
                  <p className="mt-2 text-xs text-ember">Last error: {peer.last_error}</p>
                )}
              </div>
            ))}
          </div>
        ) : (
          <p className="text-sm text-fog/60">No connected peers found.</p>
        )}
      </div>
    </section>
  )
}
