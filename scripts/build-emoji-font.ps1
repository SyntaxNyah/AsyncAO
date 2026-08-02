<#
.SYNOPSIS
    Rebuilds internal/ui/fonts/Twemoji.ttf, the bundled colour-emoji face.

.DESCRIPTION
    A committed binary font is a mystery unless the recipe that made it is
    committed beside it. This is that recipe.

    The font is a COLRv0/CPAL build of the Twemoji artwork. COLRv0 is a hard
    requirement, not a preference: SDL_ttf cannot render COLRv1, which is why
    Windows' own 11.9 MB Segoe UI Emoji is deliberately unused. Worse, the
    failure is SILENT — internal/ui/emoji.go's colorFontRenderable rejects a
    non-COLRv0 face and quietly falls back, so a wrong --color_format here would
    ship as "emoji stopped working" with no error anywhere. Verify the output.

    Scratch lives in _emojibuild/ (gitignored): a Python venv, ~4000 upstream
    SVGs and a 10 MB source zip. Only the finished .ttf is committed.

    Takes roughly half an hour on a normal machine (8000+ ninja steps).

.NOTES
    Requires Python 3.10+ on PATH and network access. Verified on Python 3.13.
#>
[CmdletBinding()]
param(
    # Upstream tag to build from. jdecked/twemoji is the maintained successor to
    # the frozen twitter/twemoji, which never went past Emoji 14.0 (2021).
    [string]$Tag = 'v17.0.3',
    [string]$ScratchDir = "$PSScriptRoot\..\_emojibuild",
    [string]$OutFile = "$PSScriptRoot\..\internal\ui\fonts\Twemoji.ttf"
)

$ErrorActionPreference = 'Stop'
$scratch = (New-Item -ItemType Directory -Force $ScratchDir).FullName
Push-Location $scratch
try {
    if (-not (Test-Path "$scratch\.venv\Scripts\nanoemoji.exe")) {
        Write-Host "==> creating venv and installing nanoemoji"
        python -m venv .venv
        & "$scratch\.venv\Scripts\python.exe" -m pip install --upgrade pip -q
        & "$scratch\.venv\Scripts\python.exe" -m pip install nanoemoji -q
    }

    if (-not (Test-Path "$scratch\svg")) {
        Write-Host "==> fetching Twemoji $Tag artwork"
        # codeload zip, NOT git clone: the repo's packed history is ~800 MB.
        curl.exe -sL -o twemoji.zip "https://codeload.github.com/jdecked/twemoji/zip/refs/tags/$Tag"
        Expand-Archive twemoji.zip -DestinationPath _zip -Force
        New-Item -ItemType Directory -Force svg | Out-Null
        Copy-Item "_zip\twemoji-$($Tag.TrimStart('v'))\assets\svg\*.svg" svg\

        # Exactly one upstream SVG is not square (watermelon, viewBox
        # "0 0 36 25.22"). nanoemoji fits each source to the em box, so left
        # alone it ships visibly stretched. Re-wrap it, centred, without
        # touching any path data.
        Write-Host "==> squaring the one non-square source (1f349 watermelon)"
        & "$scratch\.venv\Scripts\python.exe" -c @'
import re
p = "svg/1f349.svg"
s = open(p, encoding="utf-8").read()
if 'viewBox="0 0 36 25.22"' in s:
    head_end = s.index(">", s.index("<svg")) + 1
    head = s[:head_end].replace('viewBox="0 0 36 25.22"', 'viewBox="0 0 36 36"')
    inner = s[head_end:s.rindex("</svg>")]
    open(p, "w", encoding="utf-8").write(head + '<g transform="translate(0 5.39)">' + inner + "</g></svg>")
    print("  rewrapped", p)
else:
    print("  already square, or upstream changed it - CHECK THE ART")
'@
    }

    Write-Host "==> building COLRv0 font (this takes a while)"
    # The venv MUST be on PATH: nanoemoji's generated build.ninja invokes
    # `picosvg` as a bare command while calling python by absolute path, so an
    # absolute-path invocation alone dies at step 1 with "CreateProcess failed".
    $env:PATH = "$scratch\.venv\Scripts;$env:PATH"
    # The .toml is POSITIONAL. --config_file exists in --helpfull but is a decoy
    # that the driver ignores; it picks argv entries by suffix.
    & "$scratch\.venv\Scripts\nanoemoji.exe" twemoji.toml

    $built = "$scratch\build\TwemojiAsyncAO.ttf"
    if (-not (Test-Path $built)) { throw "nanoemoji produced no font at $built" }
    Copy-Item $built $OutFile -Force
    $mb = [math]::Round((Get-Item $OutFile).Length / 1MB, 2)
    Write-Host "==> wrote $OutFile ($mb MB)"
    Write-Host "==> now run: go test -run 'TestEmoji' -p 1 ./internal/ui/ ./internal/render/"
    Write-Host "    (that is what proves COLR is v0, the codepoints are there,"
    Write-Host "     and ZWJ sequences still compose to one glyph)"
} finally {
    Pop-Location
}
