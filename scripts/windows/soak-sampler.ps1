<#
Long-run sampler for the INSTALLED product (round 6, case R03). It watches the three AsterLink processes while you
use or leave a real connection up, and writes one CSV row per process per sample. It changes nothing on the machine.

  powershell -File scripts\windows\soak-sampler.ps1 -Hours 8 -IntervalSeconds 60 -Out soak-8h.csv

Start it, then connect in the AsterLink window and leave the machine as it normally is (this is where sleep, Wi-Fi
changes and normal browsing should happen). A process that is not running yet is simply skipped for that sample.
At the end it prints first / last / maximum values per process. It does NOT decide pass or fail on its own: "no
unexplained accumulation" is a judgement made with the CSV in hand, and nothing here is a performance promise.

Columns: time_utc, process, pid, cpu_seconds, working_set_mib, private_mib, handles, threads
Sampling the Core (SYSTEM) needs an elevated PowerShell for the handle and memory counters.
#>
param(
    [double]$Hours = 8,
    [int]$IntervalSeconds = 60,
    [Parameter(Mandatory)][string]$Out
)
$ErrorActionPreference = 'Continue'
if (Test-Path $Out) { throw "$Out already exists" }
if ($IntervalSeconds -lt 5) { throw '-IntervalSeconds must be at least 5' }
$names = @('AsterLink', 'AsterLink.Service', 'asterlink-core')
'time_utc,process,pid,cpu_seconds,working_set_mib,private_mib,handles,threads' | Set-Content -Encoding ASCII $Out
$deadline = (Get-Date).AddHours($Hours)
$rows = New-Object System.Collections.Generic.List[object]
while ((Get-Date) -lt $deadline) {
    $stamp = (Get-Date).ToUniversalTime().ToString('s') + 'Z'
    foreach ($n in $names) {
        foreach ($p in @(Get-Process -Name $n -ErrorAction SilentlyContinue)) {
            try {
                $cpu = [math]::Round($p.TotalProcessorTime.TotalSeconds, 2)
                $row = [pscustomobject]@{
                    time = $stamp; process = $n; pid = $p.Id; cpu = $cpu
                    ws = [math]::Round($p.WorkingSet64 / 1MB, 1); priv = [math]::Round($p.PrivateMemorySize64 / 1MB, 1)
                    handles = $p.HandleCount; threads = $p.Threads.Count
                }
                $rows.Add($row)
                ('{0},{1},{2},{3},{4},{5},{6},{7}' -f $row.time, $row.process, $row.pid, $row.cpu, $row.ws, $row.priv, $row.handles, $row.threads) |
                    Add-Content -Encoding ASCII $Out
            } catch { }
        }
    }
    Start-Sleep -Seconds $IntervalSeconds
}
foreach ($n in $names) {
    $mine = @($rows | Where-Object { $_.process -eq $n })
    if ($mine.Count -eq 0) { "$n : never seen"; continue }
    $first = $mine[0]; $last = $mine[-1]
    '{0,-18} samples={1} handles {2}->{3} (max {4}) threads {5}->{6} (max {7}) private {8}->{9} MiB (max {10}) cpu {11}s pids={12}' -f `
        $n, $mine.Count, $first.handles, $last.handles, ($mine | Measure-Object handles -Maximum).Maximum,
        $first.threads, $last.threads, ($mine | Measure-Object threads -Maximum).Maximum,
        $first.priv, $last.priv, ($mine | Measure-Object priv -Maximum).Maximum, $last.cpu, (($mine | Select-Object -ExpandProperty pid -Unique) -join '/')
}
"CSV: $Out (a changing pid means that process restarted during the run)"
