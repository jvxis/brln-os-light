param(
  [string]$BaseUrl = $env:BRLN_API_URL,
  [string]$Password = $env:BRLN_API_PASSWORD,
  [string]$OutDir = "",
  [int]$HistoryLimit = 288,
  [int]$BaselineDays = 14
)

$ErrorActionPreference = "Stop"

if ([string]::IsNullOrWhiteSpace($BaseUrl)) {
  throw "BaseUrl is required. Pass -BaseUrl or set BRLN_API_URL."
}
if ([string]::IsNullOrWhiteSpace($Password)) {
  throw "Password is required. Pass -Password or set BRLN_API_PASSWORD."
}
if ([string]::IsNullOrWhiteSpace($OutDir)) {
  $stamp = Get-Date -Format "yyyyMMdd-HHmmss"
  $OutDir = Join-Path $PWD "sovereign-audit-$stamp"
}

$BaseUrl = $BaseUrl.TrimEnd("/")
New-Item -ItemType Directory -Path $OutDir -Force | Out-Null

# The appliance commonly uses a local/self-signed certificate.
[System.Net.ServicePointManager]::ServerCertificateValidationCallback = { $true }

function Invoke-BrlnGet {
  param(
    [Parameter(Mandatory = $true)][string]$Path,
    [Parameter(Mandatory = $true)]$Session
  )
  Invoke-RestMethod -Uri ($BaseUrl + $Path) -Method Get -WebSession $Session
}

function Save-Json {
  param(
    [Parameter(Mandatory = $true)]$Data,
    [Parameter(Mandatory = $true)][string]$Name
  )
  $Data | ConvertTo-Json -Depth 100 | Set-Content -Path (Join-Path $OutDir $Name) -Encoding UTF8
}

function Sum-Field {
  param($Rows, [string]$Field)
  $value = ($Rows | Measure-Object $Field -Sum).Sum
  if ($null -eq $value) { return 0 }
  return [int64]$value
}

function Count-Where {
  param($Rows, [scriptblock]$Predicate)
  return @($Rows | Where-Object $Predicate).Count
}

function Percent {
  param([double]$Numerator, [double]$Denominator)
  if ($Denominator -le 0) { return "0.00%" }
  return ("{0:N2}%" -f (($Numerator / $Denominator) * 100))
}

function Sats {
  param($Value)
  if ($null -eq $Value) { return "0" }
  return ("{0:N0}" -f [double]$Value)
}

function New-Table {
  param(
    [array]$Rows,
    [string[]]$Columns
  )
  if ($Rows.Count -eq 0) {
    return "_No rows._`n"
  }
  $lines = @()
  $lines += "| " + ($Columns -join " | ") + " |"
  $lines += "| " + (($Columns | ForEach-Object { "---" }) -join " | ") + " |"
  foreach ($row in $Rows) {
    $values = foreach ($column in $Columns) {
      $text = [string]$row.$column
      $text = $text -replace "\|", "/"
      $text
    }
    $lines += "| " + ($values -join " | ") + " |"
  }
  return ($lines -join "`n") + "`n"
}

$session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$headers = @{ Origin = $BaseUrl; Referer = "$BaseUrl/" }
$loginBody = @{ password = $Password } | ConvertTo-Json -Compress
$login = Invoke-RestMethod -Uri "$BaseUrl/api/auth/login" -Method Post -WebSession $session -Headers $headers -ContentType "application/json" -Body $loginBody
if (-not $login.authenticated) {
  throw "Authentication failed."
}

$config = Invoke-BrlnGet "/api/rebalance/config" $session
$overview = Invoke-BrlnGet "/api/rebalance/overview" $session
$sovereignHistoryRoot = Invoke-BrlnGet "/api/rebalance/sovereign-history?limit=$HistoryLimit&include_decisions=1" $session
$channelsRoot = Invoke-BrlnGet "/api/rebalance/channels" $session
$historyRoot = Invoke-BrlnGet "/api/rebalance/history" $session
$baseline = Invoke-BrlnGet "/api/rebalance/metrics/baseline?days=$BaselineDays" $session
$rankingRoot = Invoke-BrlnGet "/api/lnops/channel-ranking?limit=200" $session

Save-Json $config "config.json"
Save-Json $overview "overview.json"
Save-Json $sovereignHistoryRoot "sovereign-history.json"
Save-Json $channelsRoot "channels.json"
Save-Json $historyRoot "history-full.json"
Save-Json $baseline "baseline.json"
Save-Json $rankingRoot "channel-ranking.json"

$history = @($sovereignHistoryRoot.history)
$channels = @($channelsRoot.channels)
$jobs = @($historyRoot.jobs)
$attempts = @($historyRoot.attempts)
$ranking = @($rankingRoot.items)

$decisions = @()
foreach ($scan in $history) {
  foreach ($decision in @($scan.decisions)) {
    if ($null -eq $decision) { continue }
    $row = $decision | Select-Object *
    $row | Add-Member -NotePropertyName scan_at -NotePropertyValue $scan.scan_at
    $row | Add-Member -NotePropertyName mode -NotePropertyValue $scan.mode
    $decisions += $row
  }
}
$selectedDecisions = @($decisions | Where-Object { $_.selected })
$selectedExploration = @($selectedDecisions | Where-Object { $_.exploration_slot })

$skipReasons = @{}
foreach ($scan in $history) {
  if ($scan.skip_reasons) {
    foreach ($prop in $scan.skip_reasons.PSObject.Properties) {
      if (-not $skipReasons.ContainsKey($prop.Name)) {
        $skipReasons[$prop.Name] = 0
      }
      $skipReasons[$prop.Name] += [int]$prop.Value
    }
  }
}
$skipRows = $skipReasons.GetEnumerator() |
  Sort-Object Value -Descending |
  Select-Object -First 20 |
  ForEach-Object { [pscustomobject]@{ reason = $_.Name; count = $_.Value } }

$modeRows = $history |
  Group-Object mode |
  ForEach-Object {
    [pscustomobject]@{
      mode = $_.Name
      rounds = $_.Count
      candidates = Sum-Field $_.Group "candidates"
      selected = Sum-Field $_.Group "selected"
      expected_profit_sat = Sum-Field $_.Group "expected_profit_sat"
      zero_selected = Count-Where $_.Group { $_.selected -eq 0 }
    }
  } |
  Sort-Object mode

$selectedRows = $decisions |
  Where-Object { $_.selected } |
  Group-Object channel_id, peer_alias |
  Sort-Object Count -Descending |
  Select-Object -First 20 |
  ForEach-Object { [pscustomobject]@{ target = $_.Name; selected = $_.Count } }

$skippedTargetRows = $decisions |
  Where-Object { -not $_.selected } |
  Group-Object channel_id, peer_alias, reason |
  Sort-Object Count -Descending |
  Select-Object -First 20 |
  ForEach-Object { [pscustomobject]@{ target_reason = $_.Name; count = $_.Count } }

$sovereignJobs = @($jobs | Where-Object { $_.trigger_reason -eq "sovereign-autopilot" })
$sovereignJobIDs = @{}
foreach ($job in $sovereignJobs) {
  $sovereignJobIDs[[int64]$job.id] = $job
}
$sovereignAttempts = @($attempts | Where-Object { $sovereignJobIDs.ContainsKey([int64]$_.job_id) })
$sovereignSucceededAttempts = @($sovereignAttempts | Where-Object { $_.status -eq "succeeded" })

$targetExecutionRows = $sovereignJobs |
  ForEach-Object {
    $job = $_
    $jobAttempts = @($sovereignAttempts | Where-Object { $_.job_id -eq $job.id })
    $successAttempts = @($jobAttempts | Where-Object { $_.status -eq "succeeded" })
    [pscustomobject]@{
      target = "$($job.target_peer_alias), $($job.target_channel_id)"
      status = $job.status
      attempts = $jobAttempts.Count
      succeeded_attempts = $successAttempts.Count
      sent_sat = Sum-Field $successAttempts "amount_sat"
      fee_sat = Sum-Field $successAttempts "fee_paid_sat"
      expected_profit_sat = $job.sovereign_expected_profit_sat
    }
  } |
  Group-Object target |
  ForEach-Object {
    [pscustomobject]@{
      target = $_.Name
      jobs = $_.Count
      succeeded_jobs = Count-Where $_.Group { $_.status -eq "succeeded" }
      partial_jobs = Count-Where $_.Group { $_.status -eq "partial" }
      failed_jobs = Count-Where $_.Group { $_.status -eq "failed" }
      attempts = Sum-Field $_.Group "attempts"
      succeeded_attempts = Sum-Field $_.Group "succeeded_attempts"
      sent_sat = Sum-Field $_.Group "sent_sat"
      fee_sat = Sum-Field $_.Group "fee_sat"
      expected_profit_sat = Sum-Field $_.Group "expected_profit_sat"
    }
  } |
  Sort-Object jobs -Descending |
  Select-Object -First 20

$sourceAttemptRows = $sovereignAttempts |
  Group-Object source_channel_id, source_peer_alias |
  ForEach-Object {
    $successAttempts = @($_.Group | Where-Object { $_.status -eq "succeeded" })
    [pscustomobject]@{
      source = $_.Name
      attempts = $_.Count
      succeeded = $successAttempts.Count
      sent_sat = Sum-Field $successAttempts "amount_sat"
      fee_sat = Sum-Field $successAttempts "fee_paid_sat"
      no_route = Count-Where $_.Group { $_.fail_reason -like "*path to destination*" -or $_.fail_reason -like "*NO_ROUTE*" }
      no_amount = Count-Where $_.Group { $_.fail_reason -like "*no amount*" }
    }
  } |
  Sort-Object attempts -Descending |
  Select-Object -First 20

$rankingByID = @{}
foreach ($item in $ranking) {
  $rankingByID[[string]$item.channel_id] = $item
}
$sourceRows = $channels |
  ForEach-Object {
    $rank = $rankingByID[[string]$_.channel_id]
    [pscustomobject]@{
      peer = $_.peer_alias
      local_pct = "{0:N1}" -f [double]$_.local_pct
      out_ppm = $_.outgoing_fee_ppm
      assisted_fee_7d = if ($rank) { $rank.assisted_forward_fee_7d_sat } else { 0 }
      assisted_amt_7d = if ($rank) { $rank.assisted_forward_amt_7d_sat } else { 0 }
      revenue_7d = $_.revenue_7d_sat
      drain_rate = $_.drain_rate_sat_per_hour
      eligible_source = $_.eligible_as_source
      max_source_sat = $_.max_source_sat
      auto = $_.auto_enabled
    }
  } |
  Where-Object { $_.eligible_source -or $_.max_source_sat -gt 0 } |
  Sort-Object assisted_fee_7d -Descending |
  Select-Object -First 20

$report = @()
$report += "# Sovereign Autopilot Audit"
$report += ""
$report += "- Generated at: $(Get-Date -Format o)"
$report += "- Base URL: $BaseUrl"
$report += "- Scheduler mode: $($config.scheduler_mode)"
$report += "- Auto enabled: $($config.auto_enabled)"
$report += "- Sovereign scope: $($config.sovereign_candidate_scope)"
$report += "- Max jobs/cycle: $($config.sovereign_max_jobs_per_cycle)"
$report += "- Exploration slot: $($config.sovereign_exploration_slot_pct)%"
$report += "- Budget unlimited: $($config.budget_unlimited)"
$report += ""
$report += "## Overview"
$report += ""
$report += "- Last sovereign scan: $($overview.sovereign_last_decision_at)"
$report += "- Last sovereign candidates/selected: $($overview.sovereign_candidates) / $($overview.sovereign_selected)"
$report += "- Last scan status: $($overview.last_scan_status), queued: $($overview.last_scan_queued), candidates: $($overview.last_scan_candidates)"
$report += "- Selected exploration decisions in audit window: $($selectedExploration.Count) / $($selectedDecisions.Count) ($(Percent $selectedExploration.Count $selectedDecisions.Count))"
$report += "- 24h attempts/success attempts: $(Sats $overview.attempts_24h) / $(Sats $overview.success_attempts_24h) ($(Percent $overview.success_attempts_24h $overview.attempts_24h))"
$report += "- 7d effectiveness: $(Percent $overview.effectiveness_7d 1), execution effectiveness: $(Percent $overview.effectiveness_execution_7d 1)"
$report += "- Sovereign 7d sent/cost: $(Sats $overview.sovereign_rebalance_amount_7d_sat) sat / $(Sats $overview.sovereign_rebalance_cost_7d_sat) sat"
$report += "- Sovereign 7d forward/fee/net: $(Sats $overview.sovereign_forward_amount_7d_sat) sat / $(Sats $overview.sovereign_forward_fee_7d_sat) sat / $(Sats $overview.sovereign_realized_net_7d_sat) sat"
$report += "- Sovereign 7d sell-through: $(Percent $overview.sovereign_sellthrough_7d 1)"
$report += ""
$report += "## Mode Split"
$report += ""
$report += New-Table $modeRows @("mode", "rounds", "candidates", "selected", "expected_profit_sat", "zero_selected")
$report += ""
$report += "## Skip Reasons"
$report += ""
$report += New-Table $skipRows @("reason", "count")
$report += ""
$report += "## Selected Targets"
$report += ""
$report += New-Table $selectedRows @("target", "selected")
$report += ""
$report += "## Skipped Targets"
$report += ""
$report += New-Table $skippedTargetRows @("target_reason", "count")
$report += ""
$report += "## Sovereign Live Execution"
$report += ""
$report += "- Jobs: $($sovereignJobs.Count)"
$report += "- Job success/partial/failed: $(Count-Where $sovereignJobs { $_.status -eq 'succeeded' }) / $(Count-Where $sovereignJobs { $_.status -eq 'partial' }) / $(Count-Where $sovereignJobs { $_.status -eq 'failed' })"
$report += "- Attempts/succeeded attempts: $($sovereignAttempts.Count) / $($sovereignSucceededAttempts.Count) ($(Percent $sovereignSucceededAttempts.Count $sovereignAttempts.Count))"
$report += "- Sent/cost from succeeded attempts: $(Sats (Sum-Field $sovereignSucceededAttempts 'amount_sat')) sat / $(Sats (Sum-Field $sovereignSucceededAttempts 'fee_paid_sat')) sat"
$report += ""
$report += New-Table $targetExecutionRows @("target", "jobs", "succeeded_jobs", "partial_jobs", "failed_jobs", "attempts", "succeeded_attempts", "sent_sat", "fee_sat", "expected_profit_sat")
$report += ""
$report += "## Sources Used By Sovereign Jobs"
$report += ""
$report += New-Table $sourceAttemptRows @("source", "attempts", "succeeded", "sent_sat", "fee_sat", "no_route", "no_amount")
$report += ""
$report += "## Assisted Source Candidates"
$report += ""
$report += New-Table $sourceRows @("peer", "local_pct", "out_ppm", "assisted_fee_7d", "assisted_amt_7d", "revenue_7d", "drain_rate", "eligible_source", "max_source_sat", "auto")

$reportPath = Join-Path $OutDir "report.md"
$report -join "`n" | Set-Content -Path $reportPath -Encoding UTF8
Write-Output $reportPath
