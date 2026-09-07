import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { getMeshStatus, meshAction, type MeshAction, type MeshPreview, type MeshStatus } from '../api'
import meshIcon from '../assets/apps/los-mesh.svg'

export default function LOSMesh() {
  const { i18n } = useTranslation()
  const pt = i18n.language.startsWith('pt')
  const text = (br: string, en: string) => pt ? br : en
  const [status, setStatus] = useState<MeshStatus | null>(null)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)
  const [device, setDevice] = useState('')
  const [mode, setMode] = useState('send')
  const [peer, setPeer] = useState('')
  const [nodeID, setNodeID] = useState('')
  const [name, setName] = useState('')
  const [key, setKey] = useState('')
  const [allowRelay, setAllowRelay] = useState(false)
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState(false)
  const [raw, setRaw] = useState('')
  const [address, setAddress] = useState('')
  const [amount, setAmount] = useState('')
  const [rate, setRate] = useState('2')
  const [invoice, setInvoice] = useState('')
  const [memo, setMemo] = useState('')
  const [maxFee, setMaxFee] = useState('10')
  const [preview, setPreview] = useState<MeshPreview | null>(null)
  const [tab, setTab] = useState('onchain')
  const refresh = useCallback(async () => { const value = await getMeshStatus(); setStatus(value); return value }, [])
  useEffect(() => {
    let live = true
    const load = async () => { try { const value = await getMeshStatus(); if (live) setStatus(value) } catch (e) { if (live) setError(String(e)) } }
    void load(); const timer = window.setInterval(() => void load(), 8000)
    return () => { live = false; window.clearInterval(timer) }
  }, [])
  const act = async <T,>(payload: MeshAction): Promise<T | undefined> => {
    setBusy(true); setError(''); setNotice('')
    try {
      const response = await meshAction<T>({ ...payload, confirm_password: password })
      setPassword(''); setConfirm(false); await refresh()
      setNotice(text('Solicitação registrada. Acompanhe o estado abaixo.', 'Request recorded. Follow its status below.'))
      if (payload.action === 'install') window.dispatchEvent(new CustomEvent('apps:changed', { detail: { id: 'los-mesh' } }))
      return response
    } catch (e) { setError(e instanceof Error ? e.message : String(e)); return undefined }
    finally { setBusy(false) }
  }
  const label = (title: string, children: ReactNode) => <label className="block space-y-2 text-sm"><span className="text-fog/75">{title}</span>{children}</label>
  const states: Record<string, string> = {
    not_installed: text('Não instalado', 'Not installed'), stopped: text('Parado', 'Stopped'), running: text('Conectado', 'Connected'),
    hardware_disconnected: text('Rádio USB desconectado', 'USB radio disconnected'), connecting: text('Conectando ao rádio', 'Connecting to radio'),
    awaiting_pairing: text('Aguardando pareamento', 'Awaiting pairing'), communication_error: text('Erro de comunicação', 'Communication error'), upgrade_required: text('Atualização necessária', 'Upgrade required'),
    sending: text('Enviando pacotes', 'Sending packets'), receiving: text('Recebendo pacotes', 'Receiving packets'), awaiting_result: text('Aguardando resposta', 'Awaiting result'),
    published: text('Publicada — aguarda confirmação na blockchain', 'Published — awaiting blockchain confirmation'), rejected: text('Rejeitada', 'Rejected'),
    publication_unknown: text('Publicação incerta — consulte o TXID antes de tentar novamente', 'Publication uncertain — check the TXID before retrying'),
    incomplete: text('Transmissão incompleta', 'Incomplete transmission'), interrupted: text('Interrompida pelo reinício', 'Interrupted by restart'),
    expired: text('Expirada', 'Expired'), cancelled: text('Cancelada', 'Cancelled'), relay_disabled: text('Relay não autorizado no destino', 'Destination relay not authorized'),
    awaiting_approval: text('Recebida — aguarda aprovação local', 'Received — awaiting local approval'), paid: text('Pagamento confirmado pelo LND', 'Payment confirmed by LND'), payment_unknown: text('Resultado incerto — consulte a carteira', 'Uncertain result — check wallet activity')
  }
  const connected = status?.radio.state === 'running' || status?.radio.state === 'awaiting_pairing'
  const canSend = connected && status?.mode !== 'relay' && Boolean(peer)
  const input = 'input w-full'
  const paired = status?.peers.filter(p => p.paired) || []
  const review = async () => { const value = await act<MeshPreview>({ action: 'preview', node: Number(peer), raw_tx: raw || undefined, address, amount_sat: Number(amount), sat_per_vbyte: Number(rate) }); if (value) setPreview(value) }
  return <section className="space-y-6">
    <div className="section-card flex flex-wrap items-center justify-between gap-4">
      <div className="flex items-center gap-4"><img src={meshIcon} className="h-14 w-14" alt="" /><div><h2 className="text-2xl font-semibold">LOS Mesh</h2><p className="text-sm text-fog/60">Bitcoin · Lightning · Meshtastic LoRa</p></div></div>
      <span className="rounded-full border border-emerald-400/30 px-4 py-2 text-sm">{states[status?.radio.state || ''] || text('Carregando…', 'Loading…')}</span>
    </div>
    <p className="text-sm text-fog/70">{text('O rádio transporta solicitações e transações assinadas. Pagamentos Lightning usam a rede Lightning normal e exigem aprovação local. Use dois rádios compatíveis, na mesma região e canal Meshtastic.', 'The radio transports requests and signed transactions. Lightning payments use the normal Lightning network and require local approval. Use two compatible radios with matching Meshtastic region and channel settings.')} <a href="https://meshtastic.org/docs/" target="_blank" rel="noreferrer" className="text-emerald-300 underline">Meshtastic ↗</a></p>
    {error && <div role="alert" className="section-card text-red-300">{error}</div>}
    {notice && <p role="status" className="text-emerald-300">{notice}</p>}
    <div className="section-card space-y-4">
      <h3 className="text-lg font-semibold">{text('Rádio e operação', 'Radio and operation')}</h3>
      <div className="grid gap-4 md:grid-cols-2">
        {label(text('Dispositivo USB', 'USB device'), <select className={input} value={device || status?.app.device || ''} onChange={e => setDevice(e.target.value)}><option value="">{text('Selecione o rádio', 'Select a radio')}</option>{status?.app.devices.map(d => <option key={d} value={d}>{d}</option>)}</select>)}
        {label(text('Modo', 'Mode'), <select className={input} value={mode} onChange={e => setMode(e.target.value)}><option value="send">{text('Somente envio (padrão)', 'Send only (default)')}</option><option value="relay">{text('Somente relay', 'Relay only')}</option><option value="both">{text('Bidirecional', 'Bidirectional')}</option></select>)}
      </div>
      {status && !status.app.devices.length && <p className="text-sm text-amber-200">{text('Nenhum rádio USB encontrado. Conecte o dispositivo e, em uma VM, habilite a conexão USB no hipervisor.', 'No USB radio found. Connect the device and, for a VM, enable USB passthrough in the hypervisor.')}</p>}
      <div className="flex flex-wrap gap-3 text-sm text-fog/65"><span>{text('ID local', 'Local ID')}: {status?.radio.node ? `!${status.radio.node.toString(16).padStart(8, '0')}` : '—'}</span><span>SNR: {status?.radio.snr ?? '?'} dB ? RSSI: {status?.radio.rssi ?? '?'} dBm</span><span>{text('Modo ativo', 'Active mode')}: {status?.mode || '—'}</span><span>{text('Último pacote', 'Last packet')}: {status?.radio.last_receive && !status.radio.last_receive.startsWith('0001') ? new Date(status.radio.last_receive).toLocaleString() : '—'}</span></div>
      <p className="text-xs text-fog/60">{text('Relay publica apenas transações assinadas de contatos pareados com permissão explícita. Não existe relay público.', 'Relay publishes signed transactions only from paired contacts with explicit permission. There is no public relay.')}</p>
      <div className="flex flex-wrap gap-3"><button className="btn-primary" disabled={busy || !confirm || !(device || status?.app.device)} onClick={() => void act({ action: 'install', device: device || status?.app.device, mode, confirm })}>{text('Instalar / conectar rádio', 'Install / connect radio')}</button>{status?.app.installed && <button className="btn-secondary" disabled={busy || !confirm} onClick={() => void act({ action: 'mode', mode, confirm })}>{text('Aplicar modo', 'Apply mode')}</button>}</div>
    </div>
    <details className="section-card space-y-4" open={!paired.length}>
      <summary className="cursor-pointer text-lg font-semibold">{text('Contatos e pareamento privado', 'Contacts and private pairing')}</summary>
      <p className="text-sm text-fog/65">{text('Em cada LightningOS, cadastre o ID do rádio do outro lado e a mesma chave de 64 caracteres hexadecimais. Compartilhe a chave por um canal seguro. Salve nos dois lados e repita o pareamento se necessário.', 'On each LightningOS, enter the other radio’s ID and the same 64-character hexadecimal key. Share it through a secure channel. Save on both sides and repeat pairing if necessary.')}</p>
      <div className="grid gap-4 md:grid-cols-2">{label(text('Nome do contato', 'Contact name'), <input className={input} value={name} maxLength={64} onChange={e => setName(e.target.value)} />)}{label(text('ID do rádio remoto (!xxxxxxxx)', 'Remote radio ID (!xxxxxxxx)'), <input className={input} value={nodeID} placeholder="!1234abcd" onChange={e => setNodeID(e.target.value)} />)}</div>
      {label(text('Chave compartilhada', 'Shared key'), <input className={`${input} font-mono`} autoComplete="off" type="password" value={key} maxLength={64} onChange={e => setKey(e.target.value)} />)}
      <div className="flex flex-wrap gap-3"><button className="btn-secondary" onClick={() => setKey(Array.from(crypto.getRandomValues(new Uint8Array(32)), n => n.toString(16).padStart(2, '0')).join(''))}>{text('Gerar chave', 'Generate key')}</button><button className="btn-secondary" disabled={!key} onClick={() => void navigator.clipboard.writeText(key).then(() => setNotice(text('Chave copiada. Compartilhe com segurança.', 'Key copied. Share securely.'))).catch(() => setError(text('Não foi possível copiar.', 'Copy failed.')))}>{text('Copiar chave', 'Copy key')}</button></div>
      <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={allowRelay} onChange={e => setAllowRelay(e.target.checked)} />{text('Autorizar este contato a publicar transações assinadas pelo meu relay', 'Allow this contact to publish signed transactions through my relay')}</label>
      <button className="btn-primary" disabled={busy || !connected || !confirm || !/^!?[0-9a-fA-F]{8}$/.test(nodeID)} onClick={async () => { const result = await act({ action: 'peer', node: parseInt(nodeID.replace('!', ''), 16), name, key, allow_relay: allowRelay, confirm }); if (result) setKey('') }}>{text('Salvar e parear', 'Save and pair')}</button>
      <div className="space-y-2">{status?.peers.map(p => <div key={p.node} className="rounded-xl border border-white/10 p-3 text-sm"><div className="flex flex-wrap justify-between gap-2"><strong>{p.name} · !{p.node.toString(16).padStart(8, '0')}</strong><span>{p.paired ? text('Pareado', 'Paired') : text('Aguardando prova da chave', 'Awaiting key proof')}</span></div><p className="my-2 font-mono text-xs">{p.fingerprint} · relay {p.allow_relay ? '✓' : '—'}</p><button className="btn-secondary" disabled={busy || !confirm} onClick={() => void act({ action: 'remove_peer', node: p.node, confirm })}>{text('Remover contato', 'Remove contact')}</button></div>)}</div>
    </details>
    <div className="section-card space-y-4">
      <div className="flex flex-wrap gap-3">{['onchain', 'lightning'].map(t => <button key={t} className={tab === t ? 'btn-primary' : 'btn-secondary'} onClick={() => setTab(t)}>{t === 'onchain' ? 'Bitcoin on-chain' : 'Lightning'}</button>)}</div>
      {label(text('Contato de destino', 'Destination contact'), <select className={input} value={peer} onChange={e => setPeer(e.target.value)}><option value="">{text('Selecione um contato pareado', 'Select a paired contact')}</option>{paired.map(p => <option key={p.node} value={p.node}>{p.name}</option>)}</select>)}
      {tab === 'onchain' ? <>
        <p className="text-sm text-fog/65">{text('Importe uma transação já assinada ou crie uma transação com fundos confirmados da carteira LND. A assinatura ocorre após sua aprovação; a publicação é feita pelo relay remoto.', 'Import a signed transaction or create one using confirmed LND wallet funds. Signing follows your approval; the remote relay publishes the transaction.')}</p>
        {label(text('Endereço Bitcoin mainnet', 'Bitcoin mainnet address'), <input className={input} value={address} onChange={e => setAddress(e.target.value)} />)}
        <div className="grid gap-4 md:grid-cols-2">{label(text('Valor (sats)', 'Amount (sats)'), <input className={input} type="number" min="1" value={amount} onChange={e => setAmount(e.target.value)} />)}{label('sat/vB (1–1000)', <input className={input} type="number" min="1" max="1000" value={rate} onChange={e => setRate(e.target.value)} />)}</div>
        <details><summary className="cursor-pointer text-sm">{text('Importar transação assinada', 'Import signed transaction')}</summary><textarea className={`${input} mt-3 min-h-28 font-mono text-xs`} value={raw} maxLength={30720} onChange={e => setRaw(e.target.value)} placeholder="Raw transaction hex" /><p className="text-xs text-fog/60">{text('Quando preenchido, o raw substitui os campos de criação acima. Até 15.360 bytes.', 'When provided, raw transaction data overrides the creation fields above. Up to 15,360 bytes.')}</p></details>
        <div className="flex flex-wrap gap-3"><button className="btn-primary" disabled={busy || !canSend || Boolean(preview)} onClick={() => void review()}>{text('Revisar transação', 'Review transaction')}</button><button className="btn-secondary" disabled={busy || !canSend || !address || !amount} onClick={() => void act({ action: 'request', node: Number(peer), address, amount_sat: Number(amount), memo })}>{text('Enviar solicitação de recebimento', 'Send payment request')}</button></div>
        {preview && <div className="space-y-3 rounded-xl border border-amber-400/35 p-4">
          <h4 className="font-semibold">{text('Aprovação da transação', 'Transaction approval')}</h4>
          {preview.preview.address && <><p className="break-all">{preview.preview.address}</p><p>{text('Destino', 'Recipient')}: {preview.preview.recipient_amount_sat} sats · {text('Taxa', 'Fee')}: {preview.preview.fee_sat} sats</p><p>{text('Troco', 'Change')}: {preview.preview.change_sat} sats · {text('Débito total', 'Total debit')}: {preview.preview.total_debit_sat} sats</p><p>{text('Entradas selecionadas', 'Selected inputs')}: {preview.preview.selected_input_count}</p></>}
          {preview.preview.outputs?.map((o, i) => <p key={i} className="break-all">{o.address} · {o.sats} sats</p>)}
          {preview.preview.txid && <p className="break-all font-mono text-xs">TXID: {preview.preview.txid}<br />{preview.preview.bytes} bytes · {preview.preview.chunks} {text('pacotes', 'packets')}</p>}
          {preview.preview.txid && <p className="text-sm text-amber-200">{text('A taxa da transação importada depende das entradas e não foi verificada nesta prévia.', 'The imported transaction fee depends on its inputs and has not been verified in this preview.')}</p>}
          <p className="text-sm text-amber-200">{text('Uma transação assinada pode gastar fundos. Cancelar o envio pelo rádio não revoga uma assinatura já transmitida.', 'A signed transaction can spend funds. Cancelling radio transmission does not revoke a signature already transmitted.')}</p>
          <div className="flex gap-3"><button className="btn-primary" disabled={busy || !confirm || Date.now() >= Date.parse(preview.expires)} onClick={async () => { const result = await act({ action: 'send', id: preview.id, confirm }); if (result) { setPreview(null); setRaw('') } }}>{text('Aprovar e transmitir', 'Approve and transmit')}</button><button className="btn-secondary" disabled={busy} onClick={async () => { await act({ action: 'cancel', id: preview.id }); setPreview(null) }}>{text('Cancelar prévia', 'Cancel preview')}</button></div>
        </div>}
      </> : <>
        <p className="text-sm text-fog/65">{text('Crie uma invoice para receber ou envie uma BOLT11 existente. O destinatário decide localmente se deseja pagar.', 'Create an invoice to receive funds or send an existing BOLT11. The recipient decides locally whether to pay.')}</p>
        {label(text('Valor (sats)', 'Amount (sats)'), <input className={input} type="number" min="1" value={amount} onChange={e => setAmount(e.target.value)} />)}
        {label(text('Descrição', 'Description'), <input className={input} value={memo} maxLength={120} onChange={e => setMemo(e.target.value)} />)}
        {label(text('BOLT11 existente (opcional)', 'Existing BOLT11 (optional)'), <textarea className={`${input} font-mono text-xs`} value={invoice} maxLength={4096} onChange={e => setInvoice(e.target.value)} />)}
        <button className="btn-primary" disabled={busy || !canSend} onClick={() => void act({ action: 'invoice', node: Number(peer), invoice, amount_sat: Number(amount), memo })}>{text('Enviar invoice', 'Send invoice')}</button>
      </>}
    </div>
    <div className="section-card space-y-4"><h3 className="text-lg font-semibold">{text('Solicitações recebidas', 'Received requests')}</h3>
      {!status?.pending.length && <p className="text-sm text-fog/60">{text('Nenhuma solicitação aguardando aprovação.', 'No requests awaiting approval.')}</p>}
      {status?.pending.map(p => <div key={p.id} className="space-y-3 rounded-xl border border-white/10 p-4"><p><strong>{p.kind === 'invoice' ? 'Lightning' : 'Bitcoin'} · {p.amount_sat} sats</strong> · {status.peers.find(peer => peer.node === p.peer)?.name}</p><p className="break-all text-sm">{p.memo}</p><p className="break-all font-mono text-xs">{p.address || p.destination}<br />{p.payment_hash}</p><p className="text-xs">{text('Expira', 'Expires')}: {new Date(p.expires).toLocaleString()}</p>
        {p.kind === 'invoice' ? <>{label(text('Taxa máxima Lightning (sats)', 'Maximum Lightning fee (sats)'), <input className={input} type="number" min="0" max="100000" value={maxFee} onChange={e => setMaxFee(e.target.value)} />)}<button className="btn-primary" disabled={busy || !confirm} onClick={() => void act({ action: 'pay', id: p.id, amount_sat: p.amount_sat, max_fee_sat: Number(maxFee), confirm })}>{text('Aprovar e pagar via Lightning', 'Approve and pay via Lightning')}</button></> : <button className="btn-primary" disabled={busy || Boolean(preview)} onClick={() => { setPeer(String(p.peer)); setAddress(p.address || ''); setAmount(String(p.amount_sat)); setRaw(''); setTab('onchain'); setNotice(text('Solicitação carregada. Revise a transação antes de aprovar.', 'Request loaded. Review the transaction before approving.')) }}>{text('Preparar prévia on-chain', 'Prepare on-chain preview')}</button>}
        <button className="btn-secondary ml-3" disabled={busy} onClick={() => void act({ action: 'cancel', id: p.id })}>{text('Rejeitar', 'Reject')}</button>
      </div>)}
    </div>
    <div className="section-card space-y-4 border-amber-400/20">
      <h3 className="font-semibold">{text('Confirmação local', 'Local confirmation')}</h3>
      {label(text('Senha do LightningOS', 'LightningOS password'), <input className={input} type="password" autoComplete="current-password" value={password} onChange={e => setPassword(e.target.value)} />)}
      <label className="flex items-center gap-3 text-sm"><input type="checkbox" checked={confirm} onChange={e => setConfirm(e.target.checked)} />{text('Revisei os dados e autorizo a ação selecionada.', 'I reviewed the details and authorize the selected action.')}</label>
    </div>
    <div className="section-card space-y-4"><h3 className="text-lg font-semibold">{text('Histórico de sessões', 'Session history')}</h3><p className="text-xs text-fog/60">{text('Até 100 sessões recentes; retenção de 30 dias. Sem transações brutas, invoices completas ou preimages no histórico.', 'Up to 100 recent sessions; 30-day retention. No raw transactions, full invoices or preimages in history.')}</p>
      {!status?.history.length && <p className="text-sm text-fog/60">{text('Nenhuma sessão registrada.', 'No sessions recorded.')}</p>}
      {status?.history.map(h => <div key={h.id} className="space-y-2 rounded-xl border border-white/10 p-3 text-sm"><p className="font-semibold">{h.direction === 'in' ? '↓' : '↑'} {states[h.state] || h.state}</p><p className="text-xs text-fog/65">{new Date(h.created).toLocaleString()} · {h.received}/{h.total} {text('pacotes', 'packets')} · !{h.peer.toString(16).padStart(8, '0')}</p>{h.txid && <a className="block break-all font-mono text-xs text-emerald-300 underline" href={`https://mempool.space/tx/${h.txid}`} target="_blank" rel="noreferrer">{h.txid} ↗</a>}{['sending', 'receiving', 'awaiting_result'].includes(h.state) && <button className="btn-secondary" disabled={busy} onClick={() => void act({ action: 'cancel', id: h.id })}>{text('Cancelar envio', 'Cancel transfer')}</button>}</div>)}
    </div>
  </section>
}
