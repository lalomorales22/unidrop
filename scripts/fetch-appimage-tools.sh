#!/bin/sh
set -eu

TARGET_ARCH=${1:-}
DESTINATION=${2:-}
[ -n "$TARGET_ARCH" ] && [ -n "$DESTINATION" ] || {
  printf '%s\n' 'Usage: fetch-appimage-tools.sh <amd64|arm64> <destination>' >&2
  exit 1
}

case "$TARGET_ARCH" in
  amd64)
    TOOL_ARCH=x86_64
    TOOL_SHA256=a6d71e2b6cd66f8e8d16c37ad164658985e0cf5fcaa950c90a482890cb9d13e0
    RUNTIME_SHA256=1cc49bcf1e2ccd593c379adb17c9f85a36d619088296504de95b1d06215aebbf
    ;;
  arm64)
    TOOL_ARCH=aarch64
    TOOL_SHA256=1b00524ba8c6b678dc15ef88a5c25ec24def36cdfc7e3abb32ddcd068e8007fe
    RUNTIME_SHA256=7d5d772b7c32f0c84caf0a452a3072a5709027d7eac5856feb89a7a7a8881372
    ;;
  *)
    printf '%s\n' "Unsupported AppImage architecture: $TARGET_ARCH" >&2
    exit 1
    ;;
esac

command -v curl >/dev/null 2>&1 || { printf '%s\n' 'curl is required.' >&2; exit 1; }
command -v sha256sum >/dev/null 2>&1 || { printf '%s\n' 'sha256sum is required.' >&2; exit 1; }
mkdir -p "$DESTINATION"

TOOL="$DESTINATION/appimagetool-$TOOL_ARCH.AppImage"
RUNTIME="$DESTINATION/runtime-$TOOL_ARCH"
curl --fail --location --proto '=https' --proto-redir '=https' --tlsv1.2 \
  "https://github.com/AppImage/appimagetool/releases/download/continuous/appimagetool-$TOOL_ARCH.AppImage" \
  --output "$TOOL"
curl --fail --location --proto '=https' --proto-redir '=https' --tlsv1.2 \
  "https://github.com/AppImage/type2-runtime/releases/download/continuous/runtime-$TOOL_ARCH" \
  --output "$RUNTIME"
printf '%s  %s\n' "$TOOL_SHA256" "$TOOL" | sha256sum --check --strict
printf '%s  %s\n' "$RUNTIME_SHA256" "$RUNTIME" | sha256sum --check --strict
chmod 755 "$TOOL" "$RUNTIME"
printf 'Fetched reviewed AppImage tools for %s into %s\n' "$TARGET_ARCH" "$DESTINATION"
