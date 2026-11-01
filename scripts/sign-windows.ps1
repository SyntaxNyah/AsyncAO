# Authenticode-signs dist\stage\asyncao.exe. Skips with no certificate; fails if a
# certificate exists and signing breaks. See docs/CODE-SIGNING.md for the secrets.
$ErrorActionPreference = "Stop"
$exe = "dist\stage\asyncao.exe"

if ([string]::IsNullOrEmpty($env:WIN_CERT_BASE64)) {
    Write-Host "::notice::No WINDOWS_CODESIGN_PFX_BASE64 secret: shipping an unsigned Windows build."
    exit 0
}

$pfx = Join-Path $env:RUNNER_TEMP "asyncao-codesign.pfx"
[IO.File]::WriteAllBytes($pfx, [Convert]::FromBase64String($env:WIN_CERT_BASE64))
try {
    $signtool = Get-ChildItem "C:\Program Files (x86)\Windows Kits\10\bin\*\x64\signtool.exe" |
        Sort-Object FullName -Descending | Select-Object -First 1
    if (-not $signtool) { throw "signtool.exe not found in the Windows SDK" }
    # The RFC-3161 timestamp keeps the signature valid after the certificate expires.
    & $signtool.FullName sign /f $pfx /p $env:WIN_CERT_PASSWORD /fd SHA256 `
        /tr http://timestamp.digicert.com /td SHA256 $exe
    if ($LASTEXITCODE -ne 0) { throw "signtool sign failed ($LASTEXITCODE)" }
    & $signtool.FullName verify /pa $exe
    if ($LASTEXITCODE -ne 0) { throw "signtool verify failed ($LASTEXITCODE)" }
} finally {
    Remove-Item $pfx -Force
}
