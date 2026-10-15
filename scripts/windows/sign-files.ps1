<#
Authenticode signing for the Windows client, one entry point for three modes. Used by
scripts/package-native-windows.py and, through Inno Setup's SignTool, for Setup.exe and the uninstaller.

  sign-files.ps1 -Mode test       -Files a.exe,b.exe
  sign-files.ps1 -Mode thumbprint -Files ...      # a certificate already in the store / on a token (EV, HSM, cloud signing)
  sign-files.ps1 -Mode pfx        -Files ...      # a PFX; the path and password come from the environment

Secrets never travel on a command line:
  ASTERLINK_SIGN_THUMBPRINT   SHA-1 thumbprint of the certificate (mode thumbprint)
  ASTERLINK_SIGN_PFX          path to the PFX file                  (mode pfx)
  ASTERLINK_SIGN_PFX_PASSWORD PFX password                           (mode pfx)
  ASTERLINK_TIMESTAMP_URL     RFC 3161 time-stamp server; REQUIRED for thumbprint/pfx, never used for test

Mode "test" creates (or reuses, per CurrentUser) a self-signed code-signing certificate named
"AsterLink TEST signing - NOT A TRUSTED RELEASE". It proves the pipeline end to end (what is signed, in what
order, that verification sees the signature and that tampering is detected). It is NOT trusted by any
machine, gives no SmartScreen reputation and must never ship to customers.
#>
param(
    [Parameter(Mandatory)][ValidateSet('test', 'thumbprint', 'pfx')][string]$Mode,
    [Parameter(Mandatory)][string[]]$Files
)
$ErrorActionPreference = 'Stop'
$TestSubject = 'CN=AsterLink TEST signing - NOT A TRUSTED RELEASE'

function Get-SigningCertificate {
    switch ($Mode) {
        'test' {
            $existing = Get-ChildItem Cert:\CurrentUser\My -CodeSigningCert | Where-Object { $_.Subject -eq $TestSubject -and $_.NotAfter -gt (Get-Date).AddDays(2) } | Select-Object -First 1
            if ($existing) { return $existing }
            return New-SelfSignedCertificate -Type CodeSigningCert -Subject $TestSubject -KeyAlgorithm RSA -KeyLength 3072 -HashAlgorithm SHA256 -NotAfter (Get-Date).AddMonths(6) -CertStoreLocation Cert:\CurrentUser\My
        }
        'thumbprint' {
            $thumb = $env:ASTERLINK_SIGN_THUMBPRINT
            if (-not $thumb) { throw 'ASTERLINK_SIGN_THUMBPRINT is not set' }
            $cert = Get-ChildItem Cert:\CurrentUser\My, Cert:\LocalMachine\My -CodeSigningCert -ErrorAction SilentlyContinue | Where-Object { $_.Thumbprint -eq $thumb } | Select-Object -First 1
            if (-not $cert) { throw "no code-signing certificate with that thumbprint is available to this account" }
            return $cert
        }
        'pfx' {
            if (-not $env:ASTERLINK_SIGN_PFX -or -not $env:ASTERLINK_SIGN_PFX_PASSWORD) { throw 'ASTERLINK_SIGN_PFX and ASTERLINK_SIGN_PFX_PASSWORD must be set' }
            $secure = ConvertTo-SecureString $env:ASTERLINK_SIGN_PFX_PASSWORD -AsPlainText -Force
            $imported = Import-PfxCertificate -FilePath $env:ASTERLINK_SIGN_PFX -CertStoreLocation Cert:\CurrentUser\My -Password $secure
            return $imported | Where-Object { $_.HasPrivateKey } | Select-Object -First 1
        }
    }
}

if ($Mode -ne 'test' -and -not $env:ASTERLINK_TIMESTAMP_URL) {
    throw 'ASTERLINK_TIMESTAMP_URL is required: a release signature without a time stamp stops validating when the certificate expires'
}
$Files = @($Files | ForEach-Object { $_ -split ',' } | Where-Object { $_ })
$cert = Get-SigningCertificate
$failed = 0
foreach ($file in $Files) {
    $path = (Resolve-Path $file).Path
    if ($Mode -eq 'test') {
        $result = Set-AuthenticodeSignature -FilePath $path -Certificate $cert -HashAlgorithm SHA256
    } else {
        $result = Set-AuthenticodeSignature -FilePath $path -Certificate $cert -HashAlgorithm SHA256 -TimestampServer $env:ASTERLINK_TIMESTAMP_URL
    }
    # A self-signed certificate is reported UnknownError ("not trusted") by design; a missing signature or a
    # hash mismatch is not acceptable in any mode.
    $signature = Get-AuthenticodeSignature $path
    $ok = $signature.SignerCertificate -and $signature.SignerCertificate.Thumbprint -eq $cert.Thumbprint -and $signature.Status -notin @('NotSigned', 'HashMismatch')
    if ($Mode -ne 'test' -and $signature.Status -ne 'Valid') { $ok = $false }
    Write-Host ("signed {0}: {1} (signer {2})" -f (Split-Path $path -Leaf), $signature.Status, $signature.SignerCertificate.Subject)
    if (-not $ok) { $failed++ }
}
if ($failed -gt 0) { throw "$failed file(s) did not end up with the expected signature" }
if ($Mode -eq 'test') { Write-Host "TEST certificate $($cert.Thumbprint): not trusted by anyone, for pipeline verification only" }
