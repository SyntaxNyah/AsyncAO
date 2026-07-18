# Code signing (Windows SmartScreen + macOS notarization)

The release pipeline (`.github/workflows/release.yml`) signs the Windows `.exe`
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

> These steps were authored without certificates to test against. The macOS step
> is **best-effort** (a hiccup logs a warning but never fails the release, like
> the Flatpak job). Treat the first signed release as a shakedown and read the CI
> logs.

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
the certificate), then verifies, **before** zipping — so both the bare
self-update `.exe` and the install bundle carry the signature.

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
collects the SDL2/libwebp/libavif dylib closure into a `lib/` folder beside the
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

Two macOS assets ship per variant: the bundle tarball
(`asyncao-macos-bundle-arm64.tar.gz`) for first install, and the rewritten bare
binary (`asyncao-macos-arm64`) as the self-update target. See
`internal/update.SelfUpdateAssetMatch` for why the tarball name deliberately
dodges the `macos-arm64` self-update token.

> **Self-update constraint (SONAME skew).** On macOS the self-updater swaps only
> the bare `asyncao-macos-arm64` binary — it does NOT refresh the sibling `lib/`
> from the tarball. The rewritten binary loads its dylibs by `@rpath`, resolved
> in order `@executable_path/lib` → `/opt/homebrew/lib` → `/usr/local/lib`
> (`scripts/bundle-macos.sh`). That is safe as long as the dylib SONAMEs the new
> binary references still exist in the installed `lib/`. If a future release bumps
> a Homebrew dependency across a SONAME change (libwebp/libavif do this — e.g.
> `libavif.16.dylib` → `libavif.17.dylib`), a self-updated binary would reference
> the NEW soname, the stale bundled `lib/` only has the OLD one, `@executable_path/lib`
> misses, and the rpath falls through to `/opt/homebrew/lib` — which the tarball's
> no-Homebrew audience (the whole reason the bundle exists) does not have, so the
> app would fail to launch.
>
> **This is guarded in code, not just here.** Before it swaps anything,
> `internal/update.StageReplace` runs `preflightDarwinSwap`: when a sibling `lib/`
> exists (the tarball install — the population that can skew), it parses the
> downloaded binary's Mach-O `@rpath` dylib imports and, if any is absent from that
> `lib/`, REFUSES the swap *before touching the install* and surfaces a
> "re-download the bundle tarball" message. The install is left pristine, so the
> user simply grabs the new tarball (which carries a matching `lib/`). A bare brew
> install has no sibling `lib/`, so the guard is a no-op there (the rpath fallback
> to `/opt/homebrew/lib` covers it), and the check fails open on any binary it
> can't inspect — it never blocks a legitimate update. The durable fix, when
> in scope, is to move macOS self-update to fetch the tarball and replace the
> binary + `lib/` in lockstep rather than swapping the bare binary alone.

## What is NOT signed

- **Linux** AppImage / Flatpak — Linux uses repo/distro trust, not per-binary
  Authenticode-style signing; nothing to configure.
- **Local dev builds** (`scripts\build.ps1`) — unsigned by design.
