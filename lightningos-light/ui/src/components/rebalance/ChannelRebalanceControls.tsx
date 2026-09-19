import { useTranslation } from 'react-i18next'
import { rebalanceEligibility } from './eligibility'
import RebalanceEligibility from './RebalanceEligibility'
import type { RebalanceChannel, RebalanceConfig } from './types'

type Props = {
  channel: RebalanceChannel
  config: RebalanceConfig | null
  bypass: boolean
  onAuto: (enabled: boolean) => void
  onRestart: (enabled: boolean) => void
  onBypass: (enabled: boolean) => void
  onGuaranteed: (enabled: boolean) => void
  onExclude: (enabled: boolean) => void
  onAutoTarget: (enabled: boolean) => void
}

export default function ChannelRebalanceControls({ channel, config, bypass, onAuto, onRestart, onBypass, onGuaranteed, onExclude, onAutoTarget }: Props) {
  const { t } = useTranslation()
  const parked = channel.automation_mode === 'parked'
  const conviction = config ? rebalanceEligibility(channel, config).conviction : false
  const label = 'flex items-start gap-2 text-xs text-fog/75'
  return (
    <div className="mt-3 min-w-0 space-y-2 md:min-w-[16rem]">
      <RebalanceEligibility channel={{ ...channel, auto_bypass_cost_gate: bypass }} config={config} />
      <details className="rounded-xl border border-white/10 p-2">
        <summary className="cursor-pointer rounded text-xs text-fog/80 focus-visible:outline focus-visible:outline-2 focus-visible:outline-sky-300">{t('rebalanceEligibility.controls')}</summary>
        <div className="mt-3 space-y-3">
          <fieldset className="space-y-2" disabled={parked}>
            <legend className="mb-2 text-xs font-semibold text-fog">{t('rebalanceEligibility.targetControls')}</legend>
            <label className={label}><input type="checkbox" checked={channel.auto_enabled} onChange={(event) => onAuto(event.target.checked)} />{t('rebalanceCenter.channels.auto')}</label>
            <label className={label}><input type="checkbox" checked={channel.manual_restart_enabled} onChange={(event) => onRestart(event.target.checked)} />{t('rebalanceEligibility.manualRestart')}</label>
            <p className="text-[11px] text-fog/55">{t('rebalanceEligibility.modeHint')}</p>
            <div className="space-y-2 border-l border-white/15 pl-3">
              <label className={label}><input type="checkbox" checked={bypass} disabled={conviction} onChange={(event) => onBypass(event.target.checked)} />{t('rebalanceCenter.channels.autoBypassCostGate')}</label>
              <p className="text-[11px] text-fog/55">{t(conviction ? 'rebalanceEligibility.convictionHint' : 'rebalanceEligibility.bypassHint')}</p>
            </div>
            <label className={label}><input type="checkbox" checked={Boolean(channel.guaranteed_rebalance_enabled)} onChange={(event) => onGuaranteed(event.target.checked)} />{t('rebalanceCenter.channels.guaranteedRebalance')}</label>
            <p className="text-[11px] text-fog/55">{t('rebalanceCenter.channelsHints.guaranteedRebalance')}</p>
            {config?.auto_target_enabled && <label className={label}><input type="checkbox" checked={channel.auto_target_managed ?? true} onChange={(event) => onAutoTarget(event.target.checked)} />{t('rebalanceCenter.autoTarget.managed')}</label>}
          </fieldset>
          <fieldset className="border-t border-white/10 pt-2" disabled={parked}>
            <legend className="text-xs font-semibold text-fog">{t('rebalanceEligibility.sourceControls')}</legend>
            <label className={label}><input type="checkbox" checked={channel.excluded_as_source} onChange={(event) => onExclude(event.target.checked)} />{t('rebalanceCenter.channels.excludeSource')}</label>
            <p className="mt-1 text-[11px] text-fog/55">{t('rebalanceEligibility.sourceHint')}</p>
          </fieldset>
        </div>
      </details>
    </div>
  )
}
