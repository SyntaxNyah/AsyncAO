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
    $flags = @("-pgo=auto", "-trimpath", "-ldflags", "-s -w")
}

New-Item -ItemType Directory -Force bin | Out-Null
go build @flags -o bin\asyncao.exe .\cmd\asyncao
if (-not $?) { exit 1 }
Write-Host "Built bin\asyncao.exe"

# Copy runtime DLLs next to the exe so it runs without MSYS2 on PATH.
$dlls = @(
    "SDL2.dll", "SDL2_ttf.dll", "SDL2_mixer.dll",
    "libwebp-7.dll", "libwebpdemux-2.dll", "libsharpyuv-0.dll",
    "libfreetype-6.dll", "libbz2-1.dll", "libbrotlidec.dll", "libbrotlicommon.dll",
    "libpng16-16.dll", "zlib1.dll", "libharfbuzz-0.dll", "libglib-2.0-0.dll",
    "libgraphite2.dll", "libintl-8.dll", "libiconv-2.dll", "libpcre2-8-0.dll",
    "libopusfile-0.dll", "libopus-0.dll", "libogg-0.dll", "libvorbis-0.dll",
    "libvorbisfile-3.dll", "libmpg123-0.dll", "libwavpack-1.dll",
    "libgcc_s_seh-1.dll", "libwinpthread-1.dll", "libstdc++-6.dll",
    "libxmp.dll", "libgme.dll", "libzstd.dll", "libshlwapi.dll"
)
foreach ($dll in $dlls) {
    $src = Join-Path "$msys\bin" $dll
    if (Test-Path $src) { Copy-Item $src bin\ -Force }
}
Write-Host "Runtime DLLs staged in bin\"

if ($Run) { & bin\asyncao.exe }
