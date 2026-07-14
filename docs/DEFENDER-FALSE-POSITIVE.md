# Windows Defender false positive (`Trojan:Win32/Bearfoos.A!ml`)

## What happened

Windows Defender flagged an AsyncAO release binary as
`Trojan:Win32/Bearfoos.A!ml`. The `!ml` suffix means it is a **low-confidence
cloud machine-learning verdict**, not a signature match — Defender's model
scored an unsigned, unfamiliar binary as *probably* suspicious. It is a false
positive: AsyncAO is AGPL-3.0 free software with public source.

## Why AsyncAO trips it

Each of these is a legitimate feature that also happens to be a heuristic signal:

- **Unsigned binary.** Releases ship unsigned by default (code signing is
  opt-in — see `docs/CODE-SIGNING.md`), so there is no publisher reputation and
  no Authenticode trust to offset the ML score.
- **HDID hardware-ID registry query.** AO2 requires a per-machine hardware ID;
  we derive it from a registry read (`MachineGuid`). Reading machine identifiers
  is a behaviour droppers also exhibit.
- **Self-update.** The client downloads a release asset and replaces its own
  running executable — "a program that rewrites its own binary" is textbook
  malware shape, even though ours is a bounded, checksum-verified (when the
  release ships a `SHA256SUMS.txt`), rename-with-rollback swap over HTTPS
  (GitHub's asset URLs).
- **Hidden PowerShell.** Desktop toast notifications shell out to
  `powershell -WindowStyle Hidden -EncodedCommand <base64>` (Unicode-safe
  transport of user shownames — see `internal/ui/ostoast.go`). "Hidden
  PowerShell running an encoded command" is a top dropper heuristic.

None of these is going away — they are core to the client. The goal is to lower
the *provenance* surface (make the binary look like what it is) and to build
signing reputation.

## What changed this milestone

Provenance and verification fixes that shrink the false-positive surface without
touching HDID, self-update, or the hidden-PowerShell mechanism:

- **Checksum-verified self-update.** The release workflow now publishes a
  `SHA256SUMS.txt` asset; the in-app updater fetches it and verifies the
  download against the published digest before the swap, aborting on mismatch
  (`internal/update`, `internal/ui/update_ui.go`). Releases cut before this
  proceed unverified as before.
- **Windows VERSIONINFO resource.** The `.exe` now carries real
  CompanyName / ProductName / FileDescription / LegalCopyright metadata
  (`cmd/asyncao/versioninfo_windows.syso`) instead of blank publisher fields.
- **Documented the hidden-PowerShell toast** as a deliberate, injection-safe
  Unicode-transport necessity (not obfuscation) at the exec site.

The one thing that actually clears an ML verdict for good is **code signing**;
everything above is groundwork that makes signing reputation accrue faster.

## Maintainer checklist (non-code)

1. **Submit the flagged binary as a false positive.** Send the *exact* flagged
   `.exe` to Microsoft as an incorrectly-detected file:
   <https://www.microsoft.com/en-us/wdsi/filesubmission>. Cross-check the same
   binary on <https://www.virustotal.com/> first — a detection that is
   **heuristic/ML-only** (few or no signature-based engines agreeing) is
   evidence for the submission that it is a false positive, so include the
   VirusTotal link and note the `!ml` low-confidence suffix.

2. **Get a code-signing certificate and turn signing on.** Set the two secrets
   documented in `docs/CODE-SIGNING.md`. The OV-vs-EV tradeoff described there:
   - **OV** (Organisation Validation) — cheap, drops straight into the existing
     PFX-secret flow (`WINDOWS_CODESIGN_PFX_BASE64` / `_PASSWORD`), and
     SmartScreen/Defender reputation accrues over downloads and time.
   - **EV / Azure Trusted Signing** — clears SmartScreen instantly, but the key
     lives on an HSM/token and does **not** fit the PFX-secret flow (needs the
     CA's cloud tool). Only worth it if instant clearance matters more than
     fitting the current pipeline.

3. **Re-submit after each signed release.** Reputation is per-signing-identity
   and accrues per download; keep submitting the first few signed builds until
   the verdict stops recurring.

4. **Do NOT recommend end-user Defender exclusions.** Telling users to exclude
   the binary or a folder is a security anti-pattern and trains bad habits — fix
   provenance and signing instead. A narrow *build-output-directory* exclusion
   (e.g. `bin\`) on a **developer's own machine** is a local convenience only,
   never guidance shipped to users.

See also `docs/CODE-SIGNING.md` for the signing setup this checklist references.
