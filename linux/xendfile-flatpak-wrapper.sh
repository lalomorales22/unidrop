#!/bin/sh
set -eu

CORE=/app/bin/xendfile
TRAY=/app/bin/xendfile-tray

case "${1:-}" in
  --version)
    exec "$CORE" --version
    ;;
  --cli)
    shift
    exec "$CORE" "$@"
    ;;
esac

"$CORE" --no-open >/dev/null 2>&1 &
CORE_PID=$!

cleanup() {
  if kill -0 "$CORE_PID" >/dev/null 2>&1; then
    "$CORE" stop >/dev/null 2>&1 || kill "$CORE_PID" >/dev/null 2>&1 || :
  fi
}
trap cleanup EXIT HUP INT TERM

"$TRAY" "$@"
