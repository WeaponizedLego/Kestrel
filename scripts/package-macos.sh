#!/usr/bin/env bash
# Wraps a built kestrel binary in a macOS .app bundle and zips it.
#
# Usage: scripts/package-macos.sh <binary> <out-dir> [--icon path/to/icon.png] [--version X.Y.Z]
#
# The icon is optional — without it the bundle uses the generic Finder app
# icon. Provide a 1024x1024 PNG and the script will produce an .icns at the
# right size ladder via sips + iconutil. Both tools ship with macOS, so no
# extra install is required.
#
# Output: <out-dir>/Kestrel.app and <out-dir>/Kestrel-macos-<arch>.zip
set -euo pipefail

if [[ $# -lt 2 ]]; then
    echo "usage: $0 <binary> <out-dir> [--icon path] [--version X.Y.Z]" >&2
    exit 2
fi

BINARY="$1"
OUT_DIR="$2"
shift 2

ICON_SRC=""
VERSION=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        --icon) ICON_SRC="$2"; shift 2 ;;
        --version) VERSION="$2"; shift 2 ;;
        *) echo "unknown flag: $1" >&2; exit 2 ;;
    esac
done

if [[ ! -x "$BINARY" ]]; then
    echo "binary not found or not executable: $BINARY" >&2
    exit 1
fi

if [[ -z "$VERSION" ]]; then
    VERSION="$(git -C "$(dirname "$0")/.." describe --tags --always --dirty 2>/dev/null || echo "0.0.0")"
fi

ARCH="$(uname -m)"
case "$ARCH" in
    arm64)  BUNDLE_ARCH="arm64" ;;
    x86_64) BUNDLE_ARCH="amd64" ;;
    *)      BUNDLE_ARCH="$ARCH" ;;
esac

mkdir -p "$OUT_DIR"
APP_DIR="$OUT_DIR/Kestrel.app"
rm -rf "$APP_DIR"
mkdir -p "$APP_DIR/Contents/MacOS" "$APP_DIR/Contents/Resources"

cp "$BINARY" "$APP_DIR/Contents/MacOS/kestrel"
chmod +x "$APP_DIR/Contents/MacOS/kestrel"

ICON_PLIST_ENTRY=""
if [[ -n "$ICON_SRC" && -f "$ICON_SRC" ]]; then
    ICONSET="$(mktemp -d)/Kestrel.iconset"
    mkdir -p "$ICONSET"
    for spec in \
        "16 icon_16x16.png" \
        "32 icon_16x16@2x.png" \
        "32 icon_32x32.png" \
        "64 icon_32x32@2x.png" \
        "128 icon_128x128.png" \
        "256 icon_128x128@2x.png" \
        "256 icon_256x256.png" \
        "512 icon_256x256@2x.png" \
        "512 icon_512x512.png" \
        "1024 icon_512x512@2x.png"
    do
        size="${spec%% *}"
        name="${spec#* }"
        sips -z "$size" "$size" "$ICON_SRC" --out "$ICONSET/$name" >/dev/null
    done
    iconutil -c icns "$ICONSET" -o "$APP_DIR/Contents/Resources/Kestrel.icns"
    ICON_PLIST_ENTRY="<key>CFBundleIconFile</key><string>Kestrel</string>"
elif [[ -n "$ICON_SRC" ]]; then
    echo "icon path given but file missing: $ICON_SRC" >&2
    exit 1
fi

cat > "$APP_DIR/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleDevelopmentRegion</key><string>en</string>
    <key>CFBundleExecutable</key><string>kestrel</string>
    <key>CFBundleIdentifier</key><string>com.weaponizedlego.kestrel</string>
    <key>CFBundleName</key><string>Kestrel</string>
    <key>CFBundleDisplayName</key><string>Kestrel</string>
    <key>CFBundlePackageType</key><string>APPL</string>
    <key>CFBundleShortVersionString</key><string>${VERSION}</string>
    <key>CFBundleVersion</key><string>${VERSION}</string>
    <key>LSMinimumSystemVersion</key><string>11.0</string>
    <key>NSHighResolutionCapable</key><true/>
    ${ICON_PLIST_ENTRY}
</dict>
</plist>
PLIST

ZIP_NAME="Kestrel-macos-${BUNDLE_ARCH}.zip"
( cd "$OUT_DIR" && rm -f "$ZIP_NAME" && ditto -c -k --keepParent Kestrel.app "$ZIP_NAME" )

echo "built: $APP_DIR"
echo "built: $OUT_DIR/$ZIP_NAME"
