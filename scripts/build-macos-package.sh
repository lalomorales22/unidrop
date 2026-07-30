#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
DIST_DIR="$SCRIPT_DIR/dist"
VERSION=$(tr -d '\r\n' < "$SCRIPT_DIR/internal/version/VERSION")
WORK_DIR=$(mktemp -d "${TMPDIR:-/tmp}/unidrop-macos-package.XXXXXX")
MOUNT_DIR="$WORK_DIR/mount"
MOUNTED=0

cleanup() {
  if [ "$MOUNTED" = "1" ]; then
    hdiutil detach -force "$MOUNT_DIR" >/dev/null 2>&1 || :
  fi
  rm -rf "$WORK_DIR"
}
trap cleanup EXIT HUP INT TERM

[ "$(uname -s)" = "Darwin" ] || { printf '%s\n' 'The macOS package must be built on macOS.' >&2; exit 1; }
for tool in lipo codesign hdiutil plutil; do
  command -v "$tool" >/dev/null 2>&1 || { printf '%s\n' "Missing required macOS tool: $tool" >&2; exit 1; }
done

required_files="
$DIST_DIR/unidrop-darwin-amd64
$DIST_DIR/unidrop-darwin-arm64
$DIST_DIR/unidrop-menu-darwin-amd64
$DIST_DIR/unidrop-menu-darwin-arm64
$DIST_DIR/unidrop-update-darwin-amd64
$DIST_DIR/unidrop-update-darwin-arm64
$SCRIPT_DIR/macos/Info.plist
$SCRIPT_DIR/macos/UniDrop.entitlements
$SCRIPT_DIR/macos/UniDrop.icns
"
for required in $required_files; do
  [ -f "$required" ] || { printf '%s\n' "Missing package input: $required" >&2; exit 1; }
done

APP="$WORK_DIR/UniDrop.app"
CONTENTS="$APP/Contents"
MACOS_DIR="$CONTENTS/MacOS"
RESOURCES="$CONTENTS/Resources"
mkdir -p "$MACOS_DIR" "$RESOURCES"

lipo -create "$DIST_DIR/unidrop-menu-darwin-amd64" "$DIST_DIR/unidrop-menu-darwin-arm64" -output "$MACOS_DIR/UniDrop"
lipo -create "$DIST_DIR/unidrop-darwin-amd64" "$DIST_DIR/unidrop-darwin-arm64" -output "$RESOURCES/unidrop-core"
lipo -create "$DIST_DIR/unidrop-update-darwin-amd64" "$DIST_DIR/unidrop-update-darwin-arm64" -output "$RESOURCES/unidrop-update"
chmod 755 "$MACOS_DIR/UniDrop" "$RESOURCES/unidrop-core" "$RESOURCES/unidrop-update"
sed "s/__VERSION__/$VERSION/g" "$SCRIPT_DIR/macos/Info.plist" > "$CONTENTS/Info.plist"
cp "$SCRIPT_DIR/macos/UniDrop.icns" "$RESOURCES/UniDrop.icns"
plutil -lint "$CONTENTS/Info.plist" >/dev/null

for binary in "$MACOS_DIR/UniDrop" "$RESOURCES/unidrop-core" "$RESOURCES/unidrop-update"; do
  architectures=$(lipo -archs "$binary")
  case "$architectures" in
    "x86_64 arm64"|"arm64 x86_64") ;;
    *) printf '%s\n' "Universal binary is missing an architecture: $binary ($architectures)" >&2; exit 1 ;;
  esac
done

SIGNING_IDENTITY=${MACOS_SIGNING_IDENTITY:--}
sign_one() {
  target=$1
  if [ "$SIGNING_IDENTITY" = "-" ]; then
    codesign --force --options runtime --entitlements "$SCRIPT_DIR/macos/UniDrop.entitlements" --sign - "$target"
  else
    codesign --force --timestamp --options runtime --entitlements "$SCRIPT_DIR/macos/UniDrop.entitlements" --sign "$SIGNING_IDENTITY" "$target"
  fi
}

sign_one "$RESOURCES/unidrop-core"
sign_one "$RESOURCES/unidrop-update"
sign_one "$MACOS_DIR/UniDrop"
sign_one "$APP"
codesign --verify --deep --strict --verbose=2 "$APP"
for signed_target in "$RESOURCES/unidrop-core" "$RESOURCES/unidrop-update" "$MACOS_DIR/UniDrop" "$APP"; do
  codesign -d --verbose=4 "$signed_target" 2>&1 | grep -E 'flags=.*runtime' >/dev/null || {
    printf '%s\n' "Hardened Runtime flag is missing: $signed_target" >&2
    exit 1
  }
done
"$RESOURCES/unidrop-core" --version | grep -F "UniDrop $VERSION" >/dev/null

DMG_ROOT="$WORK_DIR/dmg-root"
mkdir -p "$DMG_ROOT"
cp -R "$APP" "$DMG_ROOT/UniDrop.app"
ln -s /Applications "$DMG_ROOT/Applications"
OUTPUT="$DIST_DIR/unidrop-$VERSION-macos-universal.dmg"
hdiutil create -quiet -volname "UniDrop $VERSION" -srcfolder "$DMG_ROOT" -format UDZO -ov "$OUTPUT"
if [ "$SIGNING_IDENTITY" != "-" ]; then
  codesign --force --timestamp --sign "$SIGNING_IDENTITY" "$OUTPUT"
  codesign --verify --verbose=2 "$OUTPUT"
fi

mkdir -p "$MOUNT_DIR"
hdiutil attach -quiet -readonly -nobrowse -mountpoint "$MOUNT_DIR" "$OUTPUT"
MOUNTED=1
codesign --verify --deep --strict --verbose=2 "$MOUNT_DIR/UniDrop.app"
test -L "$MOUNT_DIR/Applications"
hdiutil detach -quiet "$MOUNT_DIR"
MOUNTED=0

if [ "$SIGNING_IDENTITY" = "-" ]; then
  printf 'Created verified development DMG (ad-hoc identity; not for public release): %s\n' "$OUTPUT"
else
  printf 'Created Developer ID signed DMG ready for notarization: %s\n' "$OUTPUT"
fi
