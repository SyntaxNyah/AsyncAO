package main

// Windows version resource (RT_VERSION).
//
// versioninfo_windows.syso in this directory embeds a Windows VERSIONINFO
// resource — the CompanyName / ProductName / FileDescription / LegalCopyright /
// FileVersion fields Explorer shows on the .exe's Properties → Details tab. The
// Go toolchain automatically links any *.syso it finds in the main package on
// Windows, so this rides in with no build flag, ALONGSIDE the icon syso
// (rsrc_windows.syso): the two carry disjoint resource types (RT_VERSION vs
// RT_GROUP_ICON/RT_ICON) and merge cleanly, so both the icon and the version
// details survive in the linked exe.
//
// Why it exists: an unsigned Go binary with no VERSIONINFO ("blank" publisher
// metadata) is a small but real signal in the heuristics that produced the
// Windows Defender Bearfoos.A!ml false positive (see
// docs/DEFENDER-FALSE-POSITIVE.md). Real provenance metadata lowers that
// surface at zero runtime cost.
//
// The version numbers are baked into versioninfo.json STATICALLY (1.70.0.0),
// NOT sourced from the link-time -X update.Version stamp — the two live in
// different build phases (the syso is compiled before the Go link step). The
// release workflow regenerates the syso with the real tag version before build
// (best-effort; it falls back to this committed 1.70.0.0 syso on any failure).
//
// To regenerate after a version bump or a field change (needs Go; the tool is
// build-time only, NOT a linked dependency — it never enters go.mod):
//
//	go run github.com/josephspurrier/goversioninfo/cmd/goversioninfo@v1.4.0 \
//	  -64 -o cmd/asyncao/versioninfo_windows.syso cmd/asyncao/versioninfo.json
//
// (@v1.4.0 matches the pin in .github/workflows/release.yml — keep them in
// lockstep; the workflow deliberately never uses @latest.)
//
// The -64 flag is REQUIRED: it emits an amd64 COFF object; without it the syso
// is 32-bit and the amd64 `go build ./cmd/asyncao` fails to link it. Both
// versioninfo.json and versioninfo_windows.syso are committed so a normal build
// needs no extra tooling.
