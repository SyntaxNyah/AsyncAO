# AsyncAO DEBUG build script (MSYS2 UCRT64 toolchain).
# Produces a symbolized, Delve-friendly test build for crash diagnosis.
# Usage:  powershell -ExecutionPolicy Bypass -File scripts\build-debug.ps1 [-Run]
#
# Differences from build.ps1 (release):
#   * keeps the console subsystem (no -H=windowsgui) so stderr is visible,
#   * keeps symbols (omits -s -w) and disables optimization/inlining
#     (-gcflags "all=-N -l") so stack traces carry file:line and Delve works,
#   * writes to testbuild-debug\ instead of bin\.
#
# Crash logs this build produces (all next to the exe):
#   testbuild-debug\asyncao-crash.log                    <- ANY unhandled panic /
#                                                           fatal error (process-
#                                                           wide hook in main.go)
#   testbuild-debug\recordings\scene-maker-crash.log     <- frame-loop / picker
#                                                           panics recovered by the UI
param(
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

$out = "testbuild-debug"
# No inlining / no optimization: clean, full file:line stack traces and a binary
# Delve can debug. (Release builds strip symbols with -s -w and optimize.)
$flags = @("-gcflags", "all=-N -l")

New-Item -ItemType Directory -Force $out | Out-Null
go build @flags -o "$out\asyncao.exe" .\cmd\asyncao
if (-not $?) { exit 1 }
Write-Host "Built $out\asyncao.exe (debug)"

# Stage the runtime DLL closure (same recursive-import walk as build.ps1, so the
# DLL set can't drift as SDL2_mixer/webp/avif pull in new deps).
$seen = @{}
$queue = New-Object System.Collections.Generic.Queue[string]
$queue.Enqueue("$out\asyncao.exe")
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
        if (Test-Path $src) {
            Copy-Item $src "$out\" -Force
            $queue.Enqueue($src)
            $staged++
        }
    }
}
Write-Host "Runtime DLLs staged in $out\ ($staged)"

Write-Host ""
Write-Host "Reproduce the crash, then collect:"
Write-Host "  $out\asyncao-crash.log"
Write-Host "  $out\recordings\scene-maker-crash.log"

if ($Run) {
    Write-Host ""
    Write-Host "Launching with -debug (pprof on http://localhost:6060/debug/pprof/)..."
    Write-Host "Ctrl+C in this console stops the client; stderr stays visible here."
    & "$out\asyncao.exe" -debug
}
