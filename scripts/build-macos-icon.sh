#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
SOURCE="$SCRIPT_DIR/linux/xendfile.svg"
OUTPUT="$SCRIPT_DIR/macos/Xendfile.icns"
WORK_DIR=$(mktemp -d "${TMPDIR:-/tmp}/xendfile-icon.XXXXXX")
trap 'rm -rf "$WORK_DIR"' EXIT HUP INT TERM

[ "$(uname -s)" = "Darwin" ] || { printf '%s\n' 'The macOS icon must be built on macOS.' >&2; exit 1; }
for tool in qlmanage sips iconutil; do
  command -v "$tool" >/dev/null 2>&1 || { printf '%s\n' "Missing required macOS tool: $tool" >&2; exit 1; }
done

qlmanage -t -s 1024 -o "$WORK_DIR" "$SOURCE" >/dev/null 2>&1
SOURCE_PNG=$(find "$WORK_DIR" -maxdepth 1 -type f -name '*.png' -print | head -n 1)
[ -n "$SOURCE_PNG" ] || { printf '%s\n' 'Quick Look did not render the Xendfile SVG.' >&2; exit 1; }

ICONSET="$WORK_DIR/Xendfile.iconset"
mkdir -p "$ICONSET"
render() {
  pixels=$1
  name=$2
  sips -z "$pixels" "$pixels" "$SOURCE_PNG" --out "$ICONSET/$name" >/dev/null
}
render 16 icon_16x16.png
render 32 icon_16x16@2x.png
render 32 icon_32x32.png
render 64 icon_32x32@2x.png
render 128 icon_128x128.png
render 256 icon_128x128@2x.png
render 256 icon_256x256.png
render 512 icon_256x256@2x.png
render 512 icon_512x512.png
render 1024 icon_512x512@2x.png
iconutil -c icns "$ICONSET" -o "$OUTPUT"
printf 'Generated %s\n' "$OUTPUT"
