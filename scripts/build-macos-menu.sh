#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
DIST_DIR="$SCRIPT_DIR/dist"
SOURCE="$SCRIPT_DIR/macos/XendfileMenu.swift"

[ "$(uname -s)" = "Darwin" ] || { printf '%s\n' 'The native menu shell must be built on macOS.' >&2; exit 1; }
command -v xcrun >/dev/null 2>&1 && xcrun --find swiftc >/dev/null 2>&1 || {
  printf '%s\n' 'Apple Swift compiler not found. Install Xcode Command Line Tools.' >&2
  exit 1
}
[ -f "$SOURCE" ] || { printf '%s\n' "Missing $SOURCE" >&2; exit 1; }
mkdir -p "$DIST_DIR"

for target_arch in amd64 arm64; do
  case "$target_arch" in
    amd64) swift_arch="x86_64" ;;
    arm64) swift_arch="arm64" ;;
  esac
  output="$DIST_DIR/xendfile-menu-darwin-$target_arch"
  printf 'Building macOS menu shell for %s...\n' "$target_arch"
  xcrun swiftc -swift-version 5 -O -whole-module-optimization \
    -target "$swift_arch-apple-macosx13.0" -framework AppKit -framework WebKit \
    "$SOURCE" -o "$output"
  chmod 755 "$output"
done
