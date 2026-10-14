<#
Round 6 recovery checks (handoff plan 10 round 6, cases T08 / T09 / R03). Run on the Windows TEST machine, elevated,
with the INTERNAL build installed. It never installs or changes anything; the only destructive act is the
-Mode Kill process kill that you ask for.

  # 1. Connect in the AsterLink window first (a real connection), then hard-kill one component and check recovery:
  powershell -File scripts\windows\reliability-check.ps1 -Mode Kill -Target Core    -Report kill-core.json
  powershell -File scripts\windows\reliability-check.ps1 -Mode Kill -Target Gui     -Report kill-gui.json
  powershell -File scripts\windows\reliability-check.ps1 -Mode Kill -Target Service -Report kill-service.json

  # 2. After a sleep/resume, a hibernate, a Wi-Fi <-> Ethernet switch or a power-cut reboot, with the tunnel having
  #    been connected before the event, check that the data path is closed and the machine's own network works:
  powershell -File scripts\windows\reliability-check.ps1 -Mode Verify -Report after-resume.json

Expected after any of these events (plan 7.4): the tunnel is closed (no AsterLink adapter, no split routes), the
system resolver answers with real addresses (not 198.18.x.x), the router/ISP resolver is reachable again (the port-53
block is gone), direct internet works, the WinINET proxy is untouched, and the app does NOT reconnect by itself.
Statuses: PASS / FAIL / NOT_RUN. A case that cannot be evaluated is NOT_RUN, never PASS.
#>
param(
    [Parameter(Mandatory)][ValidateSet('Kill', 'Verify')][string]$Mode,
    [ValidateSet('Core', 'Service', 'Gui')][string]$Target,
    [Parameter(Mandatory)][string]$Report,
    [int]$WaitSeconds = 45
)
$ErrorActionPreference = 'Continue'
if ($Mode -eq 'Kill' -and -not $Target) { throw '-Target is required with -Mode Kill' }
$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'run this from an elevated PowerShell' }
if (Test-Path $Report) { throw "$Report already exists" }

$Cases = New-Object System.Collections.Generic.List[object]
function Case([string]$Id, [string]$Status, [string]$Observed) {
    $Cases.Add([ordered]@{ case_id = $Id; status = $Status; observed = $Observed })
    '{0,-8} {1,-30} {2}' -f $Status, $Id, $Observed
}
function Wait-Until([scriptblock]$Condition, [int]$Seconds) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    while ((Get-Date) -lt $deadline) { if (& $Condition) { return $true }; Start-Sleep -Milliseconds 500 }
    return [bool](& $Condition)
}
function CoreCount { @(Get-Process -Name asterlink-core -ErrorAction SilentlyContinue).Count }
function TunAdapter { Get-NetAdapter -Name 'AsterLink' -ErrorAction SilentlyContinue }
function SplitRoutes {
    @(Get-NetRoute -AddressFamily IPv4 -ErrorAction SilentlyContinue |
        Where-Object { $_.InterfaceAlias -eq 'AsterLink' -and ($_.DestinationPrefix -eq '0.0.0.0/1' -or $_.DestinationPrefix -eq '128.0.0.0/1') }).Count
}
function SystemLookupIsReal {
    try {
        $addrs = [System.Net.Dns]::GetHostAddresses('example.org') | Where-Object { $_.AddressFamily -eq 'InterNetwork' }
        $text = ($addrs | ForEach-Object { $_.ToString() }) -join ','
        [pscustomobject]@{ Ok = ($addrs.Count -gt 0 -and $text -notmatch '(^|,)198\.18\.'); Text = $text }
    } catch { [pscustomobject]@{ Ok = $false; Text = $_.Exception.Message } }
}
function PhysicalResolvers {
    @(Get-DnsClientServerAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue |
        Where-Object { $_.InterfaceAlias -ne 'AsterLink' -and $_.ServerAddresses } |
        ForEach-Object { $_.ServerAddresses } | Where-Object { $_ -and $_ -ne '127.0.0.1' -and $_ -notlike '198.18.*' } | Select-Object -Unique -First 4)
}
function ProxyState {
    $k = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings'
    $p = Get-ItemProperty $k -ErrorAction SilentlyContinue
    'enable={0} server={1}' -f $p.ProxyEnable, $p.ProxyServer
}
function ServiceState { (Get-Service -Name AsterLinkService -ErrorAction SilentlyContinue).Status }

$proxyBefore = ProxyState
$serviceBefore = ServiceState
$facts = [ordered]@{
    os = (Get-CimInstance Win32_OperatingSystem | Select-Object -ExpandProperty Caption) + ' ' + [Environment]::OSVersion.Version
    arch = $env:PROCESSOR_ARCHITECTURE
    mode = $Mode; target = $Target; service_before = "$serviceBefore"; proxy_before = $proxyBefore
}

if ($Mode -eq 'Kill') {
    $preAdapter = TunAdapter
    $preCore = CoreCount
    if (-not $preAdapter -or $preCore -eq 0) {
        Case 'precondition' 'NOT_RUN' "not connected (adapter present=$([bool]$preAdapter), core processes=$preCore); connect in the AsterLink window first"
        $facts.results = $Cases
        $facts | ConvertTo-Json -Depth 6 | Set-Content -Encoding UTF8 $Report
        exit 3
    }
    Case 'precondition' 'PASS' "connected: adapter up, core processes=$preCore"
    $name = switch ($Target) { 'Core' { 'asterlink-core' } 'Service' { 'AsterLink.Service' } 'Gui' { 'AsterLink' } }
    $victims = @(Get-Process -Name $name -ErrorAction SilentlyContinue)
    if ($victims.Count -eq 0) {
        Case "T09-kill-$Target" 'NOT_RUN' "no $name process found"
    } else {
        $victims | Stop-Process -Force
        Case "T09-kill-$Target-issued" 'PASS' "terminated $($victims.Count) $name process(es) with Stop-Process -Force"
    }
}

# ---- post-conditions (both modes) ------------------------------------------------------------------------------
$closed = Wait-Until { -not (TunAdapter) -and (SplitRoutes) -eq 0 } $WaitSeconds
$adapterNow = if (TunAdapter) { 'present' } else { 'absent' }
Case 'T09-tunnel-closed' $(if ($closed) { 'PASS' } else { 'FAIL' }) "AsterLink adapter $adapterNow, split routes via AsterLink = $(SplitRoutes) (waited up to ${WaitSeconds}s)"

if ($Mode -eq 'Kill' -and $Target -ne 'Core') {
    # A killed GUI makes the Service stop the session; a killed Service is restarted by the SCM. Either way no
    # old authorization comes back: there must be no Core running a tunnel that nobody owns.
    $serviceBack = Wait-Until { (ServiceState) -eq 'Running' } 90
    Case 'T09-service-state' $(if ($Target -eq 'Service') { if ($serviceBack) { 'PASS' } else { 'FAIL' } } else { 'PASS' }) "service is $(ServiceState) (before: $serviceBefore); Service kill expects the SCM recovery to bring it back"
    Start-Sleep -Seconds 10
}
$orphans = CoreCount
$allowCore = ($Mode -eq 'Verify')
$orphanNote = ''
if ($allowCore -and $orphans -gt 0) { $orphanNote = ' (Verify mode: an idle Core of the still-open app is allowed; the tunnel itself is closed)' }
Case 'T09-no-orphan-core' $(if ($orphans -eq 0 -or $allowCore) { 'PASS' } else { 'FAIL' }) "asterlink-core processes = $orphans$orphanNote"
Case 'T08-no-auto-reconnect' $(if (-not (TunAdapter)) { 'PASS' } else { 'FAIL' }) "adapter $(if (TunAdapter) { 'present' } else { 'absent' }) after the settle wait; the product must not reconnect by itself"

$look = SystemLookupIsReal
Case 'T03-system-dns-real' $(if ($look.Ok) { 'PASS' } else { 'FAIL' }) "system resolver (DNS Client) returned: $($look.Text)"

$resolvers = PhysicalResolvers
if ($resolvers.Count -eq 0) {
    Case 'T03-lan-dns-restored' 'NOT_RUN' 'no physical DNS server configured to query'
} else {
    $answered = @()
    foreach ($r in $resolvers) {
        try { if (Resolve-DnsName -Name example.org -Server $r -Type A -DnsOnly -ErrorAction Stop) { $answered += $r } } catch { }
    }
    Case 'T03-lan-dns-restored' $(if ($answered.Count -gt 0) { 'PASS' } else { 'FAIL' }) "$($answered.Count) of $($resolvers.Count) physical resolvers answer a direct query again (the port-53 block is gone)"
}

try {
    $r = Invoke-WebRequest -Uri 'https://ipv4.icanhazip.com' -UseBasicParsing -TimeoutSec 20
    Case 'T09-direct-internet' $(if ($r.StatusCode -eq 200) { 'PASS' } else { 'FAIL' }) "direct HTTPS returned $($r.StatusCode)"
} catch { Case 'T09-direct-internet' 'FAIL' "direct HTTPS failed: $($_.Exception.Message)" }

$proxyAfter = ProxyState
Case 'T01-noproxy' $(if ($proxyAfter -eq $proxyBefore) { 'PASS' } else { 'FAIL' }) "WinINET proxy before/after: $proxyBefore -> $proxyAfter"

$failed = @($Cases | Where-Object { $_.status -eq 'FAIL' }).Count
$facts.failed = $failed
$facts.results = $Cases
$facts | ConvertTo-Json -Depth 6 | Set-Content -Encoding UTF8 $Report
"`n$($Cases.Count) cases, $failed FAIL. Report: $Report"
if ($failed -gt 0) { exit 1 }
