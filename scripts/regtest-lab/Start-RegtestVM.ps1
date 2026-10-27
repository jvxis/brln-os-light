# Never stop LOS-TEST2 or another VM to make room automatically.
$ErrorActionPreference = 'Stop'
$labVBox = 'C:/Program Files/Oracle/VirtualBox/VBoxManage.exe'
$target = 'los-disposable-regtest'
$running = @(& $labVBox list runningvms)
if ($LASTEXITCODE -ne 0) { throw 'Cannot inspect running VMs' }
if (-not ($running | Where-Object { $_ -match '^"LOS-TEST2" \{bca2cc2f-a90f-4088-b970-71e452851b71\}$' })) {
 throw 'LOS-TEST2 must remain running: it is a mainnet node with real funds. Inspect it separately.'
}
$other = @($running | Where-Object { $_ -match '^"los-disposable-' -and $_ -notmatch '^"los-disposable-regtest" ' })
if ($other.Count -gt 0) { throw "Another disposable VM is running. Do not overcommit memory: $other" }
$info = @(& $labVBox showvminfo $target --machinereadable)
if ($LASTEXITCODE -ne 0) { throw 'Create and verify the dedicated full clone first' }
if ($info -notcontains 'name="los-disposable-regtest"' -or $info -notcontains 'CfgFile="D:\\VirtualBox\\los-disposable-regtest\\los-disposable-regtest.vbox"') { throw 'Unexpected VM identity/path' }
if ($running | Where-Object { $_ -match '^"los-disposable-regtest" ' }) {
 Write-Output 'Regtest is already running; exactly one disposable is active.'
 exit 0
}
$osInfo = Get-CimInstance Win32_OperatingSystem
$memoryLine = $info | Where-Object { $_ -match '^memory=' }
$memoryMB = [int]($memoryLine -replace '^memory=','')
if ($osInfo.FreePhysicalMemory -lt (($memoryMB + 3072) * 1024)) { throw 'Insufficient free RAM for the VM plus a 3 GiB host reserve' }
& $labVBox startvm $target --type headless
if ($LASTEXITCODE -ne 0) { throw 'VM startup failed' }
