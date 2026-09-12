import type { MeshRadioNode } from '../api'

export default function MeshDiagnostics({ node, pt }: { node?: MeshRadioNode; pt: boolean }) {
  const text = (br: string, en: string) => pt ? br : en
  const missing = text('Não informado', 'Not reported')
  const metrics = node?.metrics
  const age = metrics?.time ? Date.now() / 1000 - metrics.time : null
  return <details className="text-xs text-fog/70">
    <summary className="cursor-pointer py-2">{text('Diagnóstico do nó', 'Node diagnostics')}</summary>
    <dl className="grid grid-cols-2 gap-2 py-2">
      <dt>SNR</dt><dd>{node?.snr == null ? missing : `${node.snr.toFixed(1)} dB`}</dd>
      <dt>{text('Saltos', 'Hops')}</dt><dd>{node?.hops_away ?? missing}</dd>
      <dt>{text('Bateria', 'Battery')}</dt><dd>{metrics?.battery == null ? missing : metrics.battery > 100 ? text('Alimentação externa', 'External power') : `${metrics.battery}%`}</dd>
      <dt>{text('Utilização do canal', 'Channel utilization')}</dt><dd>{metrics?.channel_utilization == null ? missing : `${metrics.channel_utilization.toFixed(1)}%`}</dd>
      <dt>{text('Tempo de transmissão (última hora)', 'Transmit airtime (last hour)')}</dt><dd>{metrics?.air_util_tx == null ? missing : `${metrics.air_util_tx.toFixed(1)}%`}</dd>
    </dl>
    <p>{age == null ? text('Horário da telemetria desconhecido; dados podem vir do cache do rádio.', 'Telemetry time unknown; values may come from the radio cache.') : `${text('Telemetria', 'Telemetry')}: ${new Date(metrics!.time * 1000).toLocaleString()}${age > 3600 ? text(' — dados antigos (mais de 1 hora)', ' — old data (over 1 hour)') : age < -60 ? text(' — relógio do rádio adiantado', ' — radio clock ahead') : ''}`}</p>
    <p>{text('Métricas informadas pelo rádio; não comprovam disponibilidade, identidade ou entrega. SNR e saltos se referem ao último registro do nó.', 'Radio-reported metrics do not prove availability, identity or delivery. SNR and hops refer to the last node record.')}</p>
  </details>
}
