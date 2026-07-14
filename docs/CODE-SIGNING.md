# Code signing (Windows SmartScreen + macOS notarization)

The release pipeline (`.github/workflows/release.yml`) signs the Windows `.exe`
and codesigns/notarizes the macOS binary **when the signing secrets are present**.

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
three to also notarize. We ship a **bare binary**, which can't be *stapled*
(stapling needs an `.app`/`.dmg`/`.pkg`), so Gatekeeper verifies notarization
online on first run. Packaging a `.dmg` for offline stapling is a future step.

## What is NOT signed

- **Linux** AppImage / Flatpak — Linux uses repo/distro trust, not per-binary
  Authenticode-style signing; nothing to configure.
- **Local dev builds** (`scripts\build.ps1`) — unsigned by design.
