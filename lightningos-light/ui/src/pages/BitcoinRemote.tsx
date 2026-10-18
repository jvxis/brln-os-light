import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getBitcoin, postBitcoinRemote } from '../api'

export default function BitcoinRemote() {
  const { t } = useTranslation()
  const [status, setStatus] = useState<any>(null)
  const [rpcUser, setRpcUser] = useState('')
  const [rpcPass, setRpcPass] = useState('')
  const [message, setMessage] = useState('')
  const [messageTone, setMessageTone] = useState<'neutral' | 'success' | 'warn' | 'error'>('neutral')

  const syncLabel = (info: any) => {
    if (!info || typeof info.verification_progress !== 'number') {
      return t('common.na')
    }
    return `${(info.verification_progress * 100).toFixed(2)}%`
  }

  useEffect(() => {
    getBitcoin().then(setStatus).catch(() => null)
  }, [])

  const handleSave = async () => {
    setMessage(t('common.saving'))
    setMessageTone('warn')
    try {
      await postBitcoinRemote({ rpcuser: rpcUser, rpcpass: rpcPass })
      setMessage(t('bitcoinRemote.savedOk'))
      setMessageTone('success')
      const updated = await getBitcoin()
      setStatus(updated)
      setRpcPass('')
    } catch (err: any) {
      setMessage(
        err?.code === 'bitcoin_zmq_unreachable'
          ? t('bitcoinRemote.zmqUnreachable', { detail: err.message })
          : err?.message || t('bitcoinRemote.validationFailed')
      )
      setMessageTone('error')
    }
  }

  return (
    <section className="space-y-6">
      <div className="section-card">
        <h2 className="text-2xl font-semibold">{t('bitcoinRemote.title')}</h2>
        <p className="text-fog/60">{t('bitcoinRemote.subtitle')}</p>
      </div>

      <div className="section-card space-y-4">
        <h3 className="text-lg font-semibold">{t('bitcoinRemote.connection')}</h3>
        <div className="grid gap-4 lg:grid-cols-3 text-sm">
          <div>
            <p className="text-fog/60">{t('bitcoinRemote.rpcHost')}</p>
            <p>{status?.rpchost || 'bitcoin.br-ln.com:8085'}</p>
          </div>
          <div>
            <p className="text-fog/60">{t('bitcoinRemote.rpcStatus')}</p>
            <p>{status?.rpc_ok ? t('common.ok') : t('common.fail')}</p>
          </div>
          <div>
            <p className="text-fog/60">{t('bitcoinRemote.zmq')}</p>
            <p>{status?.zmq_rawblock_ok && status?.zmq_rawtx_ok ? t('common.ok') : t('common.check')}</p>
          </div>
        </div>
        {status?.rpc_ok && (
          <div className="grid gap-4 lg:grid-cols-3 text-sm">
            <div>
              <p className="text-fog/60">{t('bitcoinRemote.chain')}</p>
              <p>{status?.chain || t('common.na')}</p>
            </div>
            <div>
              <p className="text-fog/60">{t('bitcoinRemote.blocks')}</p>
              <p>{status?.blocks ?? t('common.na')}</p>
            </div>
            <div>
              <p className="text-fog/60">{t('bitcoinRemote.sync')}</p>
              <p>{syncLabel(status)}</p>
            </div>
          </div>
        )}
      </div>

      <div className="section-card space-y-4">
        <h3 className="text-lg font-semibold">{t('bitcoinRemote.updateCredentials')}</h3>
        <div className="grid gap-4 lg:grid-cols-2">
          <input className="input-field" placeholder={t('bitcoinRemote.rpcUser')} value={rpcUser} onChange={(e) => setRpcUser(e.target.value)} />
          <input className="input-field" placeholder={t('bitcoinRemote.rpcPassword')} type="password" value={rpcPass} onChange={(e) => setRpcPass(e.target.value)} />
        </div>
        <button className="btn-primary" onClick={handleSave}>{t('common.save')}</button>
        {message && (
          <p className={`text-sm ${
            messageTone === 'success'
              ? 'text-glow'
              : messageTone === 'error'
                ? 'text-ember'
                : messageTone === 'warn'
                  ? 'text-brass'
                  : 'text-fog/60'
          }`}>{message}</p>
        )}
      </div>
    </section>
  )
}
