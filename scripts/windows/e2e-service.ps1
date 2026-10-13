<#
Real end-to-end check of round 3 on a Windows host that may be modified: installs the INTERNAL stage as a
real Windows service, drives it with the real GUI executable and the real Go Core, then attacks it.

  powershell -File scripts/windows/e2e-service.ps1 -Stage <stage dir> -Report <report.json>

Needs an elevated shell. It installs, kills, tampers and uninstalls AsterLinkService and writes under
%ProgramFiles%\AsterLink and %ProgramData%\AsterLink. It changes no route, DNS or adapter: no tunnel is ever
started (a Core only answers hello / read_snapshot). Run it on CI or on a disposable test machine.

Cases that cannot be produced from one machine / one account are reported NOT_RUN, never PASS:
  S01-remote   needs a second computer to attempt the pipe over SMB
  S01-user     needs a second interactive Windows user
#>
param(
    [Parameter(Mandatory)][string]$Stage,
    [Parameter(Mandatory)][string]$Report
)
$ErrorActionPreference = 'Stop'
$Stage = (Resolve-Path $Stage).Path
$Here = Split-Path -Parent $MyInvocation.MyCommand.Path
$ServiceExe = Join-Path $Stage 'service\AsterLink.Service.exe'
$Installed = Join-Path $env:ProgramFiles 'AsterLink'
$InstalledGui = Join-Path $Installed 'app\AsterLink.exe'
$InstalledService = Join-Path $Installed 'service\AsterLink.Service.exe'
$Work = Join-Path ([IO.Path]::GetTempPath()) ('asterlink-e2e-' + [guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory $Work | Out-Null
$Cases = New-Object System.Collections.Generic.List[object]
$Version = (Get-Content (Join-Path $Stage 'manifest.json') -Raw | ConvertFrom-Json).version

function Case([string]$Id, [bool]$Ok, [string]$Observed, [string]$Status) {
    if (-not $Status) { $Status = if ($Ok) { 'PASS' } else { 'FAIL' } }
    $Cases.Add([ordered]@{ case_id = $Id; status = $Status; observed = $Observed })
    '{0,-8} {1,-34} {2}' -f $Status, $Id, $Observed
}
function Verb([string[]]$VerbArgs) {
    $out = & $ServiceExe @VerbArgs 2>&1 | Out-String
    $code = $LASTEXITCODE
    $json = $null
    try { $json = $out | ConvertFrom-Json } catch { }
    [pscustomobject]@{ Code = $code; Json = $json; Text = $out.Trim() }
}
function Status { (Verb @('status')).Json }
function GuiCheck([string]$Name, [int]$HoldSeconds = 0, [switch]$NoWait) {
    $out = Join-Path $Work "$Name.json"
    $guiArgs = @('--service-check', $out)
    if ($HoldSeconds -gt 0) { $guiArgs += @('--service-hold', "$HoldSeconds") }
    $p = Start-Process -FilePath $InstalledGui -ArgumentList $guiArgs -PassThru -WindowStyle Hidden
    if ($NoWait) { return [pscustomobject]@{ Process = $p; Out = $out } }
    if (-not $p.WaitForExit(90000)) { $p.Kill(); throw "GUI check $Name did not finish" }
    $result = $null
    if (Test-Path $out) { $result = Get-Content $out -Raw | ConvertFrom-Json }
    [pscustomobject]@{ Exit = $p.ExitCode; Result = $result }
}
function Wait-Until([scriptblock]$Condition, [int]$Seconds) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    while ((Get-Date) -lt $deadline) { if (& $Condition) { return $true }; Start-Sleep -Milliseconds 400 }
    return [bool](& $Condition)
}
function CoreCount { @(Get-Process -Name asterlink-core -ErrorAction SilentlyContinue).Count }
function ServiceProcess { Get-CimInstance Win32_Service -Filter "Name='AsterLinkService'" }
function Probe([string]$Case) {
    $raw = (& powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $Here 'pipe-probe.ps1') -Case $Case 2>&1 | Out-String).Trim()
    try { $o = $raw | ConvertFrom-Json; $o | Add-Member -NotePropertyName raw -NotePropertyValue $raw -Force; $o }
    catch { [pscustomobject]@{ answered = $null; raw = $raw } }
}

try {
    # -- clean slate ------------------------------------------------------------------------------
    $pre = Status
    Case 'E00-clean-start' (-not $pre.installed) ("installed=$($pre.installed) before the test; the e2e needs a machine without the product") $null
    if ($pre.installed) { throw 'AsterLinkService is already installed on this machine; refusing to continue' }

    # -- install a tampered stage: must be refused and leave nothing ------------------------------
    $stageCore = Join-Path $Stage 'core\asterlink-core.exe'
    Copy-Item $stageCore "$stageCore.bak"
    [IO.File]::AppendAllText($stageCore, 'x')
    $bad = Verb @('install', '--stage', $Stage)
    Move-Item "$stageCore.bak" $stageCore -Force
    $after = Status
    Case 'S02-install-refuses-tampered-stage' (($bad.Code -eq 6) -and (-not $after.installed) -and (-not (Test-Path $Installed))) `
        "exit=$($bad.Code) installed=$($after.installed) root_exists=$(Test-Path $Installed)" $null

    # -- real install --------------------------------------------------------------------------------
    $install = Verb @('install', '--stage', $Stage)
    Case 'R01-install' (($install.Code -eq 0) -and $install.Json.ok) "exit=$($install.Code) $($install.Text.Substring(0, [Math]::Min(200, $install.Text.Length)))" $null
    if ($install.Code -ne 0) { throw 'install failed; the remaining cases cannot run' }

    $st = Status
    Case 'R01-service-registration' ($st.installed -and $st.service_state -eq 'Running' -and $st.binary_path_ok -and $st.runs_as_localsystem -and $st.auto_start -and $st.integrity_ok) `
        "state=$($st.service_state) path_ok=$($st.binary_path_ok) localsystem=$($st.runs_as_localsystem) auto=$($st.auto_start) integrity=$($st.integrity_ok) problems=$($st.problems -join '|')" $null
    Case 'T09-boot-does-not-connect' ($st.core_processes -eq 0) "asterlink-core processes right after service start: $($st.core_processes)" $null

    # -- the real chain: GUI -> Service -> Core ------------------------------------------------------
    $good = GuiCheck 'good'
    $r = $good.Result
    Case 'S-chain-hello-snapshot' (($good.Exit -eq 0) -and $r.connected -and $r.service_version -eq $Version -and $r.core_version -eq $Version -and $r.snapshot_state -ne 'connected') `
        "exit=$($good.Exit) connected=$($r.connected) service=$($r.service_version) core=$($r.core_version) channel=$($r.channel) snapshot=$($r.snapshot_state) err=$($r.error_code) msg=$($r.error_message)" $null
    Case 'S-core-follows-gui' (Wait-Until { (CoreCount) -eq 0 } 30) "asterlink-core processes after the GUI closed: $(CoreCount)" $null

    # -- fakes ------------------------------------------------------------------------------------------
    $fake = Probe 'hello'
    Case 'S01-fake-gui-refused' ($fake.answered -and -not $fake.ok -and $fake.code -eq 'peer_rejected') "answered=$($fake.answered) code=$($fake.code) raw=$($fake.raw)" $null
    Case 'S01-fake-gui-starts-no-core' ((CoreCount) -eq 0) "asterlink-core processes after a fake GUI: $(CoreCount)" $null
    $gb = Probe 'garbage'
    Case 'S03-garbage-frame' ($gb.answered -and $gb.code -eq 'invalid_request') "answered=$($gb.answered) code=$($gb.code) raw=$($gb.raw)" $null
    $unk = Probe 'unknown'
    Case 'S03-unknown-command' ($unk.answered -and $unk.code -eq 'unknown_command') "answered=$($unk.answered) code=$($unk.code) raw=$($unk.raw)" $null
    $big = Probe 'oversize'
    Case 'S03-oversize-frame' ((-not $big.answered) -and $big.closed) "answered=$($big.answered) closed=$($big.closed)" $null
    Case 'S01-remote' $false 'needs a second computer attempting \\host\pipe\AsterLink.Service.v1; the pipe DACL denies NETWORK (unit-tested)' 'NOT_RUN'
    Case 'S01-other-user' $false 'needs a second interactive Windows user; the one-owner gate is unit-tested' 'NOT_RUN'

    # -- a GUI that disappears --------------------------------------------------------------------------
    $held = GuiCheck 'held' -HoldSeconds 120 -NoWait
    $up = Wait-Until { (CoreCount) -eq 1 } 60
    Case 'S-core-runs-while-gui-connected' $up "asterlink-core processes while a verified GUI holds the pipe: $(CoreCount)" $null
    $marker = (Status).recovery_record_present
    Case 'T09-record-written-before-run' $marker "recovery record present while the Core runs: $marker" $null
    try { $held.Process.Kill() } catch { }
    Case 'T09-gui-killed-core-stops' (Wait-Until { (CoreCount) -eq 0 } 40) "asterlink-core processes after the GUI was killed: $(CoreCount)" $null
    Case 'T09-gui-killed-record-cleared' (Wait-Until { -not (Status).recovery_record_present } 20) "recovery record present: $((Status).recovery_record_present)" $null

    # -- the service itself is killed while a Core runs -------------------------------------------------
    $held2 = GuiCheck 'held2' -HoldSeconds 120 -NoWait
    [void](Wait-Until { (CoreCount) -eq 1 } 60)
    $svcPid = (ServiceProcess).ProcessId
    Stop-Process -Id $svcPid -Force
    Case 'T09-service-killed-no-orphan-core' (Wait-Until { (CoreCount) -eq 0 } 15) "asterlink-core processes after the service was killed: $(CoreCount)" $null
    try { $held2.Process.Kill() } catch { }
    $restarted = Wait-Until { (ServiceProcess).State -eq 'Running' -and (ServiceProcess).ProcessId -ne $svcPid } 90
    Case 'T09-service-restarts-by-itself' $restarted "service state=$((ServiceProcess).State) pid $svcPid -> $((ServiceProcess).ProcessId)" $null
    $st2 = Status
    Case 'T09-restart-only-recovers' (($st2.core_processes -eq 0) -and (-not $st2.recovery_record_present)) "core=$($st2.core_processes) record_present=$($st2.recovery_record_present)" $null
    $again = GuiCheck 'again'
    Case 'T09-restart-gives-no-old-session' ($again.Result.connected -and $again.Result.snapshot_state -notin @('connected', 'ready', 'connecting')) `
        "snapshot after the restart=$($again.Result.snapshot_state) (a new Core never inherits the old login)" $null

    # -- a replaced Core: must not run ----------------------------------------------------------------------
    Stop-Service AsterLinkService
    $installedCore = Join-Path $Installed 'core\asterlink-core.exe'
    Copy-Item $installedCore "$Work\core.good"
    [IO.File]::WriteAllBytes($installedCore, [byte[]](1..64))
    Start-Service AsterLinkService
    $tampered = GuiCheck 'tampered'
    Case 'S02-replaced-core-refused' ((-not $tampered.Result.connected) -and $tampered.Result.error_code -eq 'integrity_failed' -and (CoreCount) -eq 0) `
        "connected=$($tampered.Result.connected) code=$($tampered.Result.error_code) cores=$(CoreCount)" $null
    $stTampered = Status
    Case 'S02-status-reports-tamper' ((-not $stTampered.integrity_ok) -and ($stTampered.problems.Count -gt 0)) "integrity_ok=$($stTampered.integrity_ok) problems=$($stTampered.problems -join '|')" $null
    Stop-Service AsterLinkService
    Copy-Item "$Work\core.good" $installedCore -Force

    # -- a planted DLL next to the Core ----------------------------------------------------------------------
    [IO.File]::WriteAllBytes((Join-Path $Installed 'core\version.dll'), [byte[]](1..16))
    Start-Service AsterLinkService
    $planted = GuiCheck 'planted'
    Case 'S02-planted-dll-refused' ((-not $planted.Result.connected) -and $planted.Result.error_code -eq 'integrity_failed') "connected=$($planted.Result.connected) code=$($planted.Result.error_code)" $null
    Stop-Service AsterLinkService
    Remove-Item (Join-Path $Installed 'core\version.dll') -Force

    # -- a user-writable install folder: repair can fix the ACL, not the content ----------------------------
    & icacls.exe (Join-Path $Installed 'core') /grant '*S-1-5-32-545:(OI)(CI)M' | Out-Null
    $before = Status
    Case 'S02-writable-core-folder-detected' (-not $before.integrity_ok) "integrity_ok=$($before.integrity_ok) problems=$($before.problems -join '|')" $null
    $repair = & $InstalledService repair 2>&1 | Out-String
    $afterRepair = Status
    Case 'R01-repair-restores-protection' (($LASTEXITCODE -eq 0) -and $afterRepair.integrity_ok -and $afterRepair.service_state -eq 'Running') `
        "exit=$LASTEXITCODE integrity_ok=$($afterRepair.integrity_ok) state=$($afterRepair.service_state)" $null
    $final = GuiCheck 'final'
    Case 'S-chain-after-repair' ($final.Result.connected) "connected=$($final.Result.connected) err=$($final.Result.error_code)" $null

    # -- a corrupt recovery record blocks until repair archives it ---------------------------------------------
    Stop-Service AsterLinkService
    $markerPath = Join-Path $env:ProgramData 'AsterLink\session.json'
    [IO.File]::WriteAllText($markerPath, '{ not json')
    Start-Service AsterLinkService
    $blocked = GuiCheck 'blocked'
    Case 'T09-corrupt-record-blocks' ((-not $blocked.Result.connected) -and $blocked.Result.error_code -eq 'recovery_blocked' -and (Test-Path $markerPath)) `
        "connected=$($blocked.Result.connected) code=$($blocked.Result.error_code) evidence_kept=$(Test-Path $markerPath)" $null
    $repair2 = & $InstalledService repair 2>&1 | Out-String
    $unblocked = GuiCheck 'unblocked'
    Case 'T09-repair-archives-record' ($unblocked.Result.connected -and (@(Get-ChildItem (Join-Path $env:ProgramData 'AsterLink') -Filter 'session.corrupt-*.json').Count -eq 1)) `
        "connected=$($unblocked.Result.connected) archived=$(@(Get-ChildItem (Join-Path $env:ProgramData 'AsterLink') -Filter 'session.corrupt-*.json').Count)" $null

    # -- uninstall ---------------------------------------------------------------------------------------------------
    $un = Verb @('uninstall')
    $gone = Status
    Case 'R02-uninstall' (($un.Code -eq 0) -and (-not $gone.installed) -and (-not (Test-Path $Installed)) -and (-not (Test-Path (Join-Path $env:ProgramData 'AsterLink')))) `
        "exit=$($un.Code) installed=$($gone.installed) root_exists=$(Test-Path $Installed) programdata_exists=$(Test-Path (Join-Path $env:ProgramData 'AsterLink'))" $null
    Case 'R02-uninstall-leaves-no-core' ((CoreCount) -eq 0) "asterlink-core processes: $(CoreCount)" $null
}
catch {
    Case 'E99-harness' $false ("harness stopped: " + $_.Exception.Message + ' at ' + $_.InvocationInfo.ScriptLineNumber) $null
}
finally {
    # Whatever happened, leave the machine without the service.
    try {
        if ((Status).installed) {
            if (Test-Path (Join-Path $Installed 'core\version.dll')) { Remove-Item (Join-Path $Installed 'core\version.dll') -Force }
            [void](Verb @('uninstall'))
        }
    } catch { }
    $Cases | ConvertTo-Json -Depth 4 | Set-Content -Path $Report -Encoding UTF8
    Remove-Item $Work -Recurse -Force -ErrorAction SilentlyContinue
}
$failed = @($Cases | Where-Object { $_.status -eq 'FAIL' })
"{0} cases, {1} FAIL, {2} NOT_RUN" -f $Cases.Count, $failed.Count, @($Cases | Where-Object { $_.status -eq 'NOT_RUN' }).Count
if ($failed.Count -gt 0) { exit 1 }
exit 0
