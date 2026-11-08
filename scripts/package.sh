#!/usr/bin/env bash
# The one build path for CI, releases and local packaging. The platform comes
# from the machine this runs on; Windows runs it in an MSYS2 UCRT64 shell.
#
# Usage: scripts/package.sh build|pack|smoke [version]
#   build  compile and collect runtime libraries into dist/stage; signing runs after this
#   pack   write the release assets to dist/assets
#   smoke  unpack the shipped asset and run it with -version
#
# Asset names are a contract with the self-updater: packaging/release-assets.txt.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

COMMAND="${1:?usage: package.sh build|pack|smoke [version]}"
VERSION="${2:-dev}"

STAGE=dist/stage
ASSETS=dist/assets
SMOKE=dist/smoke
VERSION_VAR=github.com/SyntaxNyah/AsyncAO/internal/update.Version
BUILD_TIME_VAR=github.com/SyntaxNyah/AsyncAO/internal/update.BuildTime

case "$(uname -s)" in
MINGW* | MSYS* | CYGWIN*) PLATFORM=windows ;;
Linux) PLATFORM=linux ;;
Darwin) PLATFORM=macos ;;
*)
	echo "unsupported platform: $(uname -s)" >&2
	exit 2
	;;
esac

MAC_BINARY=asyncao-macos-arm64

go_build() { # output extra-ldflags
	CGO_ENABLED=1 go build -pgo=auto -trimpath \
		-ldflags "-s -w -X $VERSION_VAR=$VERSION -X $BUILD_TIME_VAR=$(date -u +%Y-%m-%dT%H:%M:%SZ) $2" -o "$1" ./cmd/asyncao
}

# The syso is committed with a baseline version, so a non-numeric version keeps it.
regenerate_windows_versioninfo() {
	local core="${VERSION#v}" major minor patch
	core="${core%%-*}"
	IFS=. read -r major minor patch _ <<<"$core"
	if ! [[ "$major" =~ ^[0-9]+$ && "$minor" =~ ^[0-9]+$ && "$patch" =~ ^[0-9]+$ ]]; then
		return 0
	fi
	local generated=dist/versioninfo.tag.json
	sed -e "s/\"Major\": [0-9]*/\"Major\": $major/g" \
		-e "s/\"Minor\": [0-9]*/\"Minor\": $minor/g" \
		-e "s/\"Patch\": [0-9]*/\"Patch\": $patch/g" \
		-e "s/\"FileVersion\": \"[0-9.]*\"/\"FileVersion\": \"$major.$minor.$patch.0\"/g" \
		-e "s/\"ProductVersion\": \"[0-9.]*\"/\"ProductVersion\": \"$major.$minor.$patch.0\"/g" \
		cmd/asyncao/versioninfo.json >"$generated"
	go run github.com/josephspurrier/goversioninfo/cmd/goversioninfo@v1.4.0 \
		-64 -o cmd/asyncao/versioninfo_windows.syso "$generated"
}

# git checks text out as CRLF on Windows, and a stray carriage return makes a name that doesn't exist.
on_demand_dlls() {
	tr -d '\r' <packaging/windows/on-demand-dlls.txt
}

build_windows() {
	local syso=cmd/asyncao/versioninfo_windows.syso
	cp "$syso" dist/versioninfo.syso.committed
	regenerate_windows_versioninfo
	go_build "$STAGE/asyncao.exe" "-H=windowsgui"
	mv -f dist/versioninfo.syso.committed "$syso"

	# Libraries loaded on demand appear in no import table, so they are seeded by hand.
	local on_demand=() name
	while read -r name; do
		on_demand+=("/ucrt64/bin/$name")
	done < <(on_demand_dlls)
	{
		ldd "$STAGE/asyncao.exe"
		for name in "${on_demand[@]}"; do
			echo "$name"
			ldd "$name"
		done
	} | grep -io '/ucrt64/bin/[a-z0-9_.+-]*\.dll' | sort -u | while read -r name; do
		cp "$name" "$STAGE/"
	done
	# msys2's lavapipe manifest points into its own tree.
	sed 's|"library_path": *"[^"]*"|"library_path": ".\\\\vulkan_lvp.dll"|' \
		/ucrt64/share/vulkan/icd.d/lvp_icd.x86_64.json >"$STAGE/lvp_icd.x86_64.json"
}

build_linux() {
	go_build "$STAGE/asyncao" ""
}

build_macos() {
	# Info.plist gives the bare binary a 2x Retina backing store without a .app.
	go_build "dist/$MAC_BINARY" "-extldflags=-Wl,-sectcreate,__TEXT,__info_plist,$PWD/packaging/macos/Info.plist"
	bash scripts/bundle-macos.sh "dist/$MAC_BINARY" "$STAGE"
}

pack_windows() {
	local bundle=asyncao-windows-x86_64-bundle.zip
	# The updater needs a flat zip; no stage entry has a subfolder.
	powershell.exe -NoProfile -NonInteractive -Command \
		"Compress-Archive -Path '$(cygpath -w "$STAGE")\\*' -DestinationPath '$(cygpath -w "$ASSETS/$bundle")' -Force"
}

pack_linux() {
	local output=AsyncAO-linux-x86_64.AppImage
	APPIMAGE_OUTPUT="$output" bash scripts/build-appimage.sh "$STAGE/asyncao"
	mv -f "dist/$output" "$ASSETS/$output"
}

pack_macos() {
	local bundle=asyncao-macos-bundle-arm64.tar.gz
	local homebrew=asyncao-macos-homebrew-arm64.tar.gz
	local brew_stage=dist/brew-stage
	cp packaging/macos/INSTALL-bundle.txt "$STAGE/INSTALL.txt"
	tar -czf "$ASSETS/$bundle" -C "$STAGE" .
	# Homebrew edition: no lib/; the binary finds its libraries through the rpath fallback.
	rm -rf "$brew_stage"
	mkdir -p "$brew_stage"
	cp "$STAGE/$MAC_BINARY" "$brew_stage/"
	cp packaging/macos/INSTALL-homebrew.txt "$brew_stage/INSTALL.txt"
	tar -czf "$ASSETS/$homebrew" -C "$brew_stage" .
}

# PATH and LD_LIBRARY_PATH are cleared so only what the asset bundles can satisfy its imports.
smoke_windows() {
	local bundle=asyncao-windows-x86_64-bundle.zip name
	powershell.exe -NoProfile -NonInteractive -Command \
		"Expand-Archive -Path '$(cygpath -w "$ASSETS/$bundle")' -DestinationPath '$(cygpath -w "$SMOKE")' -Force"
	while read -r name; do
		test -f "$SMOKE/$name"
	done < <(on_demand_dlls)
	test -f "$SMOKE/lvp_icd.x86_64.json"
	check_version "$(env PATH=/usr/bin "$SMOKE/asyncao.exe" -version)"
}

smoke_linux() {
	local image="$PWD/$ASSETS/AsyncAO-linux-x86_64.AppImage"
	check_version "$(env -u LD_LIBRARY_PATH APPIMAGE_EXTRACT_AND_RUN=1 "$image" -version)"
}

smoke_macos() {
	tar -xzf "$ASSETS/asyncao-macos-bundle-arm64.tar.gz" -C "$SMOKE"
	check_version "$("$SMOKE/$MAC_BINARY" -version)"
}

check_version() {
	if [[ "${1%$'\r'}" != "$VERSION" ]]; then
		echo "smoke test: binary reports '$1', expected '$VERSION'" >&2
		exit 1
	fi
	echo "smoke test: $PLATFORM reports $VERSION"
}

case "$COMMAND" in
build)
	rm -rf dist/stage dist/assets
	mkdir -p "$STAGE" "$ASSETS"
	"build_$PLATFORM"
	;;
pack)
	"pack_$PLATFORM"
	ls -l "$ASSETS"
	;;
smoke)
	rm -rf "$SMOKE"
	mkdir -p "$SMOKE"
	"smoke_$PLATFORM"
	;;
*)
	echo "unknown command: $COMMAND" >&2
	exit 2
	;;
esac
