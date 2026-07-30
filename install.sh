#!/bin/sh
# UniDrop installer for macOS and Linux.
# It installs per-user, needs no sudo, and never downloads third-party modules.
set -eu

APP_VERSION="0.1.0"
GO_VERSION="1.26.5"
SCRIPT_DIR=$(CDPATH= cd "$(dirname "$0")" && pwd)
TEMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/unidrop-install.XXXXXX")
INSTALL_HOME=${UNIDROP_INSTALL_HOME:-$HOME}
NO_START=${UNIDROP_NO_START:-0}
trap 'rm -rf "$TEMP_DIR"' EXIT HUP INT TERM

say() { printf '%s\n' "UniDrop: $*"; }
fail() { printf '%s\n' "UniDrop installer error: $*" >&2; exit 1; }

OS_NAME=$(uname -s)
case "$OS_NAME" in
  Darwin) TARGET_OS="darwin" ;;
  Linux) TARGET_OS="linux" ;;
  MINGW*|MSYS*|CYGWIN*)
    command -v powershell.exe >/dev/null 2>&1 || fail "PowerShell is required on Windows"
    if command -v cygpath >/dev/null 2>&1; then
      WINDOWS_INSTALLER=$(cygpath -w "$SCRIPT_DIR/install.ps1")
    else
      WINDOWS_INSTALLER="$SCRIPT_DIR/install.ps1"
    fi
    exec powershell.exe -NoProfile -ExecutionPolicy Bypass -File "$WINDOWS_INSTALLER"
    ;;
  *) fail "unsupported operating system: $OS_NAME (use install.ps1 on Windows)" ;;
esac

MACHINE=$(uname -m)
case "$MACHINE" in
  x86_64|amd64) TARGET_ARCH="amd64" ;;
  arm64|aarch64) TARGET_ARCH="arm64" ;;
  *) fail "unsupported CPU architecture: $MACHINE" ;;
esac

verify_sha256() {
  expected=$1
  file=$2
  if command -v shasum >/dev/null 2>&1; then
    actual=$(shasum -a 256 "$file" | awk '{print $1}')
  elif command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum "$file" | awk '{print $1}')
  else
    fail "SHA-256 tool not found (expected shasum or sha256sum)"
  fi
  [ "$actual" = "$expected" ] || fail "SHA-256 verification failed for $(basename "$file")"
}

toolchain_hash() {
  case "$TARGET_OS-$TARGET_ARCH" in
    darwin-amd64) printf '%s' '6231d8d3b8f5552ec6cbf6d685bdd5482e1e703214b120e89b3bf0d7bf1ef725' ;;
    darwin-arm64) printf '%s' 'efb87ff28af9a188d0536ef5d42e63dd52ba8263cd7344a993cc48dd11dedb6a' ;;
    linux-amd64) printf '%s' '5c2c3b16caefa1d968a94c1daca04a7ca301a496d9b086e17ad77bb81393f053' ;;
    linux-arm64) printf '%s' 'fe4789e92b1f33358680864bbe8704289e7bb5fc207d80623c308935bd696d49' ;;
    *) fail "no verified toolchain for $TARGET_OS-$TARGET_ARCH" ;;
  esac
}

build_binary() {
  output=$1
  bundled="$SCRIPT_DIR/dist/unidrop-$TARGET_OS-$TARGET_ARCH"
  if [ -f "$bundled" ]; then
    say "using bundled $TARGET_OS/$TARGET_ARCH binary"
    manifest="$SCRIPT_DIR/dist/SHA256SUMS"
    if [ -f "$manifest" ]; then
      expected=$(awk -v name="$(basename "$bundled")" '$2 == name || $2 == "*" name {print $1; exit}' "$manifest")
      [ -n "$expected" ] || fail "bundled binary is missing from SHA256SUMS"
      verify_sha256 "$expected" "$bundled"
    fi
    cp "$bundled" "$output"
    chmod 755 "$output"
    return
  fi

  [ -f "$SCRIPT_DIR/go.mod" ] && [ -f "$SCRIPT_DIR/main.go" ] || \
    fail "source files or a bundled binary are required"

  if command -v go >/dev/null 2>&1; then
    GO_CMD=$(command -v go)
    say "building with the installed Go compiler"
  else
    command -v curl >/dev/null 2>&1 || fail "curl is required to fetch the verified Go compiler"
    archive="go$GO_VERSION.$TARGET_OS-$TARGET_ARCH.tar.gz"
    archive_path="$TEMP_DIR/$archive"
    say "fetching the official Go $GO_VERSION toolchain from go.dev"
    curl -fL --retry 3 --proto '=https' --tlsv1.2 -o "$archive_path" "https://go.dev/dl/$archive"
    verify_sha256 "$(toolchain_hash)" "$archive_path"
    tar -xzf "$archive_path" -C "$TEMP_DIR"
    GO_CMD="$TEMP_DIR/go/bin/go"
  fi

  say "building UniDrop $APP_VERSION (standard library only)"
  (cd "$SCRIPT_DIR" && CGO_ENABLED=0 GOOS="$TARGET_OS" GOARCH="$TARGET_ARCH" "$GO_CMD" build \
    -trimpath -ldflags="-s -w -X main.appVersion=$APP_VERSION" -o "$output" .)
  chmod 755 "$output"
}

install_macos() {
  app_dir="$INSTALL_HOME/Applications/UniDrop.app"
  contents="$app_dir/Contents"
  binary="$contents/MacOS/unidrop"
  launch_agents="$INSTALL_HOME/Library/LaunchAgents"
  plist="$launch_agents/com.unidrop.app.plist"
  cli_dir="$INSTALL_HOME/.local/bin"

  mkdir -p "$contents/MacOS" "$launch_agents" "$cli_dir"
  build_binary "$binary"
  cat > "$contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "https://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleDisplayName</key><string>UniDrop</string>
  <key>CFBundleExecutable</key><string>unidrop</string>
  <key>CFBundleIdentifier</key><string>com.unidrop.app</string>
  <key>CFBundleName</key><string>UniDrop</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>0.1.0</string>
  <key>LSUIElement</key><true/>
</dict></plist>
PLIST
  cat > "$plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "https://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>com.unidrop.app</string>
  <key>ProgramArguments</key><array><string>$binary</string><string>--no-open</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ProcessType</key><string>Interactive</string>
  <key>StandardOutPath</key><string>$INSTALL_HOME/Library/Logs/UniDrop.log</string>
  <key>StandardErrorPath</key><string>$INSTALL_HOME/Library/Logs/UniDrop.log</string>
</dict></plist>
PLIST
  ln -sf "$binary" "$cli_dir/unidrop"
  if [ "$NO_START" != "1" ]; then
    launchctl bootout "gui/$(id -u)/com.unidrop.app" >/dev/null 2>&1 || :
    launchctl bootstrap "gui/$(id -u)" "$plist"
  fi
  say "installed $app_dir"
  if [ "$NO_START" = "1" ]; then
    say "startup files installed; automatic start was skipped"
  else
    say "UniDrop is running. Open the app from ~/Applications or visit http://127.0.0.1:43337"
  fi
}

install_linux() {
  binary_dir="$INSTALL_HOME/.local/bin"
  binary="$binary_dir/unidrop"
  apps_dir="${XDG_DATA_HOME:-$INSTALL_HOME/.local/share}/applications"
  autostart_dir="${XDG_CONFIG_HOME:-$INSTALL_HOME/.config}/autostart"
  mkdir -p "$binary_dir" "$apps_dir" "$autostart_dir"
  build_binary "$binary"

  cat > "$apps_dir/unidrop.desktop" <<DESKTOP
[Desktop Entry]
Type=Application
Name=UniDrop
Comment=Secure local file sharing
Exec=$binary --open
Icon=folder-publicshare
Terminal=false
Categories=Network;FileTransfer;
DESKTOP

  if command -v systemctl >/dev/null 2>&1 && systemctl --user show-environment >/dev/null 2>&1; then
    unit_dir="${XDG_CONFIG_HOME:-$INSTALL_HOME/.config}/systemd/user"
    mkdir -p "$unit_dir"
    cat > "$unit_dir/unidrop.service" <<UNIT
[Unit]
Description=UniDrop secure local file sharing
After=network-online.target

[Service]
ExecStart=$binary --no-open
Restart=on-failure
RestartSec=3

[Install]
WantedBy=default.target
UNIT
    if [ "$NO_START" != "1" ]; then
      systemctl --user daemon-reload
      systemctl --user enable --now unidrop.service
    fi
  else
    cat > "$autostart_dir/unidrop.desktop" <<DESKTOP
[Desktop Entry]
Type=Application
Name=UniDrop background service
Exec=$binary --no-open
Terminal=false
X-GNOME-Autostart-enabled=true
DESKTOP
    if [ "$NO_START" != "1" ]; then
      "$binary" --no-open >/dev/null 2>&1 &
    fi
  fi
  say "installed $binary"
  if [ "$NO_START" = "1" ]; then
    say "startup files installed; automatic start was skipped"
  else
    say "UniDrop is running. Open it from your application menu or visit http://127.0.0.1:43337"
  fi
}

case "$TARGET_OS" in
  darwin) install_macos ;;
  linux) install_linux ;;
esac
