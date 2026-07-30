#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
SMOKE_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/unidrop-smoke.XXXXXX")
SMOKE_CONFIG="$SMOKE_ROOT/config"
SMOKE_XDG_CONFIG="$SMOKE_ROOT/xdg-config"
SMOKE_XDG_DATA="$SMOKE_ROOT/xdg-data"
trap 'rm -rf "$SMOKE_ROOT"' EXIT HUP INT TERM

install_once() {
  UNIDROP_INSTALL_HOME="$SMOKE_ROOT" \
  UNIDROP_NO_START=1 \
  XDG_CONFIG_HOME="$SMOKE_XDG_CONFIG" \
  XDG_DATA_HOME="$SMOKE_XDG_DATA" \
  GO_CMD=${GO_CMD:-go} \
  sh "$SCRIPT_DIR/install.sh"
}

uninstall_once() {
  UNIDROP_INSTALL_HOME="$SMOKE_ROOT" \
  XDG_CONFIG_HOME="$SMOKE_XDG_CONFIG" \
  XDG_DATA_HOME="$SMOKE_XDG_DATA" \
  sh "$SCRIPT_DIR/uninstall.sh" "$@"
}

case "$(uname -s)" in
  Darwin)
    BINARY="$SMOKE_ROOT/Applications/UniDrop.app/Contents/Resources/unidrop-core"
    USER_DATA="$SMOKE_ROOT/Library/Application Support/UniDrop"
    ;;
  Linux)
    BINARY="$SMOKE_ROOT/.local/bin/unidrop"
    USER_DATA="$SMOKE_XDG_CONFIG/UniDrop"
    ;;
  *) printf '%s\n' 'smoke-install.sh supports macOS and Linux' >&2; exit 1 ;;
esac

mkdir -p "$SMOKE_CONFIG" "$USER_DATA"
printf '%s\n' 'preserve-me' > "$USER_DATA/upgrade-sentinel"

install_once
test -x "$BINARY"
"$BINARY" --version | grep -F "UniDrop $(tr -d '\r\n' < "$SCRIPT_DIR/internal/version/VERSION")" >/dev/null

# An in-place reinstall must keep unrelated/user state intact.
install_once
test "$(cat "$USER_DATA/upgrade-sentinel")" = 'preserve-me'

# Exercise a real start and graceful stop without using fixed production ports.
UNIDROP_CONFIG_DIR="$SMOKE_CONFIG/runtime" \
UNIDROP_DOWNLOAD_DIR="$SMOKE_ROOT/downloads" \
"$BINARY" --ui 127.0.0.1:0 --listen 127.0.0.1:0 --no-open > "$SMOKE_ROOT/runtime.log" 2>&1 &
APP_PID=$!
sleep 1
kill -TERM "$APP_PID"
wait "$APP_PID"

uninstall_once
test ! -e "$BINARY"
test "$(cat "$USER_DATA/upgrade-sentinel")" = 'preserve-me'

# Reinstall after uninstall and verify the explicit destructive option removes
# identity data but never received files.
install_once
mkdir -p "$USER_DATA"
printf '%s\n' 'identity' > "$USER_DATA/state.json"
uninstall_once --remove-user-data
test ! -e "$BINARY"
test ! -e "$USER_DATA"

printf '%s\n' 'UniDrop install, upgrade, lifecycle, uninstall, and reinstall smoke test passed.'
