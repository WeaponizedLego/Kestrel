#!/usr/bin/env bash
# Wraps a built linux/amd64 kestrel binary in an AppImage.
#
# Usage: scripts/package-linux.sh <binary> <out-dir> [--icon path/to/icon.png]
#
# appimagetool is downloaded into <out-dir>/tools/ on first run and reused.
# Requires FUSE on the build host (works on GitHub's ubuntu-latest runner).
#
# Output: <out-dir>/Kestrel-x86_64.AppImage
set -euo pipefail

if [[ $# -lt 2 ]]; then
    echo "usage: $0 <binary> <out-dir> [--icon path]" >&2
    exit 2
fi

BINARY="$1"
OUT_DIR="$2"
shift 2

ICON_SRC=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        --icon) ICON_SRC="$2"; shift 2 ;;
        *) echo "unknown flag: $1" >&2; exit 2 ;;
    esac
done

if [[ ! -f "$BINARY" ]]; then
    echo "binary not found: $BINARY" >&2
    exit 1
fi

mkdir -p "$OUT_DIR/tools"
APPIMAGETOOL="$OUT_DIR/tools/appimagetool"
if [[ ! -x "$APPIMAGETOOL" ]]; then
    echo "fetching appimagetool..."
    curl -L --fail --output "$APPIMAGETOOL" \
        https://github.com/AppImage/AppImageKit/releases/download/continuous/appimagetool-x86_64.AppImage
    chmod +x "$APPIMAGETOOL"
fi

APPDIR="$OUT_DIR/Kestrel.AppDir"
rm -rf "$APPDIR"
mkdir -p "$APPDIR/usr/bin"

cp "$BINARY" "$APPDIR/usr/bin/kestrel"
chmod +x "$APPDIR/usr/bin/kestrel"

ln -sf usr/bin/kestrel "$APPDIR/AppRun"

cat > "$APPDIR/kestrel.desktop" <<'DESKTOP'
[Desktop Entry]
Type=Application
Name=Kestrel
Comment=Photo manager for very large libraries
Exec=kestrel
Icon=kestrel
Categories=Graphics;Photography;
Terminal=false
DESKTOP

# AppImage requires an icon at AppDir root. If the caller didn't supply one,
# generate a 1x1 transparent PNG so appimagetool stops complaining; the
# desktop file still references "kestrel" so a real icon dropped later just
# works.
if [[ -n "$ICON_SRC" && -f "$ICON_SRC" ]]; then
    cp "$ICON_SRC" "$APPDIR/kestrel.png"
else
    # 1x1 transparent PNG, base64-encoded.
    base64 -d > "$APPDIR/kestrel.png" <<'PNG'
iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=
PNG
fi

OUT_FILE="$OUT_DIR/Kestrel-x86_64.AppImage"
rm -f "$OUT_FILE"
ARCH=x86_64 "$APPIMAGETOOL" "$APPDIR" "$OUT_FILE"

echo "built: $OUT_FILE"
