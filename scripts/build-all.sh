#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
DIST_DIR="$SCRIPT_DIR/dist"
VERSION=${UNIDROP_VERSION:-$(tr -d '\r\n' < "$SCRIPT_DIR/internal/version/VERSION")}
GO_BINARY=${GO_CMD:-go}

command -v "$GO_BINARY" >/dev/null 2>&1 || { printf '%s\n' 'Go is required to build release binaries.' >&2; exit 1; }
mkdir -p "$DIST_DIR"
rm -f "$DIST_DIR/unidrop-$VERSION-macos-universal.dmg"

build() {
  target_os=$1
  target_arch=$2
  suffix=$3
  output="$DIST_DIR/unidrop-$target_os-$target_arch$suffix"
  printf 'Building %s/%s...\n' "$target_os" "$target_arch"
  (cd "$SCRIPT_DIR" && CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" "$GO_BINARY" build \
    -mod=vendor -trimpath -ldflags="-s -w -X main.appVersion=$VERSION" -o "$output" .)
}

build_tray() {
  target_arch=$1
  output="$DIST_DIR/unidrop-tray-linux-$target_arch"
  printf 'Building Linux tray/%s...\n' "$target_arch"
  (cd "$SCRIPT_DIR" && CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" "$GO_BINARY" build \
    -mod=vendor -trimpath -ldflags="-s -w -X main.appVersion=$VERSION" -o "$output" ./cmd/unidrop-tray)
}

build_windows_tray() {
  target_arch=$1
  output="$DIST_DIR/unidrop-tray-windows-$target_arch.exe"
  printf 'Building Windows notification-area companion/%s...\n' "$target_arch"
  (cd "$SCRIPT_DIR" && CGO_ENABLED=0 GOOS=windows GOARCH="$target_arch" "$GO_BINARY" build \
    -mod=vendor -trimpath -ldflags="-s -w -H=windowsgui -X main.appVersion=$VERSION" -o "$output" ./cmd/unidrop-tray-windows)
}

build_updater() {
  target_os=$1
  target_arch=$2
  suffix=$3
  output="$DIST_DIR/unidrop-update-$target_os-$target_arch$suffix"
  printf 'Building signed update verifier for %s/%s...\n' "$target_os" "$target_arch"
  (cd "$SCRIPT_DIR" && CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" "$GO_BINARY" build \
    -mod=vendor -trimpath -ldflags="-s -w" -o "$output" ./cmd/unidrop-update)
}

build darwin amd64 ''
build darwin arm64 ''
build linux amd64 ''
build linux arm64 ''
build_tray amd64
build_tray arm64
build windows amd64 '.exe'
build windows arm64 '.exe'
build_windows_tray amd64
build_windows_tray arm64
build_updater darwin amd64 ''
build_updater darwin arm64 ''
build_updater linux amd64 ''
build_updater linux arm64 ''
build_updater windows amd64 '.exe'
build_updater windows arm64 '.exe'

if [ "$(uname -s)" = "Darwin" ] && command -v xcrun >/dev/null 2>&1 && xcrun --find swiftc >/dev/null 2>&1; then
  "$SCRIPT_DIR/scripts/build-macos-menu.sh"
fi

if [ "${UNIDROP_BUILD_DEVELOPMENT_DMG:-0}" = "1" ]; then
  "$SCRIPT_DIR/scripts/build-macos-package.sh"
fi

command -v node >/dev/null 2>&1 || { printf '%s\n' 'Node.js is required to generate release metadata.' >&2; exit 1; }
node "$SCRIPT_DIR/scripts/release-metadata.mjs"

printf 'Release binaries written to %s\n' "$DIST_DIR"
