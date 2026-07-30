#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
DIST_DIR="$SCRIPT_DIR/dist"
TARGET_ARCH=${1:-}
GO_BINARY=${GO_CMD:-go}
APPIMAGETOOL=${APPIMAGETOOL:-}
APPIMAGE_RUNTIME=${APPIMAGE_RUNTIME:-}
VERSION=$(tr -d '\r\n' < "$SCRIPT_DIR/internal/version/VERSION")
RELEASE_DATE=$(git -C "$SCRIPT_DIR" show -s --format=%cs HEAD)
SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH:-$(git -C "$SCRIPT_DIR" show -s --format=%ct HEAD)}

case "$TARGET_ARCH" in
  amd64) APPIMAGE_ARCH=x86_64 ;;
  arm64) APPIMAGE_ARCH=aarch64 ;;
  *) printf '%s\n' 'Usage: build-linux-appimage.sh <amd64|arm64>' >&2; exit 1 ;;
esac

[ "$(uname -s)" = "Linux" ] || { printf '%s\n' 'AppImages must be built on Linux.' >&2; exit 1; }
command -v "$GO_BINARY" >/dev/null 2>&1 || { printf '%s\n' 'Go is required.' >&2; exit 1; }
[ -x "$APPIMAGETOOL" ] || { printf '%s\n' 'APPIMAGETOOL must name the reviewed executable.' >&2; exit 1; }
[ -f "$APPIMAGE_RUNTIME" ] || { printf '%s\n' 'APPIMAGE_RUNTIME must name the reviewed runtime.' >&2; exit 1; }

mkdir -p "$DIST_DIR"
build() {
  package=$1
  output=$2
  linker_flags=$3
  (cd "$SCRIPT_DIR" && CGO_ENABLED=0 GOOS=linux GOARCH="$TARGET_ARCH" "$GO_BINARY" build \
    -mod=vendor -trimpath -ldflags="$linker_flags" -o "$output" "$package")
}
build . "$DIST_DIR/unidrop-linux-$TARGET_ARCH" "-s -w -X main.appVersion=$VERSION"
build ./cmd/unidrop-tray "$DIST_DIR/unidrop-tray-linux-$TARGET_ARCH" "-s -w -X main.appVersion=$VERSION"
build ./cmd/unidrop-update "$DIST_DIR/unidrop-update-linux-$TARGET_ARCH" "-s -w"

WORK_DIR=$(mktemp -d "${TMPDIR:-/tmp}/unidrop-appimage.XXXXXX")
trap 'rm -rf "$WORK_DIR"' EXIT HUP INT TERM
APP_DIR="$WORK_DIR/UniDrop.AppDir"
mkdir -p \
  "$APP_DIR/usr/bin" \
  "$APP_DIR/usr/share/applications" \
  "$APP_DIR/usr/share/icons/hicolor/scalable/apps" \
  "$APP_DIR/usr/share/metainfo" \
  "$APP_DIR/usr/share/doc/unidrop"

cp "$DIST_DIR/unidrop-linux-$TARGET_ARCH" "$APP_DIR/usr/bin/unidrop"
cp "$DIST_DIR/unidrop-tray-linux-$TARGET_ARCH" "$APP_DIR/usr/bin/unidrop-tray"
cp "$DIST_DIR/unidrop-update-linux-$TARGET_ARCH" "$APP_DIR/usr/bin/unidrop-update"
cp "$SCRIPT_DIR/linux/AppRun" "$APP_DIR/AppRun"
cp "$SCRIPT_DIR/linux/unidrop.desktop" "$APP_DIR/usr/share/applications/unidrop.desktop"
cp "$SCRIPT_DIR/linux/unidrop.svg" "$APP_DIR/usr/share/icons/hicolor/scalable/apps/unidrop.svg"
cp "$SCRIPT_DIR/linux/AppImage-runtime-LICENSE" "$APP_DIR/usr/share/doc/unidrop/AppImage-runtime-LICENSE"
sed -e "s/__VERSION__/$VERSION/g" -e "s/__RELEASE_DATE__/$RELEASE_DATE/g" \
  "$SCRIPT_DIR/linux/com.unidrop.app.metainfo.xml" > "$APP_DIR/usr/share/metainfo/com.unidrop.app.metainfo.xml"
chmod 755 "$APP_DIR/AppRun" "$APP_DIR/usr/bin/unidrop" "$APP_DIR/usr/bin/unidrop-tray" "$APP_DIR/usr/bin/unidrop-update"
ln -s usr/share/applications/unidrop.desktop "$APP_DIR/unidrop.desktop"
ln -s usr/share/icons/hicolor/scalable/apps/unidrop.svg "$APP_DIR/unidrop.svg"
ln -s unidrop.svg "$APP_DIR/.DirIcon"

if command -v desktop-file-validate >/dev/null 2>&1; then
  desktop-file-validate "$APP_DIR/usr/share/applications/unidrop.desktop"
fi
if command -v appstreamcli >/dev/null 2>&1; then
  appstreamcli validate --no-net "$APP_DIR/usr/share/metainfo/com.unidrop.app.metainfo.xml"
fi

OUTPUT="$DIST_DIR/unidrop-$VERSION-linux-$TARGET_ARCH.AppImage"
rm -f "$OUTPUT"
APPIMAGE_EXTRACT_AND_RUN=1 ARCH="$APPIMAGE_ARCH" VERSION="$VERSION" SOURCE_DATE_EPOCH="$SOURCE_DATE_EPOCH" \
  "$APPIMAGETOOL" --runtime-file "$APPIMAGE_RUNTIME" "$APP_DIR" "$OUTPUT"
chmod 755 "$OUTPUT"

APPIMAGE_EXTRACT_AND_RUN=1 "$OUTPUT" --version | grep -F "UniDrop $VERSION" >/dev/null
EXTRACT_DIR="$WORK_DIR/extracted"
mkdir -p "$EXTRACT_DIR"
(cd "$EXTRACT_DIR" && "$OUTPUT" --appimage-extract >/dev/null)
test -x "$EXTRACT_DIR/squashfs-root/AppRun"
test -x "$EXTRACT_DIR/squashfs-root/usr/bin/unidrop"
test -x "$EXTRACT_DIR/squashfs-root/usr/bin/unidrop-tray"
test -x "$EXTRACT_DIR/squashfs-root/usr/bin/unidrop-update"
test -f "$EXTRACT_DIR/squashfs-root/usr/share/metainfo/com.unidrop.app.metainfo.xml"
test -f "$EXTRACT_DIR/squashfs-root/usr/share/doc/unidrop/AppImage-runtime-LICENSE"
printf 'Created and verified %s\n' "$OUTPUT"
