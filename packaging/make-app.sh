#!/usr/bin/env bash
# Assemble the macOS "K8s Port Forwards.app" bundle and zip it for distribution.
#
# Used both locally (`task bundle`) and by the release workflow, so the bundle
# CI ships is byte-for-byte the same shape you can build by hand. macOS only —
# it relies on sips/iconutil (icns generation) and ditto (bundle-safe zipping).
#
# Usage:
#   packaging/make-app.sh [--bin PATH] [--version VERSION] [--out DIR] [--icon PATH]
#
#   --bin      Prebuilt executable to embed. If omitted, `go build` is run for
#              the host architecture (the dev/local path).
#   --version  Marketing version for Info.plist / the zip name. Defaults to
#              `git describe`, else "dev".
#   --out      Directory to write "<APP_NAME>.app" and the zip into. Default: ".".
#   --icon     1024x1024 PNG to render into the icon. Default: packaging/icon.png.
set -euo pipefail

APP_NAME="K8s Port Forwards"
EXECUTABLE="k8s-tray-forwarder"
BUNDLE_ID="com.github.dawidlaszuk.k8s-tray-forwarder"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

BIN=""
VERSION=""
OUT_DIR="."
ICON_PNG="$SCRIPT_DIR/icon.png"

while [[ $# -gt 0 ]]; do
	case "$1" in
	--bin) BIN="$2"; shift 2 ;;
	--version) VERSION="$2"; shift 2 ;;
	--out) OUT_DIR="$2"; shift 2 ;;
	--icon) ICON_PNG="$2"; shift 2 ;;
	*) echo "make-app.sh: unknown argument: $1" >&2; exit 2 ;;
	esac
done

if [[ "$(uname -s)" != "Darwin" ]]; then
	echo "make-app.sh: macOS only (needs sips/iconutil/ditto)" >&2
	exit 1
fi

if [[ -z "$VERSION" ]]; then
	VERSION="$(git -C "$REPO_ROOT" describe --tags --always 2>/dev/null || echo dev)"
fi
VERSION="${VERSION#v}" # normalise a leading v (v0.1.0 -> 0.1.0)

# Build the binary for the host arch if the caller didn't hand us one. Build into
# a throwaway dir rather than the repo root so we never clobber a checked-out
# ./k8s-tray-forwarder.
if [[ -z "$BIN" ]]; then
	BUILD_DIR="$(mktemp -d)"
	trap 'rm -rf "$BUILD_DIR"' EXIT
	BIN="$BUILD_DIR/$EXECUTABLE"
	echo "make-app.sh: building $EXECUTABLE (version $VERSION)"
	( cd "$REPO_ROOT" && go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$BIN" . )
fi

if [[ ! -x "$BIN" ]]; then
	echo "make-app.sh: binary not found or not executable: $BIN" >&2
	exit 1
fi
if [[ ! -f "$ICON_PNG" ]]; then
	echo "make-app.sh: icon not found: $ICON_PNG" >&2
	exit 1
fi

mkdir -p "$OUT_DIR"
OUT_DIR="$(cd "$OUT_DIR" && pwd)"
APP_DIR="$OUT_DIR/$APP_NAME.app"
CONTENTS="$APP_DIR/Contents"

echo "make-app.sh: assembling $APP_DIR"
rm -rf "$APP_DIR"
mkdir -p "$CONTENTS/MacOS" "$CONTENTS/Resources"

cp "$BIN" "$CONTENTS/MacOS/$EXECUTABLE"
chmod +x "$CONTENTS/MacOS/$EXECUTABLE"

# Render the .icns from the source PNG. iconutil consumes a .iconset directory
# of the standard sizes; sips scales each one.
ICONSET="$(mktemp -d)/icon.iconset"
mkdir -p "$ICONSET"
for sz in 16 32 64 128 256 512; do
	sips -z "$sz" "$sz" "$ICON_PNG" --out "$ICONSET/icon_${sz}x${sz}.png" >/dev/null
	sips -z "$((sz * 2))" "$((sz * 2))" "$ICON_PNG" --out "$ICONSET/icon_${sz}x${sz}@2x.png" >/dev/null
done
iconutil -c icns "$ICONSET" -o "$CONTENTS/Resources/icon.icns"
rm -rf "$(dirname "$ICONSET")"

cat >"$CONTENTS/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key>
	<string>$APP_NAME</string>
	<key>CFBundleDisplayName</key>
	<string>$APP_NAME</string>
	<key>CFBundleExecutable</key>
	<string>$EXECUTABLE</string>
	<key>CFBundleIdentifier</key>
	<string>$BUNDLE_ID</string>
	<key>CFBundleIconFile</key>
	<string>icon</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleInfoDictionaryVersion</key>
	<string>6.0</string>
	<key>CFBundleShortVersionString</key>
	<string>$VERSION</string>
	<key>CFBundleVersion</key>
	<string>$VERSION</string>
	<key>LSMinimumSystemVersion</key>
	<string>10.15</string>
	<key>LSApplicationCategoryType</key>
	<string>public.app-category.developer-tools</string>
	<key>NSHighResolutionCapable</key>
	<true/>
</dict>
</plist>
PLIST

# PkgInfo is optional but conventional for a well-formed bundle.
printf 'APPL????' >"$CONTENTS/PkgInfo"

# ditto is the canonical way to zip a bundle: it preserves the executable bit,
# symlinks and resource forks that a plain `zip` can mangle.
ZIP_NAME="K8s-Port-Forwards_${VERSION}.zip"
ZIP_PATH="$OUT_DIR/$ZIP_NAME"
rm -f "$ZIP_PATH"
( cd "$OUT_DIR" && ditto -c -k --sequesterRsrc --keepParent "$APP_NAME.app" "$ZIP_NAME" )

echo "make-app.sh: built $APP_DIR"
echo "make-app.sh: zipped $ZIP_PATH"
