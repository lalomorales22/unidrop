#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
DIST_DIR="$SCRIPT_DIR/dist"
VERSION=${UNIDROP_VERSION:-0.1.0}

command -v go >/dev/null 2>&1 || { printf '%s\n' 'Go is required to build release binaries.' >&2; exit 1; }
mkdir -p "$DIST_DIR"

build() {
  target_os=$1
  target_arch=$2
  suffix=$3
  output="$DIST_DIR/unidrop-$target_os-$target_arch$suffix"
  printf 'Building %s/%s...\n' "$target_os" "$target_arch"
  extra_flags=''
  if [ "$target_os" = "windows" ]; then
    extra_flags='-H=windowsgui'
  fi
  (cd "$SCRIPT_DIR" && CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build \
    -trimpath -ldflags="-s -w $extra_flags -X main.appVersion=$VERSION" -o "$output" .)
}

build darwin amd64 ''
build darwin arm64 ''
build linux amd64 ''
build linux arm64 ''
build windows amd64 '.exe'
build windows arm64 '.exe'

if command -v sha256sum >/dev/null 2>&1; then
  (cd "$DIST_DIR" && sha256sum unidrop-* > SHA256SUMS)
else
  (cd "$DIST_DIR" && shasum -a 256 unidrop-* > SHA256SUMS)
fi

printf 'Release binaries written to %s\n' "$DIST_DIR"
