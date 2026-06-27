# AsyncAO Windows build script (MSYS2 UCRT64 toolchain).
# Usage:  powershell -ExecutionPolicy Bypass -File scripts\build.ps1 [-Release] [-Run]
param(
    [switch]$Release,
    [switch]$Run
)

$ErrorActionPreference = "Stop"
$msys = "C:\msys64\ucrt64"
if (-not (Test-Path "$msys\bin\gcc.exe")) {
    Write-Error "MSYS2 UCRT64 gcc not found. Run scripts\setup-deps.ps1 first."
}

$env:PATH        = "$msys\bin;$env:PATH"
$env:CGO_ENABLED = "1"
$env:CC          = "$msys\bin\gcc.exe"
$env:CGO_CFLAGS  = "-I$msys\include"
$env:CGO_LDFLAGS = "-L$msys\lib"
$env:PKG_CONFIG_PATH = "$msys\lib\pkgconfig"

$flags = @()
if ($Release) {
    # Stamp the build version for the self-update check (M13) from the git tag,
    # e.g. v1.2.3 or v1.2.3-5-gabc1234-dirty. No tag -> stays "dev" (a dev build
    # never self-updates). Dev (non -Release) builds are intentionally unstamped.
    # -H=windowsgui links a GUI-subsystem exe so no console window opens on
    # launch (release builds only; dev builds keep the console for logs).
    $ldflags = "-s -w -H=windowsgui"
    $ver = (git describe --tags --dirty 2>$null)
    if ($LASTEXITCODE -eq 0 -and $ver) {
        $ldflags = "$ldflags -X github.com/SyntaxNyah/AsyncAO/internal/update.Version=$ver"
        Write-Host "Stamping version $ver"
    }
    $flags = @("-pgo=auto", "-trimpath", "-ldflags", $ldflags)
}

New-Item -ItemType Directory -Force bin | Out-Null
go build @flags -o bin\asyncao.exe .\cmd\asyncao
if (-not $?) { exit 1 }
Write-Host "Built bin\asyncao.exe"

# Stage the runtime DLL closure next to the exe so it runs without MSYS2 on PATH.
# Walked recursively from the exe's import table (objdump) and resolved against
# the UCRT64 bin, rather than a hardcoded list: that list silently rotted once
# (SDL2_mixer pulled in libFLAC/libjpeg-8, the list didn't, the dev build broke
# with "DLL missing"). This computes the exact set, so it can't drift again.
# CI's release path does the equivalent with `ldd`.
$seen = @{}
$queue = New-Object System.Collections.Generic.Queue[string]
$queue.Enqueue("bin\asyncao.exe")
$staged = 0
while ($queue.Count -gt 0) {
    $f = $queue.Dequeue()
    $imports = & "$msys\bin\objdump.exe" -p $f 2>$null |
        Select-String "DLL Name:" |
        ForEach-Object { ($_ -split "DLL Name: ")[1].Trim() }
    foreach ($d in $imports) {
        $key = $d.ToLower()
        if ($seen.ContainsKey($key)) { continue }
        $seen[$key] = $true
        $src = Join-Path "$msys\bin" $d
        if (Test-Path $src) {        # a UCRT64-provided DLL => stage it and recurse
            Copy-Item $src bin\ -Force
            $queue.Enqueue($src)
            $staged++
        }
    }
}
Write-Host "Runtime DLLs staged in bin\ ($staged)"

if ($Run) { & bin\asyncao.exe }
