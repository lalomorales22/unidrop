#!/bin/sh
# Remove UniDrop application files while preserving identity and paired devices
# unless --remove-user-data is explicitly requested.
set -eu

INSTALL_HOME=${UNIDROP_INSTALL_HOME:-$HOME}
REMOVE_USER_DATA=0

case "${1:-}" in
  "") ;;
  --remove-user-data) REMOVE_USER_DATA=1 ;;
  *) printf '%s\n' 'Usage: ./uninstall.sh [--remove-user-data]' >&2; exit 2 ;;
esac

case "$INSTALL_HOME" in
  ""|/) printf '%s\n' 'UniDrop uninstaller refused an unsafe install home.' >&2; exit 1 ;;
esac

say() { printf '%s\n' "UniDrop: $*"; }

remove_cli_path() {
  cli_dir=$1
  shell_name=${SHELL##*/}
  case "$shell_name" in
    zsh) profile="$INSTALL_HOME/.zprofile" ;;
    fish) profile="$INSTALL_HOME/.config/fish/config.fish" ;;
    *) profile="$INSTALL_HOME/.profile" ;;
  esac
  [ -f "$profile" ] || return
  if [ "$shell_name" = "fish" ]; then
    path_line="fish_add_path \"$cli_dir\""
  else
    path_line="export PATH=\"$cli_dir:\$PATH\""
  fi
  marker='# Added by the UniDrop installer'
  temporary=$(mktemp "$profile.unidrop.XXXXXX")
  awk -v marker="$marker" -v path_line="$path_line" '
    pending {
      if ($0 == path_line) { pending = 0; next }
      print marker
      pending = 0
    }
    $0 == marker { pending = 1; next }
    { print }
    END { if (pending) print marker }
  ' "$profile" > "$temporary"
  mv "$temporary" "$profile"
}

case "$(uname -s)" in
  Darwin)
    app_dir="$INSTALL_HOME/Applications/UniDrop.app"
    plist="$INSTALL_HOME/Library/LaunchAgents/com.unidrop.app.plist"
    cli_dir="$INSTALL_HOME/.local/bin"
    launchctl bootout "gui/$(id -u)/com.unidrop.app" >/dev/null 2>&1 || :
    rm -f "$plist" "$cli_dir/unidrop" "$INSTALL_HOME/Library/Logs/UniDrop.log"
    rm -rf "$app_dir"
    remove_cli_path "$cli_dir"
    if [ "$REMOVE_USER_DATA" = "1" ]; then
      rm -rf "$INSTALL_HOME/Library/Application Support/UniDrop"
    fi
    ;;
  Linux)
    binary_dir="$INSTALL_HOME/.local/bin"
    data_home=${XDG_DATA_HOME:-$INSTALL_HOME/.local/share}
    config_home=${XDG_CONFIG_HOME:-$INSTALL_HOME/.config}
    unit_dir="$config_home/systemd/user"
    if command -v systemctl >/dev/null 2>&1 && systemctl --user show-environment >/dev/null 2>&1; then
      systemctl --user disable --now unidrop-tray.service unidrop.service >/dev/null 2>&1 || :
    fi
    command -v pkill >/dev/null 2>&1 && pkill -TERM -x unidrop-tray >/dev/null 2>&1 || :
    command -v pkill >/dev/null 2>&1 && pkill -TERM -x unidrop >/dev/null 2>&1 || :
    rm -f \
      "$binary_dir/unidrop" \
      "$binary_dir/unidrop-tray" \
      "$data_home/applications/unidrop.desktop" \
      "$data_home/icons/hicolor/scalable/apps/unidrop.svg" \
      "$config_home/autostart/unidrop.desktop" \
      "$config_home/autostart/unidrop-tray.desktop" \
      "$unit_dir/unidrop.service" \
      "$unit_dir/unidrop-tray.service"
    if command -v systemctl >/dev/null 2>&1 && systemctl --user show-environment >/dev/null 2>&1; then
      systemctl --user daemon-reload >/dev/null 2>&1 || :
    fi
    remove_cli_path "$binary_dir"
    if [ "$REMOVE_USER_DATA" = "1" ]; then
      rm -rf "$config_home/UniDrop"
    fi
    ;;
  *)
    printf '%s\n' 'UniDrop uninstaller supports macOS and Linux; use uninstall.ps1 on Windows.' >&2
    exit 1
    ;;
esac

if [ "$REMOVE_USER_DATA" = "1" ]; then
  say 'application files and local identity/pairing data were removed; received files were preserved'
else
  say 'application files were removed; local identity and paired-device data were preserved'
fi
