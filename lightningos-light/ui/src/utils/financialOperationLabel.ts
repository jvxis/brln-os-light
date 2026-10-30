export function financialOperationLabel(operation: string, feeMode: string | undefined, pt: boolean): string {
  if (operation === 'lnops/channel/close' || operation === 'channel_close') {
    return feeMode === 'existing_commitment'
      ? (pt ? 'Forçar fechamento de canal' : 'Force-close channel')
      : (pt ? 'Fechar canal cooperativamente' : 'Cooperatively close channel')
  }
  const labels: Record<string, string> = pt
    ? { onchain_send: 'Envio on-chain', chat_keysend: 'Mensagem com pagamento', channel_open: 'Abrir canal', channel_open_batch: 'Abrir canais', 'wallet/pay': 'Pagamento Lightning', 'wallet/pay/mpp': 'Pagamento Lightning MPP', 'wallet/pay/validated-route': 'Pagamento por rota validada' }
    : { onchain_send: 'On-chain transfer', chat_keysend: 'Message with payment', channel_open: 'Open channel', channel_open_batch: 'Open channels', 'wallet/pay': 'Lightning payment', 'wallet/pay/mpp': 'Lightning MPP payment', 'wallet/pay/validated-route': 'Validated route payment' }
  return labels[operation] || operation
}
