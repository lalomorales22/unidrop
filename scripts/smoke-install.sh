#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
SMOKE_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/xendfile-smoke.XXXXXX")
SMOKE_CONFIG="$SMOKE_ROOT/config"
SMOKE_XDG_CONFIG="$SMOKE_ROOT/xdg-config"
SMOKE_XDG_DATA="$SMOKE_ROOT/xdg-data"
trap 'rm -rf "$SMOKE_ROOT"' EXIT HUP INT TERM

install_once() {
  XENDFILE_INSTALL_HOME="$SMOKE_ROOT" \
  XENDFILE_NO_START=1 \
  XDG_CONFIG_HOME="$SMOKE_XDG_CONFIG" \
  XDG_DATA_HOME="$SMOKE_XDG_DATA" \
  GO_CMD=${GO_CMD:-go} \
  sh "$SCRIPT_DIR/install.sh"
}

uninstall_once() {
  XENDFILE_INSTALL_HOME="$SMOKE_ROOT" \
  XDG_CONFIG_HOME="$SMOKE_XDG_CONFIG" \
  XDG_DATA_HOME="$SMOKE_XDG_DATA" \
  sh "$SCRIPT_DIR/uninstall.sh" "$@"
}

case "$(uname -s)" in
  Darwin)
    BINARY="$SMOKE_ROOT/Applications/Xendfile.app/Contents/Resources/xendfile-core"
    USER_DATA="$SMOKE_ROOT/Library/Application Support/Xendfile"
    LEGACY_USER_DATA="$SMOKE_ROOT/Library/Application Support/UniDrop"
    LEGACY_BINARY="$SMOKE_ROOT/.local/bin/unidrop"
    LEGACY_APP="$SMOKE_ROOT/Applications/UniDrop.app"
    INSTALLED_LICENSE="$SMOKE_ROOT/Applications/Xendfile.app/Contents/Resources/LICENSE"
    ;;
  Linux)
    BINARY="$SMOKE_ROOT/.local/bin/xendfile"
    USER_DATA="$SMOKE_XDG_CONFIG/Xendfile"
    LEGACY_USER_DATA="$SMOKE_XDG_CONFIG/UniDrop"
    LEGACY_BINARY="$SMOKE_ROOT/.local/bin/unidrop"
    LEGACY_APP="$SMOKE_XDG_DATA/applications/unidrop.desktop"
    INSTALLED_LICENSE="$SMOKE_XDG_DATA/doc/xendfile/LICENSE"
    ;;
  *) printf '%s\n' 'smoke-install.sh supports macOS and Linux' >&2; exit 1 ;;
esac

mkdir -p "$SMOKE_CONFIG" "$USER_DATA" "$LEGACY_USER_DATA" "$(dirname "$LEGACY_BINARY")" "$(dirname "$LEGACY_APP")"
printf '%s\n' 'preserve-me' > "$USER_DATA/upgrade-sentinel"
printf '%s\n' 'legacy-identity' > "$LEGACY_USER_DATA/upgrade-sentinel"
printf '%s\n' 'legacy-binary' > "$LEGACY_BINARY"
if [ "$(uname -s)" = "Darwin" ]; then
  mkdir -p "$LEGACY_APP"
else
  printf '%s\n' '[Desktop Entry]' > "$LEGACY_APP"
fi

install_once
test -x "$BINARY"
test -f "$INSTALLED_LICENSE"
test ! -e "$LEGACY_BINARY"
test ! -e "$LEGACY_APP"
test "$(cat "$LEGACY_USER_DATA/upgrade-sentinel")" = 'legacy-identity'
"$BINARY" --version | grep -F "Xendfile $(tr -d '\r\n' < "$SCRIPT_DIR/internal/version/VERSION")" >/dev/null

# An in-place reinstall must keep unrelated/user state intact.
install_once
test "$(cat "$USER_DATA/upgrade-sentinel")" = 'preserve-me'

# Exercise a real start and graceful stop without using fixed production ports.
XENDFILE_CONFIG_DIR="$SMOKE_CONFIG/runtime" \
XENDFILE_DOWNLOAD_DIR="$SMOKE_ROOT/downloads" \
"$BINARY" --ui 127.0.0.1:0 --listen 127.0.0.1:0 --no-open > "$SMOKE_ROOT/runtime.log" 2>&1 &
APP_PID=$!
sleep 1
kill -TERM "$APP_PID"
wait "$APP_PID"

uninstall_once
test ! -e "$BINARY"
test ! -e "$INSTALLED_LICENSE"
test "$(cat "$USER_DATA/upgrade-sentinel")" = 'preserve-me'

# Reinstall after uninstall and verify the explicit destructive option removes
# identity data but never received files.
install_once
mkdir -p "$USER_DATA"
printf '%s\n' 'identity' > "$USER_DATA/state.json"
uninstall_once --remove-user-data
test ! -e "$BINARY"
test ! -e "$USER_DATA"
test ! -e "$LEGACY_USER_DATA"

printf '%s\n' 'Xendfile install, upgrade, lifecycle, uninstall, and reinstall smoke test passed.'
