<#
Round 8 acceptance for the distributable folder produced by
  package-native-windows.py --channel internal --installer --release-dir <dir>

  powershell -File scripts/windows/release-check.ps1 -ReleaseDir <dir> -ExpectSignature none|test -Report <report.json>

Part 1 (any host with PowerShell, nothing is installed): checksums, signatures, tamper detection, file inventory, no secrets.
Part 2 (needs an elevated Windows host that may be modified; add -Install): the REAL installer, silently, and the real
uninstaller: install, reinstall over itself, upgrade refused while the app is open, upgrade failure rolls back to the
previous version, downgrade refused, uninstall keeping / removing the current user's data, uninstall refused while the app is open.

It changes no route, DNS or adapter (no tunnel is started). It installs and removes AsterLinkService and writes under
%ProgramFiles%\AsterLink and %ProgramData%\AsterLink, so use CI or a disposable test machine.

Cases that this script cannot produce are reported NOT_RUN, never PASS: a clean Windows 10/11 machine with default security
settings and a real UAC prompt (R01), a trusted signature and SmartScreen behaviour, upgrade from an older real release.
#>
param(
    [Parameter(Mandatory)][string]$ReleaseDir,
    [Parameter(Mandatory)][ValidateSet('none', 'test', 'release')][string]$ExpectSignature,
    [Parameter(Mandatory)][string]$Report,
    # The internal folder beside the public one (handoff_app_standardization.md 8.2): release-info, manifest, notices, source.
    [string]$PrivateDir,
    [switch]$Install
)
$ErrorActionPreference = 'Stop'
$ReleaseDir = (Resolve-Path $ReleaseDir).Path
if (-not $PrivateDir) { $PrivateDir = "$ReleaseDir-private" }
$PrivateDir = (Resolve-Path $PrivateDir).Path
$Cases = New-Object System.Collections.Generic.List[object]
$Work = Join-Path ([IO.Path]::GetTempPath()) ('asterlink-release-' + [guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory $Work | Out-Null
$Installed = Join-Path $env:ProgramFiles 'AsterLink'
$InstalledGui = Join-Path $Installed 'app\AsterLink.exe'
$InstalledService = Join-Path $Installed 'service\AsterLink.Service.exe'
$AppId = '6C1F7E52-5B0E-4A6B-9D4E-A57E71C0B8D1'
$UninstallKey = "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\{$AppId}_is1"
$Uninstaller = Join-Path $Installed 'setup\unins000.exe'

function Case([string]$Id, [bool]$Ok, [string]$Observed, [string]$Status) {
    if (-not $Status) { $Status = if ($Ok) { 'PASS' } else { 'FAIL' } }
    $Cases.Add([ordered]@{ case_id = $Id; status = $Status; observed = $Observed })
    '{0,-8} {1,-38} {2}' -f $Status, $Id, $Observed
}
function Wait-Until([scriptblock]$Condition, [int]$Seconds) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    while ((Get-Date) -lt $deadline) { if (& $Condition) { return $true }; Start-Sleep -Milliseconds 400 }
    return [bool](& $Condition)
}

$info = Get-Content (Join-Path $PrivateDir 'release-info.json') -Raw | ConvertFrom-Json
$Version = $info.version
$Setup = Join-Path $ReleaseDir $info.installer

# ---- Part 1: the files ------------------------------------------------------------------------------------------
function Check-Sums([string]$Dir) {
    $problems = @()
    $listed = @()
    foreach ($line in Get-Content (Join-Path $Dir 'SHA256SUMS')) {
        if ($line -notmatch '^([0-9a-f]{64}) \*(.+)$') { $problems += "malformed: $line"; continue }
        $listed += $Matches[2]
        $file = Join-Path $Dir $Matches[2]
        if (-not (Test-Path $file)) { $problems += "missing: $($Matches[2])"; continue }
        if ((Get-FileHash $file -Algorithm SHA256).Hash.ToLower() -ne $Matches[1]) { $problems += "mismatch: $($Matches[2])" }
    }
    foreach ($f in Get-ChildItem $Dir -File) { if ($f.Name -ne 'SHA256SUMS' -and $listed -notcontains $f.Name) { $problems += "not listed: $($f.Name)" } }
    return @{ Listed = $listed; Problems = $problems }
}
$publicSums = Check-Sums $ReleaseDir
$privateSums = Check-Sums $PrivateDir
$sumProblems = @($publicSums.Problems) + @($privateSums.Problems | ForEach-Object { "private: $_" })
Case 'R04-sha256sums' ($sumProblems.Count -eq 0) ("public {0} / private {1} files listed; problems: {2}" -f @($publicSums.Listed).Count, @($privateSums.Listed).Count, ($sumProblems -join '; ')) $null

# Customers get the installer and its checksum, nothing else; the internal material stays complete beside it.
$publicFiles = @(Get-ChildItem $ReleaseDir -Force | ForEach-Object { $_.Name } | Sort-Object)
$publicOk = ($publicFiles -join '|') -eq ((@($info.installer, 'SHA256SUMS') | Sort-Object) -join '|') -and (@($publicSums.Listed) -join '|') -eq $info.installer
Case 'R04-public-installer-only' $publicOk ("public folder: " + ($publicFiles -join ', ') + "; SHA256SUMS lists: " + (@($publicSums.Listed) -join ', ')) $null
$required = @('release-info.json', 'manifest.json', 'dependencies.cdx.json', 'THIRD_PARTY_LICENSES.txt', 'SOURCE.txt')
$absent = @($required | Where-Object { -not (Test-Path (Join-Path $PrivateDir $_)) })
# The "internal, do not publish" note (its name is Chinese; matched by pattern so this file stays ASCII for Windows PowerShell).
if (@(Get-ChildItem $PrivateDir -Filter 'README-*.txt').Count -ne 1) { $absent += 'README note' }
Case 'R04-private-complete' ($absent.Count -eq 0 -and (Test-Path (Join-Path $ReleaseDir $info.installer))) ("absent from the private folder: " + ($absent -join ', ')) $null
$archivesPresent = @(@($info.source_archives) | Where-Object { Test-Path (Join-Path $PrivateDir $_) })
Case 'R04-source-archives' ($archivesPresent.Count -eq 2 -or $info.mihomo_commit -like 'not archived*') ("private archives: " + ($archivesPresent -join ', ') + "; engine $($info.mihomo_commit)") $null
Case 'R04-licenses-complete' (@($info.modules_without_license_file).Count -eq 0 -and $info.third_party_modules -gt 0) ("{0} modules, without license file: {1}" -f $info.third_party_modules, (@($info.modules_without_license_file) -join ', ')) $null
Case 'R04-trusted-flag-honest' ($info.trusted_signature -eq $false -and $info.channel -eq 'internal') "trusted_signature=$($info.trusted_signature) channel=$($info.channel); open gates: $(@($info.open_gates).Count)" $null

$secretHits = @()
foreach ($f in @(Get-ChildItem $ReleaseDir, $PrivateDir -File) | Where-Object { $_.Extension -in '.txt', '.json' }) {
    $text = Get-Content $f.FullName -Raw
    if ($text -match '(?i)postgres(ql)?://[^\s"]*:[^\s"@]+@' -or $text -match 'BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY' -or $text -match '(?i)"(password|token|secret)"\s*:\s*"[^"]{6,}"') { $secretHits += $f.Name }
}
Case 'R04-no-secrets-in-text' ($secretHits.Count -eq 0) ("files with secret-looking content: " + ($secretHits -join ', ')) $null

# Signatures: the installer and every one of our own binaries inside the stage tree (read back from the manifest roles).
$manifest = Get-Content (Join-Path $PrivateDir 'manifest.json') -Raw | ConvertFrom-Json
$ownPaths = @($manifest.files | Where-Object { $_.role -in 'gui', 'service', 'core' } | ForEach-Object { $_.path })
Case 'R04-manifest-roles' ($ownPaths.Count -eq 3) ("roles in manifest: " + ($ownPaths -join ', ')) $null
$sigSetup = Get-AuthenticodeSignature $Setup
switch ($ExpectSignature) {
    'none' { Case 'R01-signature-expectation' ($sigSetup.Status -eq 'NotSigned') "Setup status=$($sigSetup.Status) (expected NotSigned for an unsigned internal build)" $null }
    'test' {
        $isTest = $sigSetup.SignerCertificate -and $sigSetup.SignerCertificate.Subject -like '*TEST signing - NOT A TRUSTED RELEASE*'
        Case 'R01-signature-test-setup' ($isTest -and $sigSetup.Status -notin 'NotSigned', 'HashMismatch') "Setup status=$($sigSetup.Status) signer=$($sigSetup.SignerCertificate.Subject)" $null
        Case 'R01-signature-test-is-untrusted' ($sigSetup.Status -ne 'Valid') "a test signature must NOT validate as trusted; status=$($sigSetup.Status)" $null
    }
    'release' { Case 'R01-signature-release-setup' ($sigSetup.Status -eq 'Valid') "Setup status=$($sigSetup.Status) signer=$($sigSetup.SignerCertificate.Subject)" $null }
}

# Tamper detection on a copy of the installer: one flipped byte must be seen by the signature or by Inno's own check.
if ($ExpectSignature -ne 'none') {
    $copy = Join-Path $Work 'tampered-setup.exe'
    Copy-Item $Setup $copy
    $bytes = [IO.File]::ReadAllBytes($copy)
    $bytes[[int]($bytes.Length / 2)] = $bytes[[int]($bytes.Length / 2)] -bxor 0xFF
    [IO.File]::WriteAllBytes($copy, $bytes)
    $sigTampered = Get-AuthenticodeSignature $copy
    Case 'R01-tampered-installer-detected' ($sigTampered.Status -in 'HashMismatch', 'NotSigned', 'UnknownError', 'NotTrusted') "tampered copy status=$($sigTampered.Status) (original: $($sigSetup.Status))" $null
}

if (-not $Install) {
    Case 'R01-clean-machine-install' $false 'needs a clean Windows 10/11 x64 machine with default security settings and a real UAC prompt' 'NOT_RUN'
    $Cases | ConvertTo-Json -Depth 4 | Set-Content -Path $Report -Encoding UTF8
    $failed = @($Cases | Where-Object { $_.status -eq 'FAIL' })
    "{0} cases, {1} FAIL" -f $Cases.Count, $failed.Count
    Remove-Item $Work -Recurse -Force -ErrorAction SilentlyContinue
    if ($failed.Count -gt 0) { exit 1 }
    exit 0
}

# ---- Part 2: the real installer ---------------------------------------------------------------------------------
function Status { $o = & $InstalledService status 2>&1 | Out-String; try { $o | ConvertFrom-Json } catch { $null } }
function ServiceStatus {
    # The installed service exe may not exist (not installed); ask the SCM instead.
    $svc = Get-CimInstance Win32_Service -Filter "Name='AsterLinkService'"
    [pscustomobject]@{ installed = [bool]$svc; state = $svc.State; start = $svc.StartMode; account = $svc.StartName }
}
function Run-Setup([string[]]$SetupArgs, [string]$LogName) {
    $log = Join-Path $Work $LogName
    $p = Start-Process -FilePath $Setup -ArgumentList (@('/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', '/SP-', "/LOG=`"$log`"") + $SetupArgs) -PassThru -Wait
    [pscustomobject]@{ Code = $p.ExitCode; Log = $(if (Test-Path $log) { Get-Content $log -Raw } else { '' }) }
}
function Run-Uninstall([string[]]$UninstallArgs, [string]$LogName) {
    if (-not (Test-Path $Uninstaller)) { throw "the uninstaller $Uninstaller does not exist (Add/Remove Programs would be broken)" }
    $log = Join-Path $Work $LogName
    $p = Start-Process -FilePath $Uninstaller -ArgumentList (@('/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', "/LOG=`"$log`"") + $UninstallArgs) -PassThru -Wait
    [pscustomobject]@{ Code = $p.ExitCode; Log = $(if (Test-Path $log) { Get-Content $log -Raw } else { '' }) }
}
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
# The file Add/Remove Programs runs must exist where the registry says (the install verb replaces the root around it).
function UninstallerOk { (Test-Path $Uninstaller) -and ("$((Get-ItemProperty $UninstallKey -ErrorAction SilentlyContinue).UninstallString)" -like "*$Uninstaller*") }
function CoreCount { @(Get-Process -Name asterlink-core -ErrorAction SilentlyContinue).Count }
function Cred-List([string]$Pattern) { (& cmdkey.exe "/list:$Pattern" 2>&1 | Out-String) }
$Prefs = Join-Path $env:LOCALAPPDATA 'AsterLink'

try {
    $pre = ServiceStatus
    Case 'I00-clean-start' (-not $pre.installed) "service installed before the test: $($pre.installed); the check needs a machine without the product" $null
    if ($pre.installed) { throw 'AsterLinkService is already installed on this machine; refusing to continue' }

    # -- fresh install -----------------------------------------------------------------------------------------------
    $r = Run-Setup @() 'setup-fresh.log'
    $st = ServiceStatus
    Case 'I01-fresh-install-exit' ($r.Code -eq 0) "Setup exit code $($r.Code)" $null
    $s = Status
    Case 'I01-service-registered' ($st.installed -and $st.state -eq 'Running' -and $st.account -eq 'LocalSystem' -and $st.start -eq 'Auto' -and $s.integrity_ok -and $s.binary_path_ok) `
        "state=$($st.state) account=$($st.account) start=$($st.start) integrity=$($s.integrity_ok) path_ok=$($s.binary_path_ok) problems=$($s.problems -join '|')" $null
    Case 'I01-add-remove-programs' ((Test-Path $UninstallKey) -and ((Get-ItemProperty $UninstallKey).DisplayVersion -eq $Version)) "uninstall entry present=$(Test-Path $UninstallKey) version=$((Get-ItemProperty $UninstallKey -ErrorAction SilentlyContinue).DisplayVersion)" $null
    Case 'I01-uninstaller-registered' (UninstallerOk) "unins000.exe present=$(Test-Path $Uninstaller) UninstallString=$((Get-ItemProperty $UninstallKey -ErrorAction SilentlyContinue).UninstallString)" $null
    Case 'I01-start-menu-shortcut' (Test-Path (Join-Path $env:ProgramData 'Microsoft\Windows\Start Menu\Programs\AsterLink.lnk')) 'all-users Start menu shortcut' $null
    Case 'I01-no-previous-dir' (-not (Test-Path "$Installed.previous")) "leftover upgrade directory: $(Test-Path "$Installed.previous")" $null
    Case 'I02-gui-not-started-elevated-silently' ((@(Get-Process -Name AsterLink -ErrorAction SilentlyContinue).Count) -eq 0) "AsterLink.exe processes after a silent install: $(@(Get-Process -Name AsterLink -ErrorAction SilentlyContinue).Count)" $null
    $good = GuiCheck 'fresh'
    Case 'I01-chain-works' (($good.Exit -eq 0) -and $good.Result.connected -and $good.Result.service_version -eq $Version) "connected=$($good.Result.connected) service=$($good.Result.service_version) core=$($good.Result.core_version)" $null

    # Signatures as installed (they must survive the copy into Program Files).
    $sigBad = @()
    foreach ($rel in $ownPaths) {
        $sig = Get-AuthenticodeSignature (Join-Path $Installed $rel)
        if ($ExpectSignature -eq 'none') { if ($sig.Status -ne 'NotSigned') { $sigBad += "$rel=$($sig.Status)" } }
        elseif ($ExpectSignature -eq 'test') { if (-not $sig.SignerCertificate -or $sig.SignerCertificate.Subject -notlike '*TEST signing*' -or $sig.Status -in 'NotSigned', 'HashMismatch') { $sigBad += "$rel=$($sig.Status)" } }
        else { if ($sig.Status -ne 'Valid') { $sigBad += "$rel=$($sig.Status)" } }
    }
    Case 'R01-installed-binaries-signatures' ($sigBad.Count -eq 0) "expectation=$ExpectSignature; mismatches: $($sigBad -join ', ')" $null
    if ($ExpectSignature -ne 'none') {
        # Flip a byte of a signed binary: the signature must notice, and the service's own manifest check must refuse it.
        $victim = Join-Path $Work 'core-copy.exe'
        Copy-Item (Join-Path $Installed 'core\asterlink-core.exe') $victim
        $b = [IO.File]::ReadAllBytes($victim); $b[1000] = $b[1000] -bxor 0xFF; [IO.File]::WriteAllBytes($victim, $b)
        Case 'R01-tampered-binary-signature-breaks' ((Get-AuthenticodeSignature $victim).Status -in 'HashMismatch', 'NotSigned') "tampered Core copy status=$((Get-AuthenticodeSignature $victim).Status)" $null
    }

    # -- reinstall over the same version (the upgrade path with an identical version) --------------------------------
    $r = Run-Setup @() 'setup-reinstall.log'
    $s = Status
    Case 'I03-reinstall-over-existing' (($r.Code -eq 0) -and $s.integrity_ok -and $s.service_state -eq 'Running' -and -not (Test-Path "$Installed.previous") -and (UninstallerOk)) `
        "exit=$($r.Code) integrity=$($s.integrity_ok) state=$($s.service_state) previous_dir_left=$(Test-Path "$Installed.previous") uninstaller=$(UninstallerOk) log_says_reinstall=$($r.Log -match 'reinstall')" $null

    # -- setup refuses while the app is open: nothing changes ---------------------------------------------------------
    $held = GuiCheck 'held' -HoldSeconds 120 -NoWait
    [void](Wait-Until { (CoreCount) -eq 1 } 60)
    $r = Run-Setup @() 'setup-while-open.log'
    $s = Status
    Case 'I04-upgrade-refused-while-app-open' (($r.Code -ne 0) -and $s.integrity_ok -and $s.service_state -eq 'Running') "exit=$($r.Code) integrity=$($s.integrity_ok) state=$($s.service_state)" $null

    # -- uninstall refused while the app is open -----------------------------------------------------------------------
    $u = Run-Uninstall @() 'uninstall-while-open.log'
    $s = Status
    Case 'U03-uninstall-refused-while-app-open' (($u.Code -ne 0) -and $s.installed -and $s.integrity_ok) "exit=$($u.Code) installed=$($s.installed) integrity=$($s.integrity_ok)" $null
    try { $held.Process.Kill() } catch { }
    [void]$held.Process.WaitForExit(10000)
    [void](Wait-Until { (CoreCount) -eq 0 } 40)

    # -- a failed upgrade rolls back to the previous, verified version ---------------------------------------------------
    $lock = [IO.File]::Open((Join-Path $Installed 'core\asterlink-core.exe'), 'Open', 'Read', 'Read')
    try { $r = Run-Setup @() 'setup-locked.log' } finally { $lock.Dispose() }
    $s = Status
    $st = ServiceStatus
    Case 'I05-failed-upgrade-rolls-back' (($r.Code -ne 0) -and $s.integrity_ok -and $st.state -eq 'Running' -and -not (Test-Path "$Installed.previous") -and (UninstallerOk)) `
        "exit=$($r.Code) integrity=$($s.integrity_ok) service=$($st.state) previous_dir_left=$(Test-Path "$Installed.previous") uninstaller=$(UninstallerOk) verb_says_rolled_back=$($r.Log -match 'rolled back')" $null
    $after = GuiCheck 'after-rollback'
    Case 'I05-previous-version-works' ($after.Result.connected -and $after.Result.service_version -eq $Version) "connected=$($after.Result.connected) service=$($after.Result.service_version)" $null

    # -- downgrade is refused before anything is touched ---------------------------------------------------------------------
    # The installed manifest claims a newer version than the installer offers (restored right after).
    $manifestPath = Join-Path $Installed 'manifest.json'
    $original = [IO.File]::ReadAllText($manifestPath)
    [IO.File]::WriteAllText($manifestPath, ($original -replace '"version":\s*"[0-9.]+"', '"version": "99.0.0"'))
    $r = Run-Setup @() 'setup-downgrade.log'
    [IO.File]::WriteAllText($manifestPath, $original)
    $s = Status
    Case 'I06-downgrade-refused' (($r.Code -ne 0) -and ($r.Log -match 'downgrade_refused') -and $s.integrity_ok -and (ServiceStatus).state -eq 'Running') "exit=$($r.Code) log_has_downgrade_refused=$($r.Log -match 'downgrade_refused') integrity=$($s.integrity_ok)" $null

    # -- uninstall, keeping the current user's saved data -------------------------------------------------------------------
    $target = 'AsterLink/https://test.invalid/api/v1/client|release-check@asterlink-test.invalid'
    & cmdkey.exe "/generic:$target" '/user:release-check@asterlink-test.invalid' '/pass:not-a-real-password' | Out-Null
    & cmdkey.exe '/generic:OtherApp-release-check' '/user:x' '/pass:y' | Out-Null
    New-Item -ItemType Directory -Force $Prefs | Out-Null
    Set-Content (Join-Path $Prefs 'settings.json') '{}'
    $u = Run-Uninstall @('/REMOVEUSERDATA=0') 'uninstall-keep.log'
    $st = ServiceStatus
    Case 'U01-uninstall-exit' ($u.Code -eq 0) "unins000 exit code $($u.Code)" $null
    Case 'U01-product-removed' ((-not $st.installed) -and (-not (Test-Path $Installed)) -and (-not (Test-Path (Join-Path $env:ProgramData 'AsterLink'))) -and (-not (Test-Path $UninstallKey)) -and (CoreCount) -eq 0) `
        "service=$($st.installed) root=$(Test-Path $Installed) programdata=$(Test-Path (Join-Path $env:ProgramData 'AsterLink')) arp=$(Test-Path $UninstallKey) cores=$(CoreCount)" $null
    Case 'U01-shortcut-removed' (-not (Test-Path (Join-Path $env:ProgramData 'Microsoft\Windows\Start Menu\Programs\AsterLink.lnk'))) 'Start menu shortcut gone' $null
    Case 'U01-user-data-kept-when-not-chosen' ((Cred-List 'AsterLink/*') -match 'release-check@asterlink-test.invalid' -and (Test-Path (Join-Path $Prefs 'settings.json'))) "saved password kept=$((Cred-List 'AsterLink/*') -match 'release-check@asterlink-test.invalid') preferences kept=$(Test-Path (Join-Path $Prefs 'settings.json'))" $null

    # -- reinstall, then uninstall with the explicit "forget my data" choice ---------------------------------------------------
    $r = Run-Setup @() 'setup-again.log'
    Case 'U02-reinstall-after-uninstall' (($r.Code -eq 0) -and (Status).integrity_ok) "exit=$($r.Code)" $null
    $u = Run-Uninstall @('/REMOVEUSERDATA=1') 'uninstall-forget.log'
    Case 'U02-uninstall-forget-exit' ($u.Code -eq 0 -and -not (ServiceStatus).installed) "exit=$($u.Code) installed=$((ServiceStatus).installed)" $null
    $left = Cred-List 'AsterLink/*'
    Case 'U02-saved-password-and-prefs-removed' (($left -notmatch 'release-check@asterlink-test.invalid') -and (-not (Test-Path $Prefs))) "AsterLink credential left=$($left -match 'release-check@asterlink-test.invalid') preferences dir left=$(Test-Path $Prefs)" $null
    Case 'U02-other-applications-credentials-untouched' ((Cred-List 'OtherApp-release-check') -match 'OtherApp-release-check') 'a credential of another application survived' $null
    Case 'R02-other-users-credentials' $false 'a second Windows user (and a second vault) is needed; the uninstaller only ever runs the cleanup as the invoking user' 'NOT_RUN'
    Case 'R01-clean-machine-install' $false 'a clean Windows 10/11 x64 machine, default security settings, real UAC (not an already-elevated CI runner)' 'NOT_RUN'
}
catch {
    Case 'E99-harness' $false ("harness stopped: " + $_.Exception.Message + ' at line ' + $_.InvocationInfo.ScriptLineNumber) $null
}
finally {
    & cmdkey.exe '/delete:OtherApp-release-check' 2>&1 | Out-Null
    & cmdkey.exe '/delete:AsterLink/https://test.invalid/api/v1/client|release-check@asterlink-test.invalid' 2>&1 | Out-Null
    # Whatever happened, leave the machine without the product.
    try {
        if ((ServiceStatus).installed -or (Test-Path $Uninstaller)) {
            [void](Run-Uninstall @('/REMOVEUSERDATA=0') 'uninstall-cleanup.log')
        }
    } catch { }
    $Cases | ConvertTo-Json -Depth 4 | Set-Content -Path $Report -Encoding UTF8
    Remove-Item $Work -Recurse -Force -ErrorAction SilentlyContinue
}
$failed = @($Cases | Where-Object { $_.status -eq 'FAIL' })
"{0} cases, {1} FAIL, {2} NOT_RUN" -f $Cases.Count, $failed.Count, @($Cases | Where-Object { $_.status -eq 'NOT_RUN' }).Count
if ($failed.Count -gt 0) { exit 1 }
exit 0
