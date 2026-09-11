import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { getWalletAddress, getMeshStatus, meshAction, type MeshAction, type MeshPreview, type MeshStatus } from '../api'
import SensitiveActionModal from '../components/SensitiveActionModal'
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
  const [transport, setTransport] = useState('')
  const [tcpEndpoint, setTcpEndpoint] = useState<string | null>(null)
  const [mode, setMode] = useState('')
  const [peer, setPeer] = useState('')
  const [nodeSearch, setNodeSearch] = useState('')
  const [nodePage, setNodePage] = useState(0)
  const nodeListRef = useRef<HTMLDivElement>(null)
  const filteredNodes = (status?.nodes || []).filter(n => `${n.name} ${n.short_name} !${n.node.toString(16).padStart(8, '0')}`.toLowerCase().includes(nodeSearch.trim().toLowerCase()))
  const nodePageSize = 12
  const nodePageCount = Math.max(1, Math.ceil(filteredNodes.length / nodePageSize))
  const currentNodePage = Math.min(nodePage, nodePageCount - 1)
  useEffect(() => { setNodePage(page => Math.min(page, nodePageCount - 1)) }, [nodePageCount])
  useEffect(() => { nodeListRef.current?.scrollTo({ top: 0 }) }, [currentNodePage, nodeSearch])
  const [compareConfirmed, setCompareConfirmed] = useState<Record<string, boolean>>({})
  const [nodeID, setNodeID] = useState('')
  const [name, setName] = useState('')
  const [key, setKey] = useState('')
  const [allowRelay, setAllowRelay] = useState(false)
  const [password, setPassword] = useState('')
  const [section, setSection] = useState<'radio' | 'contacts' | 'payments'>('radio')
  const [approval, setApproval] = useState<MeshAction | null>(null)
  const [approvalError, setApprovalError] = useState('')
  const approvalResult = useRef<((value: unknown) => void) | null>(null)
  useEffect(() => () => { approvalResult.current?.(undefined) }, [])
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
  const execute = async <T,>(payload: MeshAction, confirmationPassword?: string): Promise<T | undefined> => {
    setBusy(true); setError(''); setNotice('')
    try {
      const response = await meshAction<T>({ ...payload, confirm_password: confirmationPassword })
      await refresh().catch(() => setError(text('A ação foi concluída, mas não foi possível atualizar o estado.', 'The action completed, but status could not be refreshed.')))
      if (payload.action === 'mode' || payload.action === 'install') setMode('')
      setNotice(text('Solicitação registrada. Acompanhe o estado abaixo.', 'Request recorded. Follow its status below.'))
      if (payload.action === 'install') window.dispatchEvent(new CustomEvent('apps:changed', { detail: { id: 'los-mesh' } }))
      return response
    } catch (e) { const message = e instanceof Error ? e.message : String(e); if (confirmationPassword !== undefined) setApprovalError(message); else setError(message); return undefined }
    finally { setBusy(false) }
  }
  const act = <T,>(payload: MeshAction): Promise<T | undefined> => {
    if (!['install', 'mode', 'peer', 'remove_peer', 'send', 'pay', 'pair_invite', 'pair_accept', 'pair_confirm', 'peer_permissions'].includes(payload.action)) return execute<T>(payload)
    if (approvalResult.current) return Promise.resolve(undefined)
    setPassword(''); setApprovalError(''); setApproval({ ...payload, confirm: true })
    return new Promise(resolve => { approvalResult.current = value => resolve(value as T | undefined) })
  }
  const closeApproval = () => {
    if (busy) return
    approvalResult.current?.(undefined); approvalResult.current = null
    setApproval(null); setPassword(''); setApprovalError('')
  }
  const confirmApproval = async () => {
    if (!approval || busy) return
    if (!password.trim()) { setApprovalError(text('Informe sua senha do LightningOS.', 'Enter your LightningOS password.')); return }
    setApprovalError('')
    const result = await execute(approval, password)
    setPassword('')
    if (result !== undefined) {
      approvalResult.current?.(result); approvalResult.current = null
      setApproval(null); setApprovalError('')
    }
  }
  const label = (title: string, children: ReactNode) => <label className="block space-y-2 text-sm"><span className="text-fog/75">{title}</span>{children}</label>
  const states: Record<string, string> = {
    not_installed: text('Não instalado', 'Not installed'), stopped: text('Parado', 'Stopped'), running: text('Conectado', 'Connected'),
    hardware_disconnected: text('Rádio desconectado — tentando reconectar', 'Radio disconnected — reconnecting'), connecting: text('Conectando ao rádio', 'Connecting to radio'),
    awaiting_pairing: text('Aguardando pareamento', 'Awaiting pairing'), communication_error: text('Erro de comunicação', 'Communication error'), upgrade_required: text('Atualização necessária', 'Upgrade required'),
    sending: text('Enviando pacotes', 'Sending packets'), receiving: text('Recebendo pacotes', 'Receiving packets'), awaiting_result: text('Aguardando resposta', 'Awaiting result'),
    published: text('Publicada — aguarda confirmação na blockchain', 'Published — awaiting blockchain confirmation'), rejected: text('Rejeitada', 'Rejected'),
    publication_unknown: text('Publicação incerta — consulte o TXID antes de tentar novamente', 'Publication uncertain — check the TXID before retrying'),
    incomplete: text('Transmissão incompleta', 'Incomplete transmission'), interrupted: text('Interrompida pelo reinício', 'Interrupted by restart'),
    expired: text('Expirada', 'Expired'), cancelled: text('Cancelada', 'Cancelled'), relay_disabled: text('Relay não autorizado no destino', 'Destination relay not authorized'),
    awaiting_approval: text('Recebida — aguarda aprovação local', 'Received — awaiting local approval'), paid: text('Pagamento confirmado pelo LND', 'Payment confirmed by LND'), payment_unknown: text('Resultado incerto — consulte a carteira', 'Uncertain result — check wallet activity')
  }
  const connected = status?.radio.state === 'running' || status?.radio.state === 'awaiting_pairing'
  const selectedMode = mode || status?.mode || 'send'
  const activeTCP = Boolean(status?.app.device?.startsWith('tcp://'))
  const selectedTransport = transport || (activeTCP ? 'tcp' : 'usb')
  const selectedEndpoint = tcpEndpoint ?? (activeTCP ? status!.app.device.slice(6) : '')
  const selectedDevice = selectedTransport === 'tcp' ? `tcp://${selectedEndpoint.trim()}` : device || (!activeTCP ? status?.app.device : '') || ''
  const modeChanged = Boolean(status?.app.installed && selectedMode !== status.mode)
  const deviceChanged = Boolean(status?.app.installed && selectedDevice !== status.app.device)
  const deviceAvailable = selectedTransport === 'tcp' ? Boolean(selectedEndpoint.trim()) : Boolean(selectedDevice && status?.app.devices.includes(selectedDevice))
  const modeNames: Record<string, string> = { send: text('Enviar sem oferecer relay', 'Send without offering relay'), relay: text('Oferecer relay', 'Offer relay'), both: text('Enviar e oferecer relay', 'Send and offer relay') }
  const modeHints: Record<string, string> = {
    send: text('Envie transações e solicitações para outros contatos. Você continua recebendo respostas, convites e solicitações, mas seu LOS não publica transações recebidas de outros contatos.', 'Send transactions and requests to other contacts. You still receive replies, invitations and requests, but your LOS does not publish transactions received from other contacts.'),
    relay: text('Use seu LOS conectado à internet para publicar transações já assinadas de contatos autorizados. Este modo bloqueia novos envios de transações e solicitações pelo LOS Mesh.', 'Use your internet-connected LOS to publish already signed transactions from authorized contacts. This mode blocks new transaction and request transfers through LOS Mesh.'),
    both: text('Envie transações e solicitações e também publique transações já assinadas de contatos autorizados. Escolha este modo para usar as duas funções.', 'Send transactions and requests and also publish already signed transactions from authorized contacts. Choose this mode to use both functions.')
  }
  const approvalTitle = approval?.action === 'pair_invite' ? text('Enviar convite', 'Send invitation')
    : approval?.action === 'pair_accept' ? text('Aceitar convite', 'Accept invitation')
    : approval?.action === 'pair_confirm' ? text('Confirmar contato', 'Confirm contact')
    : approval?.action === 'peer_permissions' ? text('Alterar permissões', 'Change permissions')
    : approval?.action === 'mode' ? text('Aplicar modo', 'Apply mode')
    : approval?.action === 'disconnect' ? text('Desconectar rádio', 'Disconnect radio')
    : approval?.action === 'install' ? text('Conectar rádio', 'Connect radio')
    : approval?.action === 'peer' ? text('Salvar e parear', 'Save and pair')
    : approval?.action === 'remove_peer' ? text('Remover contato', 'Remove contact')
    : approval?.action === 'send' ? text('Aprovar e transmitir', 'Approve and transmit')
    : text('Aprovar pagamento Lightning', 'Approve Lightning payment')
  const approvalDescription = approval?.action === 'disconnect'
    ? text('A conexão será encerrada e as tentativas de reconexão serão interrompidas. Seus contatos e configurações serão preservados. Use Reconectar rádio para voltar a conectar.', 'The connection will close and reconnection attempts will stop. Your contacts and settings will be preserved. Use Reconnect radio to connect again.')
    : approval?.action === 'pair_confirm'
    ? text('Confirme somente se comparou o código com o outro operador por um canal confiável e os dois são iguais. Isso não autoriza pagamentos nem relay.', 'Confirm only after comparing the code with the other operator over a trusted channel and finding an exact match. This does not authorize payments or relay.')
    : approval?.action === 'pair_invite' || approval?.action === 'pair_accept'
    ? text('O outro operador também precisa aceitar e confirmar o código. As chaves serão negociadas automaticamente.', 'The other operator must also accept and confirm the code. Keys are negotiated automatically.')
    : approval?.action === 'peer_permissions'
    ? `${text('Publicar transações deste contato pelo meu relay', 'Publish transactions from this contact through my relay')}: ${approval.allow_relay ? text('Permitir', 'Allow') : text('Bloquear', 'Block')}.`
    : approval?.action === 'mode'
    ? `${text('Novo modo', 'New mode')}: ${modeNames[approval.mode || '']}. ${text('Confirme sua senha para aplicar.', 'Confirm your password to apply.')}`
    : approval?.action === 'pay'
    ? `${approval.amount_sat} sats. ${text('Taxa máxima', 'Maximum fee')}: ${approval.max_fee_sat} sats. ${text('Confirme para pagar a invoice revisada.', 'Confirm to pay the reviewed invoice.')}`
    : approval?.action === 'peer'
    ? `${approval.name}. ${text('Permitir publicação pelo meu relay', 'Allow publication through my relay')}: ${approval.allow_relay ? text('Sim', 'Yes') : text('Não', 'No')}.`
    : approval?.action === 'remove_peer'
    ? `${text('Remover o contato e suas permissões', 'Remove contact and its permissions')}: ${status?.peers.find(p => p.node === approval.node)?.name || ''}.`
    : approval?.action === 'send'
    ? text('Confirme a transação apresentada na prévia. Uma assinatura transmitida não pode ser revogada pelo cancelamento do envio.', 'Confirm the transaction shown in the preview. Cancelling transmission cannot revoke a delivered signature.')
    : `${text('Dispositivo', 'Device')}: ${approval?.device || ''}. ${text('Modo', 'Mode')}: ${modeNames[approval?.mode || ''] || ''}. ${approval?.action === 'install' ? text('Esta será a única conexão ativa do LOS Mesh e substituirá a anterior. Dispositivos com MUI podem não suportar TCP; confira a compatibilidade do firmware ou utilize USB.', 'This will be the only active LOS Mesh connection and will replace the previous one. Devices running MUI may not support TCP; check firmware compatibility or use USB.') : ''}`
  const canSend = connected && status?.mode !== 'relay' && Boolean(peer)
  const input = 'input-field w-full'
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
    {!!status?.radio.last_error_code && <p role="alert" className="section-card text-amber-300">{text('Último erro de transporte informado pelo rádio', 'Last transport error reported by the radio')}: {({ 7: 'TOO_LARGE', 9: 'DUTY_CYCLE_LIMIT', 3: 'TIMEOUT', 5: 'MAX_RETRANSMIT', 6: 'NO_CHANNEL', 34: 'PKI_FAILED', 35: 'PKI_UNKNOWN_PUBKEY', 39: 'PKI_SEND_FAIL_PUBLIC_KEY' } as Record<number, string>)[status.radio.last_error_code] || status.radio.last_error_code} · {status.radio.last_error_at ? new Date(status.radio.last_error_at).toLocaleString() : ''}. {text('Esse aviso não confirma recebimento nem pagamento.', 'This report does not confirm receipt or payment.')}</p>}
    <p className="text-xs text-fog/65">{text('Para enviar invoices e transações, atualize os dois contatos para LOS 0.5.27 ou posterior.', 'To send invoices and transactions, update both contacts to LOS 0.5.27 or later.')}</p>
    <div role="tablist" aria-label={text('Seções do LOS Mesh', 'LOS Mesh sections')} className="flex flex-wrap gap-3">
      {(['radio', 'contacts', 'payments'] as const).map(value => <button key={value} id={`mesh-tab-${value}`} role="tab" aria-selected={section === value} aria-controls={`mesh-panel-${value}`} className={section === value ? 'btn-primary' : 'btn-secondary'} onClick={() => setSection(value)}>{value === 'radio' ? text('Rádio', 'Radio') : value === 'contacts' ? text('Contatos', 'Contacts') : text('Pagamentos', 'Payments')}</button>)}
    </div>
    <div id="mesh-panel-radio" role="tabpanel" aria-labelledby="mesh-tab-radio" hidden={section !== 'radio'}>
    <div className="section-card space-y-4">
      <h3 className="text-lg font-semibold">{text('Rádio e operação', 'Radio and operation')}</h3>
      {label(text('Tipo de conexão', 'Connection type'), <select className={input} value={selectedTransport} disabled={busy} onChange={e => setTransport(e.target.value)}><option value="usb">USB / Serial</option><option value="tcp">{text('Rede local — TCP', 'Local network — TCP')}</option></select>)}
      <p className="text-sm text-fog/75 break-all">{text('Conexão ativa', 'Active connection')}: {status?.app.device || '—'}. {text('Apenas uma conexão fica ativa. Conectar outra substitui a anterior; mudar a seleção não aplica a troca.', 'Only one connection is active. Connecting another replaces the previous one; changing the selection does not apply the switch.')}</p>
      <div className="grid gap-4 md:grid-cols-2">
        {selectedTransport === 'tcp' ? label(text('IP privado (porta opcional)', 'Private IP (optional port)'), <input className={input} disabled={busy} value={selectedEndpoint} placeholder="192.168.1.50" onChange={e => setTcpEndpoint(e.target.value)} />) : label(text('Dispositivo USB', 'USB device'), <select className={input} disabled={busy} value={selectedDevice} onChange={e => { setDevice(e.target.value) }}><option value="">{text('Selecione o rádio', 'Select a radio')}</option>{status?.app.devices.map(d => <option key={d} value={d}>{d}</option>)}</select>)}
        {label(text('Modo', 'Mode'), <select className={input} aria-describedby="mesh-mode-hint" disabled={busy} value={selectedMode} onChange={e => { setMode(e.target.value) }}>{Object.entries(modeNames).map(([value, title]) => <option key={value} value={value}>{title}{value === 'send' ? text(' (padrão)', ' (default)') : ''}</option>)}</select>)}
      </div>
      <div id="mesh-mode-hint" className="rounded-xl border border-white/10 bg-white/5 p-4 space-y-2 text-sm" aria-live="polite">
        <p className="text-fog/85">{modeHints[selectedMode]}</p>
        <p className="text-fog/65">{text('Relay significa publicar na rede Bitcoin uma transação já assinada por outro contato. Exige também habilitar Permitir relay nesse contato.', 'Relay means publishing a transaction already signed by another contact to the Bitcoin network. You must also enable Allow relay for that contact.')}</p>
        <p className="text-fog/65">{text('Receber bitcoins na carteira funciona em qualquer modo. Pagamentos Lightning exigem aprovação local e usam a rede Lightning normal.', 'Receiving bitcoin in your wallet works in every mode. Lightning payments require local approval and use the regular Lightning network.')}</p>
      </div>
      {selectedTransport === 'tcp' && <p className="text-sm text-amber-200">{text('O rádio precisa estar no Wi-Fi/Ethernet e oferecer a API Meshtastic TCP (porta usual 4403). Use uma rede local confiável: TCP não tem TLS. Não conecte outro cliente ao mesmo rádio. A conexão permanece aberta mesmo ao fechar esta página e reconecta automaticamente. Em dispositivos com MUI, confira a compatibilidade TCP ou utilize USB.', 'The radio must be on Wi-Fi/Ethernet and expose the Meshtastic TCP API (usual port 4403). Use a trusted local network: TCP has no TLS. Do not connect another client to the same radio. The connection stays open after closing this page and reconnects automatically. For devices running MUI, check TCP compatibility or use USB.')}</p>}
      {selectedTransport === 'usb' && status && !status.app.devices.length && <p className="text-sm text-amber-200">{text('Nenhum rádio USB encontrado. Conecte o dispositivo e, em uma VM, habilite a conexão USB no hipervisor.', 'No USB radio found. Connect the device and, for a VM, enable USB passthrough in the hypervisor.')}</p>}
      {status?.radio.name && <p className="text-sm">{text('Nome do rádio', 'Radio name')}: {status.radio.name}{status.radio.short_name ? ` (${status.radio.short_name})` : ''}</p>}
      <div className="flex flex-wrap gap-3 text-sm text-fog/65"><span>{text('ID local', 'Local ID')}: {status?.radio.node ? `!${status.radio.node.toString(16).padStart(8, '0')}` : '—'}</span><span>SNR: {status?.radio.snr ?? '?'} dB · RSSI: {status?.radio.rssi ?? '?'} dBm</span><span>{text('Modo ativo', 'Active mode')}: {status?.mode ? modeNames[status.mode] : '—'}</span><span>{text('Último pacote', 'Last packet')}: {status?.radio.last_receive && !status.radio.last_receive.startsWith('0001') ? new Date(status.radio.last_receive).toLocaleString() : '—'}</span></div>
      <p className="text-xs text-fog/60">{text('Relay publica apenas transações assinadas de contatos pareados com permissão explícita. Não existe relay público.', 'Relay publishes signed transactions only from paired contacts with explicit permission. There is no public relay.')}</p>
      {status?.app.installed && <p className="text-sm text-fog/75">{connected
        ? text('Rádio conectado. O app já está instalado; não é necessário instalar novamente. O próximo passo é parear um contato.', 'Radio connected. The app is already installed; no installation is needed. Next, pair a contact.')
        : text('O app já está instalado. Confira a conexão configurada e use Reconectar rádio para restabelecer a conexão.', 'The app is already installed. Check the configured connection and use Reconnect radio to restore it.')}</p>}
      <p className="text-sm text-fog/75">{text('Selecionar um modo não o aplica automaticamente.', 'Selecting a mode does not apply it automatically.')}</p>
      {modeChanged && <p role="status" className="text-sm text-amber-200">{text('Alteração pendente', 'Pending change')}: {modeNames[status!.mode]} → {modeNames[selectedMode]}. {text('Clique em Aplicar modo e confirme com sua senha no modal.', 'Click Apply mode and confirm your password in the dialog.')}</p>}
      <div className="flex flex-wrap items-center gap-3">
        {status?.app.installed && status.app.status !== 'stopped' && <button className="btn-secondary" disabled={busy} onClick={() => void act({ action: 'disconnect', confirm: true })}>{text('Desconectar rádio', 'Disconnect radio')}</button>}
        {status && (!status.app.installed || !connected || deviceChanged) && <button className="btn-primary disabled:opacity-40 disabled:cursor-not-allowed" disabled={busy || !deviceAvailable} onClick={() => void act({ action: 'install', device: selectedDevice, mode: status.app.installed ? status.mode : selectedMode })}>{!status.app.installed ? text('Instalar LOS Mesh', 'Install LOS Mesh') : deviceChanged ? text('Conectar rádio selecionado', 'Connect selected radio') : text('Reconectar rádio', 'Reconnect radio')}</button>}
        {status?.app.installed && (modeChanged
          ? <button className="btn-primary disabled:opacity-40 disabled:cursor-not-allowed" disabled={busy} onClick={() => void act({ action: 'mode', mode: selectedMode })}>{text('Aplicar modo', 'Apply mode')}</button>
          : <span className="text-sm text-emerald-300">{text('Modo aplicado', 'Applied mode')}: {modeNames[status.mode]}</span>)}
      </div>

    </div>
    </div>
    <div id="mesh-panel-contacts" role="tabpanel" aria-labelledby="mesh-tab-contacts" hidden={section !== 'contacts'}>
    <div className="section-card space-y-4">
      <h3 className="text-lg font-semibold">{text('Contatos e pareamento privado', 'Contacts and private pairing')}</h3>
      <p className="text-sm text-fog/65">{text('Escolha um nó conhecido pelo rádio, verifique a resposta do LOS Mesh e envie um convite. A presença na lista não garante conexão nem identidade.', 'Choose a node known to the radio, check for a LOS Mesh response and send an invitation. Being listed does not guarantee reachability or identity.')}</p>
      {label(text('Buscar nó por nome ou ID', 'Search nodes by name or ID'), <input className={input} value={nodeSearch} onChange={e => { setNodeSearch(e.target.value); setNodePage(0) }} />)}
      <div ref={nodeListRef} role="region" aria-label={text('Nós conhecidos pelo rádio', 'Nodes known to the radio')} tabIndex={0} className="max-h-[28rem] overflow-y-auto overscroll-contain space-y-3 pr-2">{filteredNodes.slice(currentNodePage * nodePageSize, (currentNodePage + 1) * nodePageSize).map(n => {
        const pairedNode = status?.peers.some(p => p.node === n.node && p.paired)
        const available = status?.pairings?.some(p => p.node === n.node && p.state === 'available')
        const pendingNode = status?.pairings?.some(p => p.node === n.node && !['available','verified'].includes(p.state))
        return <div key={n.node} className="rounded-xl border border-white/10 p-3 space-y-2 text-sm"><p className="font-semibold">{n.name || n.short_name || `!${n.node.toString(16)}`}{n.name && n.short_name && n.name !== n.short_name ? ` (${n.short_name})` : ''} <span className="font-mono text-xs text-fog/60">!{n.node.toString(16).padStart(8,'0')}</span></p><p className="text-xs text-fog/60">{text('Visto pelo rádio', 'Last heard by radio')}: {n.last_heard ? new Date(n.last_heard*1000).toLocaleString() : '—'}{n.via_mqtt ? ' · MQTT' : ''}</p>
          <p>{pairedNode ? text('Contato verificado', 'Verified contact') : available ? text('LOS Mesh respondeu ao teste; identidade ainda não verificada.', 'LOS Mesh answered the check; identity not yet verified.') : text('Compatibilidade LOS Mesh ainda não confirmada.', 'LOS Mesh compatibility not yet confirmed.')}</p>
          {!pairedNode && <div className="flex flex-wrap gap-2"><button className="btn-secondary" disabled={busy || !connected || pendingNode} onClick={() => void act({action:'pair_probe',node:n.node})}>{text('Verificar conexão', 'Check connection')}</button><button className="btn-primary" disabled={busy || !connected || !available || pendingNode} onClick={() => void act({action:'pair_invite',node:n.node})}>{text('Adicionar contato', 'Add contact')}</button></div>}
        </div>
      })}</div>
      {!status?.nodes?.length && <p className="text-sm text-fog/60">{text('Nenhum nó foi recuperado do rádio. Conecte o rádio e aguarde a leitura da lista.', 'No nodes have been retrieved from the radio. Connect it and wait for the node list.')}</p>}
      {Boolean(status?.nodes?.length) && !filteredNodes.length && <p className="text-sm text-fog/60">{text('Nenhum nó corresponde à busca.', 'No nodes match your search.')}</p>}
      <div className="flex flex-wrap items-center justify-between gap-3 text-xs text-fog/70">
        <p role="status">{filteredNodes.length ? `${currentNodePage * nodePageSize + 1}–${Math.min((currentNodePage + 1) * nodePageSize, filteredNodes.length)} / ${filteredNodes.length}` : '0'} {text('resultados', 'results')} · {status?.nodes?.length || 0} {text('nós no rádio', 'nodes in radio')}</p>
        <nav aria-label={text('Paginação dos nós', 'Node pagination')} className="flex items-center gap-3">
          <button className="btn-secondary" disabled={currentNodePage === 0} onClick={() => setNodePage(currentNodePage - 1)}>{text('Anterior', 'Previous')}</button>
          <span>{currentNodePage + 1} / {nodePageCount}</span>
          <button className="btn-secondary" disabled={currentNodePage + 1 >= nodePageCount} onClick={() => setNodePage(currentNodePage + 1)}>{text('Próxima', 'Next')}</button>
        </nav>
      </div>
      {(status?.pairings || []).map(p => <div key={p.id} className="rounded-xl border border-emerald-400/30 p-4 space-y-3"><strong>{p.name}</strong><p className="text-sm">{({checking:text('Verificando conexão…', 'Checking connection…'),declined:text('Convite recusado ou cancelado', 'Invitation declined or cancelled'),available:text('Resposta LOS Mesh recebida', 'LOS Mesh response received'),invitation_sent:text('Convite enviado; aguardando aceitação', 'Invitation sent; awaiting acceptance'),invitation_received:text('Convite recebido', 'Invitation received'),exchanging:text('Negociando conexão segura…', 'Negotiating secure connection…'),compare_code:text('Compare o código com o outro operador', 'Compare the code with the other operator'),verified:text('Contato verificado', 'Verified contact'),contact_conflict:text('Já existe um contato com este ID; nenhuma chave foi substituída.', 'A contact with this ID already exists; no key was replaced.')} as Record<string,string>)[p.state] || p.state}</p>
        <p className="text-xs text-fog/60">{text('Expira', 'Expires')}: {new Date(p.expires).toLocaleTimeString()}</p>
        {p.state==='invitation_received' && <button className="btn-primary" disabled={busy} onClick={() => void act({action:'pair_accept',id:p.id})}>{text('Aceitar convite', 'Accept invitation')}</button>}
        {p.code && <><p className="font-mono text-2xl tracking-widest">{p.code}</p><p className="text-sm text-amber-200">{text('Compare pessoalmente ou por outro canal confiável. Não use o próprio chat de rádio para validar este código.', 'Compare in person or over another trusted channel. Do not use this radio chat to verify the code.')}</p>{!p.local_confirmed ? <><label className="flex gap-2 items-center text-sm"><input type="checkbox" checked={Boolean(compareConfirmed[p.id])} onChange={e => setCompareConfirmed(v => ({...v,[p.id]:e.target.checked}))} />{text('Comparei: os códigos são iguais nos dois lados.', 'I compared: the codes match on both sides.')}</label><button className="btn-primary" disabled={busy || !compareConfirmed[p.id]} onClick={() => void act({action:'pair_confirm',id:p.id,code:p.code})}>{text('Confirmar contato', 'Confirm contact')}</button></> : <p>{text('Você confirmou. Aguardando confirmação do outro operador.', 'You confirmed. Waiting for the other operator.')}</p>}</>}
        {p.state!=='verified' && <button className="btn-secondary" disabled={busy} onClick={() => void act({action:'pair_cancel',id:p.id})}>{text('Cancelar / recusar', 'Cancel / decline')}</button>}
      </div>)}
      <details className="space-y-4"><summary className="cursor-pointer text-sm">{text('Avançado: ID e chave manual', 'Advanced: manual ID and key')}</summary>
      <p className="text-sm text-fog/65">{text('Em cada LightningOS, cadastre o ID do rádio do outro lado e a mesma chave de 64 caracteres hexadecimais. Compartilhe a chave por um canal seguro. Salve nos dois lados e repita o pareamento se necessário.', 'On each LightningOS, enter the other radio’s ID and the same 64-character hexadecimal key. Share it through a secure channel. Save on both sides and repeat pairing if necessary.')}</p>
      <div className="grid gap-4 md:grid-cols-2">{label(text('Nome do contato', 'Contact name'), <input className={input} value={name} maxLength={64} onChange={e => setName(e.target.value)} />)}{label(text('ID do rádio remoto (!xxxxxxxx)', 'Remote radio ID (!xxxxxxxx)'), <input className={input} value={nodeID} placeholder="!1234abcd" onChange={e => setNodeID(e.target.value)} />)}</div>
      {label(text('Chave compartilhada', 'Shared key'), <input className={`${input} font-mono`} autoComplete="off" type="password" value={key} maxLength={64} onChange={e => setKey(e.target.value)} />)}
      <div className="flex flex-wrap gap-3"><button className="btn-secondary" onClick={() => setKey(Array.from(crypto.getRandomValues(new Uint8Array(32)), n => n.toString(16).padStart(2, '0')).join(''))}>{text('Gerar chave', 'Generate key')}</button><button className="btn-secondary" disabled={!key} onClick={() => void navigator.clipboard.writeText(key).then(() => setNotice(text('Chave copiada. Compartilhe com segurança.', 'Key copied. Share securely.'))).catch(() => setError(text('Não foi possível copiar.', 'Copy failed.')))}>{text('Copiar chave', 'Copy key')}</button></div>
      <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={allowRelay} onChange={e => setAllowRelay(e.target.checked)} />{text('Autorizar este contato a publicar transações assinadas pelo meu relay', 'Allow this contact to publish signed transactions through my relay')}</label>
      <button className="btn-primary" disabled={busy || !connected || !/^!?[0-9a-fA-F]{8}$/.test(nodeID)} onClick={async () => { const result = await act({ action: 'peer', node: parseInt(nodeID.replace('!', ''), 16), name, key, allow_relay: allowRelay }); if (result) setKey('') }}>{text('Salvar e parear', 'Save and pair')}</button>
      </details>
      <div className="space-y-2">{status?.peers.map(p => <div key={p.node} className="rounded-xl border border-white/10 p-3 text-sm"><div className="flex flex-wrap justify-between gap-2"><strong>{p.name} · !{p.node.toString(16).padStart(8, '0')}</strong><span>{p.paired ? text('Pareado', 'Paired') : text('Aguardando prova da chave', 'Awaiting key proof')}</span></div><p className="my-2 font-mono text-xs">{p.fingerprint} · relay {p.allow_relay ? '✓' : '—'}</p>{p.paired && <button className="btn-secondary mr-2" disabled={busy} onClick={() => void act({action:'peer_permissions',node:p.node,allow_relay:!p.allow_relay})}>{p.allow_relay ? text('Revogar relay', 'Revoke relay') : text('Permitir relay', 'Allow relay')}</button>}<button className="btn-secondary" disabled={busy} onClick={() => void act({ action: 'remove_peer', node: p.node })}>{text('Remover contato', 'Remove contact')}</button></div>)}</div>
    </div>
    </div>
    <div id="mesh-panel-payments" role="tabpanel" aria-labelledby="mesh-tab-payments" hidden={section !== 'payments'} className="space-y-6">
    {status?.mode === 'relay' && <div className="section-card space-y-3"><p className="text-sm text-amber-200">{text('Seu modo ativo é Oferecer relay. Para iniciar envios, selecione Enviar sem oferecer relay ou Enviar e oferecer relay na aba Rádio e aplique a alteração.', 'Your active mode is Offer relay. To initiate transfers, select Send without offering relay or Send and offer relay in the Radio tab and apply the change.')}</p><button className="btn-secondary" onClick={() => setSection('radio')}>{text('Configurar modo', 'Configure mode')}</button></div>}
    {status && !paired.length && <div className="section-card space-y-3"><p className="text-sm text-fog/75">{text('Para enviar pagamentos e solicitações pelo rádio, primeiro pareie um contato em outro ponto LOS Mesh.', 'To send payments and requests over radio, first pair a contact at another LOS Mesh endpoint.')}</p><button className="btn-primary" onClick={() => setSection('contacts')}>{text('Parear contato', 'Pair a contact')}</button></div>}
    <div className="section-card space-y-4">
      <div className="flex flex-wrap gap-3">{['onchain', 'lightning'].map(t => <button key={t} className={tab === t ? 'btn-primary' : 'btn-secondary'} onClick={() => setTab(t)}>{t === 'onchain' ? 'Bitcoin on-chain' : 'Lightning'}</button>)}</div>
      {label(text('Contato de destino', 'Destination contact'), <select className={input} value={peer} onChange={e => setPeer(e.target.value)}><option value="">{text('Selecione um contato pareado', 'Select a paired contact')}</option>{paired.map(p => <option key={p.node} value={p.node}>{p.name}</option>)}</select>)}
      {tab === 'onchain' ? <>
        <p className="text-sm text-fog/65">{text('Importe uma transação já assinada ou crie uma transação com fundos confirmados da carteira LND. A assinatura ocorre após sua aprovação; a publicação é feita pelo relay remoto.', 'Import a signed transaction or create one using confirmed LND wallet funds. Signing follows your approval; the remote relay publishes the transaction.')}</p>
        {label(text('Endereço Bitcoin mainnet', 'Bitcoin mainnet address'), <input className={input} value={address} onChange={e => setAddress(e.target.value)} />)}
        <button className="btn-secondary" disabled={busy} onClick={async () => { setBusy(true); try { const result = await getWalletAddress(); setAddress(result?.address || ''); setRaw('') } catch (e) { setError(String(e)) } finally { setBusy(false) } }}>{text('Gerar meu endereço de recebimento', 'Generate my receiving address')}</button>
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
          <div className="flex gap-3"><button className="btn-primary" disabled={busy || Date.now() >= Date.parse(preview.expires)} onClick={async () => { const result = await act({ action: 'send', id: preview.id }); if (result) { setPreview(null); setRaw('') } }}>{text('Aprovar e transmitir', 'Approve and transmit')}</button><button className="btn-secondary" disabled={busy} onClick={async () => { await act({ action: 'cancel', id: preview.id }); setPreview(null) }}>{text('Cancelar prévia', 'Cancel preview')}</button></div>
        </div>}
      </> : <>
        <p className="text-sm text-fog/65">{text('Crie uma invoice para receber ou envie uma BOLT11 existente. O destinatário decide localmente se deseja pagar.', 'Create an invoice to receive funds or send an existing BOLT11. The recipient decides locally whether to pay.')}</p>
        {label(text('Valor (sats)', 'Amount (sats)'), <input className={input} type="number" min="1" value={amount} onChange={e => setAmount(e.target.value)} />)}
        {label(text('Descrição', 'Description'), <input className={input} value={memo} maxLength={120} onChange={e => setMemo(e.target.value)} />)}
        {label(text('BOLT11 existente (opcional)', 'Existing BOLT11 (optional)'), <textarea className={`${input} font-mono text-xs`} value={invoice} maxLength={4096} onChange={e => setInvoice(e.target.value)} />)}
        <button className="btn-secondary" disabled={busy || !canSend || !amount} onClick={() => void act({ action: 'request_invoice', node: Number(peer), amount_sat: Number(amount), memo })}>{text('Solicitar invoice ao contato', 'Request invoice from contact')}</button>
        <button className="btn-primary" disabled={busy || !canSend} onClick={() => void act({ action: 'invoice', node: Number(peer), invoice, amount_sat: Number(amount), memo })}>{text('Enviar invoice', 'Send invoice')}</button>
      </>}
    </div>
    <div className="section-card space-y-4"><h3 className="text-lg font-semibold">{text('Solicitações recebidas', 'Received requests')}</h3>
      {!status?.pending.length && <p className="text-sm text-fog/60">{text('Nenhuma solicitação aguardando aprovação.', 'No requests awaiting approval.')}</p>}
      {status?.pending.map(p => <div key={p.id} className="space-y-3 rounded-xl border border-white/10 p-4"><p><strong>{p.kind === 'onchain_request' ? 'Bitcoin' : 'Lightning'} · {p.amount_sat} sats</strong> · {status.peers.find(peer => peer.node === p.peer)?.name}</p><p className="break-all text-sm">{p.memo}</p><p className="break-all font-mono text-xs">{p.address || p.destination}<br />{p.payment_hash}</p><p className="text-xs">{text('Expira', 'Expires')}: {new Date(p.expires).toLocaleString()}</p>
        {p.kind === 'invoice_request' ? <button className="btn-primary" disabled={busy} onClick={() => { setPeer(String(p.peer)); setAmount(String(p.amount_sat)); setMemo(p.memo || ''); setInvoice(''); setTab('lightning'); setNotice(text('Pedido de invoice carregado. Confira os dados e clique em Enviar invoice.', 'Invoice request loaded. Review the details and click Send invoice.')) }}>{text('Preparar invoice solicitada', 'Prepare requested invoice')}</button> : p.kind === 'invoice' ? <>{label(text('Taxa máxima Lightning (sats)', 'Maximum Lightning fee (sats)'), <input className={input} type="number" min="0" max="100000" value={maxFee} onChange={e => setMaxFee(e.target.value)} />)}<button className="btn-primary" disabled={busy} onClick={() => void act({ action: 'pay', id: p.id, amount_sat: p.amount_sat, max_fee_sat: Number(maxFee) })}>{text('Aprovar e pagar via Lightning', 'Approve and pay via Lightning')}</button></> : <button className="btn-primary" disabled={busy || Boolean(preview)} onClick={() => { setPeer(String(p.peer)); setAddress(p.address || ''); setAmount(String(p.amount_sat)); setRaw(''); setTab('onchain'); setNotice(text('Solicitação carregada. Revise a transação antes de aprovar.', 'Request loaded. Review the transaction before approving.')) }}>{text('Preparar prévia on-chain', 'Prepare on-chain preview')}</button>}
        <button className="btn-secondary ml-3" disabled={busy} onClick={() => void act({ action: 'cancel', id: p.id })}>{text('Rejeitar', 'Reject')}</button>
      </div>)}
    </div>
    <div className="section-card space-y-4"><h3 className="text-lg font-semibold">{text('Histórico de sessões', 'Session history')}</h3><p className="text-xs text-fog/60">{text('Até 100 sessões recentes; retenção de 30 dias. Sem transações brutas, invoices completas ou preimages no histórico.', 'Up to 100 recent sessions; 30-day retention. No raw transactions, full invoices or preimages in history.')}</p>
      {!status?.history.length && <p className="text-sm text-fog/60">{text('Nenhuma sessão registrada.', 'No sessions recorded.')}</p>}
      {status?.history.map(h => <div key={h.id} className="space-y-2 rounded-xl border border-white/10 p-3 text-sm"><p className="font-semibold">{h.direction === 'in' ? '↓' : '↑'} {states[h.state] || h.state}</p><p className="text-xs text-fog/65">{new Date(h.created).toLocaleString()} · {h.received}/{h.total} {text('pacotes', 'packets')} · !{h.peer.toString(16).padStart(8, '0')}</p>{h.txid && <a className="block break-all font-mono text-xs text-emerald-300 underline" href={`https://mempool.space/tx/${h.txid}`} target="_blank" rel="noreferrer">{h.txid} ↗</a>}{['sending', 'receiving', 'awaiting_result'].includes(h.state) && <button className="btn-secondary" disabled={busy} onClick={() => void act({ action: 'cancel', id: h.id })}>{text('Cancelar envio', 'Cancel transfer')}</button>}</div>)}
    </div>
    </div>
    <SensitiveActionModal
      open={approval !== null}
      title={approvalTitle}
      description={approvalDescription}
      password={password}
      busy={busy}
      error={approvalError}
      confirmLabel={approvalTitle}
      onPasswordChange={setPassword}
      onConfirm={confirmApproval}
      onClose={closeApproval}
    />
  </section>
}
