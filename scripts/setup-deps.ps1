# AsyncAO Windows dependency bootstrap: installs MSYS2 (if missing) and the
# UCRT64 packages CGO needs (gcc, SDL3, SDL3_ttf, SDL3_mixer, opusfile, libwebp,
# libavif).
# Usage:  powershell -ExecutionPolicy Bypass -File scripts\setup-deps.ps1
$ErrorActionPreference = "Stop"

if (-not (Test-Path "C:\msys64\usr\bin\bash.exe")) {
    Write-Host "Installing MSYS2 via winget..."
    winget install MSYS2.MSYS2 --source winget --accept-source-agreements --accept-package-agreements --silent
}

$bash = "C:\msys64\usr\bin\bash.exe"
$packages = @(
    "mingw-w64-ucrt-x86_64-gcc",
    "mingw-w64-ucrt-x86_64-pkgconf",
    "mingw-w64-ucrt-x86_64-sdl3",
    "mingw-w64-ucrt-x86_64-sdl3-ttf",
    "mingw-w64-ucrt-x86_64-sdl3-mixer",
    "mingw-w64-ucrt-x86_64-opusfile",
    "mingw-w64-ucrt-x86_64-libwebp",
    "mingw-w64-ucrt-x86_64-libavif"
) -join " "

& $bash -lc "pacman -Sy --noconfirm --noprogressbar && pacman -S --noconfirm --needed --noprogressbar $packages"
if (-not $?) {
    Write-Warning "pacman failed. If you see SSL certificate errors (corporate AV/proxy),"
    Write-Warning "uncomment XferCommand in C:\msys64\etc\pacman.conf and add -k to curl,"
    Write-Warning "then re-run this script. Package signatures are still GPG-verified."
    exit 1
}
Write-Host "Done. Build with: powershell -File scripts\build.ps1"
