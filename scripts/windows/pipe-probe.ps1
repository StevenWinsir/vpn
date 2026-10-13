<#
Sends raw frames to the AsterLink service pipe from an UNRELATED process (this PowerShell), the way a
fake GUI would. Prints one JSON object: what the service answered, or that it closed the pipe.

  pipe-probe.ps1 -Case hello          a syntactically perfect hello from a program that is not the GUI
  pipe-probe.ps1 -Case garbage        a frame whose body is not JSON
  pipe-probe.ps1 -Case oversize       a header that declares more than the first-frame limit, no body
  pipe-probe.ps1 -Case unknown        a well-formed request for a command that does not exist
#>
param([Parameter(Mandatory)][ValidateSet('hello', 'garbage', 'oversize', 'unknown')][string]$Case)

$ErrorActionPreference = 'Stop'
function Send-Frame([System.IO.Stream]$stream, [byte[]]$body) {
    $header = [BitConverter]::GetBytes([uint32]$body.Length); [Array]::Reverse($header)
    $all = [byte[]]($header + $body); $stream.Write($all, 0, $all.Length); $stream.Flush()
}
function Read-Exact([System.IO.Stream]$stream, [int]$count) {
    $buffer = New-Object byte[] $count; $got = 0
    while ($got -lt $count) { $n = $stream.Read($buffer, $got, $count - $got); if ($n -le 0) { return $null }; $got += $n }
    return $buffer
}

$pipe = [System.IO.Pipes.NamedPipeClientStream]::new('.', 'AsterLink.Service.v1', [System.IO.Pipes.PipeDirection]::InOut,
    [System.IO.Pipes.PipeOptions]::None, [System.Security.Principal.TokenImpersonationLevel]::Identification)
try {
    $pipe.Connect(5000)
    switch ($Case) {
        'hello'    { Send-Frame $pipe ([Text.Encoding]::UTF8.GetBytes('{"request_id":"probe","protocol_version":1,"generation":0,"command":"hello","payload":{"app_version":"0.1.0"}}')) }
        'garbage'  { Send-Frame $pipe ([Text.Encoding]::UTF8.GetBytes('this is not json')) }
        'unknown'  { Send-Frame $pipe ([Text.Encoding]::UTF8.GetBytes('{"request_id":"probe","protocol_version":1,"generation":0,"command":"import_config","payload":{"path":"C:\\evil.yaml"}}')) }
        'oversize' { $pipe.Write([byte[]]@(0x00, 0x20, 0x00, 0x01), 0, 4); $pipe.Flush() }
    }
    $header = Read-Exact $pipe 4
    if ($null -eq $header) { @{ answered = $false; closed = $true } | ConvertTo-Json -Compress; exit 0 }
    $size = [int](([uint32]$header[0] * 16777216) + ([uint32]$header[1] * 65536) + ([uint32]$header[2] * 256) + [uint32]$header[3])
    $body = Read-Exact $pipe $size
    $response = [Text.Encoding]::UTF8.GetString($body) | ConvertFrom-Json
    @{ answered = $true; ok = $response.ok; code = $response.error.code } | ConvertTo-Json -Compress
} finally { $pipe.Dispose() }
