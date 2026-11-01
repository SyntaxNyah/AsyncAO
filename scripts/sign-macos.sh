#!/usr/bin/env bash
# Developer ID codesign and notarize. Skips with no certificate, because
# bundle-macos.sh already ad-hoc signed everything; fails if a certificate exists
# and signing breaks. See docs/CODE-SIGNING.md for the secrets.
set -euo pipefail

BINARY=asyncao-macos-arm64
BARE="dist/assets/$BINARY"
STAGE=dist/stage

if [[ -z "${APPLE_P12_BASE64:-}" ]]; then
	echo "::notice::No APPLE_CODESIGN_P12_BASE64 secret: shipping an ad-hoc-signed, unnotarized macOS build."
	exit 0
fi

keychain="$RUNNER_TEMP/asyncao.keychain-db"
keychain_password="$(uuidgen)"
trap 'security delete-keychain "$keychain" || true; rm -f "$RUNNER_TEMP/cert.p12"' EXIT
security create-keychain -p "$keychain_password" "$keychain"
security set-keychain-settings -lut 21600 "$keychain"
security unlock-keychain -p "$keychain_password" "$keychain"
base64 --decode <<<"$APPLE_P12_BASE64" >"$RUNNER_TEMP/cert.p12"
security import "$RUNNER_TEMP/cert.p12" -k "$keychain" -P "$APPLE_P12_PASSWORD" -T /usr/bin/codesign
security set-key-partition-list -S apple-tool:,apple: -k "$keychain_password" "$keychain"
# shellcheck disable=SC2046 # one word per keychain
security list-keychains -d user -s "$keychain" $(security list-keychains -d user | tr -d '"')

sign() {
	codesign --force --options runtime --timestamp --sign "$APPLE_IDENTITY" "$1"
}

# Notarization requires nested code to be signed before the code that loads it.
for dylib in "$STAGE"/lib/*.dylib; do
	sign "$dylib"
done
for binary in "$STAGE/$BINARY" "$BARE"; do
	sign "$binary"
	codesign --verify --strict --verbose=2 "$binary"
done

if [[ -z "${APPLE_ID:-}" || -z "${APPLE_ID_PASSWORD:-}" || -z "${APPLE_TEAM_ID:-}" ]]; then
	echo "::notice::Codesigned but not notarized: no APPLE_NOTARY_* secrets."
	exit 0
fi
# A bare binary can't be stapled, so Gatekeeper verifies online on first run.
ditto -c -k --keepParent "$STAGE" "$RUNNER_TEMP/notarize.zip"
xcrun notarytool submit "$RUNNER_TEMP/notarize.zip" \
	--apple-id "$APPLE_ID" --password "$APPLE_ID_PASSWORD" --team-id "$APPLE_TEAM_ID" --wait
