package main

// versioninfo_windows.syso embeds the Windows VERSIONINFO resource; the Go toolchain links any *.syso in
// package main, and it merges with rsrc_windows.syso because the resource types are disjoint.
// Blank publisher metadata feeds the heuristics behind the Defender false positive (docs/DEFENDER-FALSE-POSITIVE.md).
// The numbers are static in versioninfo.json because the syso is compiled before the link stamp; the release
// workflow regenerates it with the real tag. To regenerate (build-time tool, not in go.mod):
// go run github.com/josephspurrier/goversioninfo/cmd/goversioninfo@v1.4.0 -64 \
// -o cmd/asyncao/versioninfo_windows.syso cmd/asyncao/versioninfo.json
// Keep @v1.4.0 in step with release.yml. -64 is required: otherwise the syso is 32-bit and won't link on amd64.
