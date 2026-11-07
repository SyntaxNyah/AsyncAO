# Code signing (Windows SmartScreen + macOS notarization)

The release pipeline (`.github/workflows/build.yml`) signs the Windows `.exe`
and codesigns/notarizes the macOS binary **and its bundled dylibs** **when the
signing secrets are present**.

Signing is fully optional: with no secrets, the pipeline ships **unsigned**
builds exactly as before — Windows users may see a SmartScreen "unknown
publisher" prompt and macOS users a Gatekeeper warning, but nothing breaks. Add
the secrets below (repo → Settings → Secrets and variables → Actions) to turn
signing on; nothing else changes.

> Signing an unsigned Windows binary is also the durable fix for the
> `Trojan:Win32/Bearfoos.A!ml` Defender false positive — see
> `docs/DEFENDER-FALSE-POSITIVE.md` for the full maintainer checklist and the
> OV-vs-EV tradeoff in context.

> These steps were authored without certificates to test against. With a
> certificate set, a signing failure fails the release; with none, signing is
> skipped. Treat the first signed release as a shakedown and read the CI logs.

## Windows (Authenticode → fewer SmartScreen warnings)

You need a **code-signing certificate** as a password-protected `.pfx`:

- **OV** (Organisation Validation) — cheaper; SmartScreen reputation builds up
  over downloads/time.
- **EV** (Extended Validation) — clears SmartScreen immediately, but the private
  key usually lives on an HSM/token (this PFX flow won't fit an EV token; use the
  CA's cloud-signing tool or Azure Trusted Signing instead).

Encode the PFX to base64 and add two secrets:

```powershell
[Convert]::ToBase64String([IO.File]::ReadAllBytes("asyncao-codesign.pfx")) | Set-Clipboard
```

| Secret | Value |
|--------|-------|
| `WINDOWS_CODESIGN_PFX_BASE64` | base64 of the `.pfx` |
| `WINDOWS_CODESIGN_PASSWORD`   | the PFX password |

The step signs with SHA-256 and an RFC-3161 timestamp (so the signature outlives
the certificate), then verifies, **before** zipping, so the bundle carries the
signed `.exe`.

## macOS (Developer ID codesign + notarization)

Requires a paid **Apple Developer** account and a **"Developer ID Application"**
certificate exported as a `.p12` (Keychain Access → export). Base64-encode it:

```bash
base64 -i developer_id_application.p12 | pbcopy
```

| Secret | Value |
|--------|-------|
| `APPLE_CODESIGN_P12_BASE64` | base64 of the `.p12` |
| `APPLE_CODESIGN_PASSWORD`   | the `.p12` export password |
| `APPLE_CODESIGN_IDENTITY`   | e.g. `Developer ID Application: Your Name (TEAMID)` |
| `APPLE_NOTARY_APPLE_ID`     | your Apple ID email (notarization) |
| `APPLE_NOTARY_PASSWORD`     | an **app-specific password** (appleid.apple.com) |
| `APPLE_NOTARY_TEAM_ID`      | your 10-char Team ID |

Codesigning works with just the first three secrets; add the `APPLE_NOTARY_*`
three to also notarize.

**What gets signed.** The macOS build is self-contained: `scripts/bundle-macos.sh`
collects the SDL3/libwebp/libavif dylib closure into a `lib/` folder beside the
binary (the macOS analogue of the Windows DLL staging). The codesign step then
signs **inside-out** — every `lib/*.dylib` first, then the binary that loads
them — because notarization rejects a Mach-O whose nested code is unsigned. With
no secrets the bundle script has already *ad-hoc* signed everything, so the build
still runs on a clean Mac (Gatekeeper just warns).

**What gets notarized.** The notarize `.zip` contains the **whole staged folder**
(binary + `lib/`), not just the bare binary, so Gatekeeper's online check covers
the nested dylibs. A folder/bare binary can't be *stapled* (stapling needs an
`.app`/`.dmg`/`.pkg`), so verification happens online on first run. Packaging a
`.dmg` for offline stapling is a future step.

Two macOS assets ship: the bundle tarball
(`asyncao-macos-bundle-arm64.tar.gz`, binary + bundled `lib/` + `INSTALL.txt`),
which installs without Homebrew and is what the in-app updater downloads, and
the Homebrew edition tarball (`asyncao-macos-homebrew-arm64.tar.gz`, the binary
+ `INSTALL.txt`, libraries from `brew`). No bare binary ships, because clients
up to v1.95.1 would copy it over themselves without its libraries. Both tarballs' `INSTALL.txt`
carries the `xattr -dr com.apple.quarantine .` relief for the first-launch
Gatekeeper prompts an unsigned/unnotarized download triggers per bundled Mach-O.

> **Self-update and `lib/`.** The in-app updater downloads the bundle tarball and
> replaces the binary and every dylib in `lib/` together (`internal/update.StageBundle`),
> so a library version change (`libavif.16.dylib` → `libavif.17.dylib`) can't leave a
> new binary beside an old `lib/`. `preflightDarwinSwap` still refuses a single-binary
> swap whose `@rpath` imports are missing from a sibling `lib/`.

## What is NOT signed

- **Linux** AppImage / Flatpak — Linux uses repo/distro trust, not per-binary
  Authenticode-style signing; nothing to configure.
- **Local dev builds** (`scripts\build.ps1`) — unsigned by design.
