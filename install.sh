#!/bin/sh
# UniDrop installer for macOS and Linux.
# It installs per-user, needs no sudo, and never downloads third-party modules.
set -eu

GO_VERSION="1.26.5"
MIN_GO_VERSION="1.25.0"
SCRIPT_DIR=$(CDPATH= cd "$(dirname "$0")" && pwd)
APP_VERSION=$(tr -d '\r\n' < "$SCRIPT_DIR/internal/version/VERSION")
TEMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/unidrop-install.XXXXXX")
INSTALL_HOME=${UNIDROP_INSTALL_HOME:-$HOME}
NO_START=${UNIDROP_NO_START:-0}
trap 'rm -rf "$TEMP_DIR"' EXIT HUP INT TERM

say() { printf '%s\n' "UniDrop: $*"; }
fail() { printf '%s\n' "UniDrop installer error: $*" >&2; exit 1; }

ensure_cli_path() {
  cli_dir=$1
  case ":${PATH:-}:" in
    *":$cli_dir:"*) return ;;
  esac
  shell_name=${SHELL##*/}
  case "$shell_name" in
    zsh) profile="$INSTALL_HOME/.zprofile" ;;
    fish) profile="$INSTALL_HOME/.config/fish/config.fish" ;;
    *) profile="$INSTALL_HOME/.profile" ;;
  esac
  if [ "$shell_name" = "fish" ]; then
    path_line="fish_add_path \"$cli_dir\""
  else
    path_line="export PATH=\"$cli_dir:\$PATH\""
  fi
  mkdir -p "$(dirname "$profile")"
  if [ ! -f "$profile" ] || ! grep -F "$path_line" "$profile" >/dev/null 2>&1; then
    printf '\n%s\n%s\n' '# Added by the UniDrop installer' "$path_line" >> "$profile"
  fi
  say "added $cli_dir to your shell PATH (new terminals will see it)"
}

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

go_is_compatible() {
  candidate=$1
  version=$("$candidate" env GOVERSION 2>/dev/null || :)
  version=${version#go}
  [ -n "$version" ] || return 1
  awk -v have="$version" -v need="$MIN_GO_VERSION" 'BEGIN {
    split(have, h, "."); split(need, n, ".")
    for (i = 1; i <= 3; i++) {
      hv = h[i] + 0; nv = n[i] + 0
      if (hv > nv) exit 0
      if (hv < nv) exit 1
    }
    exit 0
  }'
}

prepare_go() {
  if [ -n "${GO_CMD:-}" ] && go_is_compatible "$GO_CMD"; then
    return
  fi
  if command -v go >/dev/null 2>&1 && go_is_compatible "$(command -v go)"; then
    GO_CMD=$(command -v go)
    say "building with the installed Go compiler"
    return
  fi
  if command -v go >/dev/null 2>&1; then
    say "the installed Go compiler is older than $MIN_GO_VERSION; using the verified current toolchain"
  fi
  command -v curl >/dev/null 2>&1 || fail "curl is required to fetch the verified Go compiler"
  archive="go$GO_VERSION.$TARGET_OS-$TARGET_ARCH.tar.gz"
  archive_path="$TEMP_DIR/$archive"
  say "fetching the official Go $GO_VERSION toolchain from go.dev"
  curl -fL --retry 3 --proto '=https' --tlsv1.2 -o "$archive_path" "https://go.dev/dl/$archive"
  verify_sha256 "$(toolchain_hash)" "$archive_path"
  tar -xzf "$archive_path" -C "$TEMP_DIR"
  GO_CMD="$TEMP_DIR/go/bin/go"
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
  [ -d "$SCRIPT_DIR/vendor" ] || fail "vendored module source is required for an offline build"

  prepare_go

  say "building UniDrop $APP_VERSION (standard library only)"
  (cd "$SCRIPT_DIR" && CGO_ENABLED=0 GOOS="$TARGET_OS" GOARCH="$TARGET_ARCH" "$GO_CMD" build \
    -mod=vendor -trimpath -ldflags="-s -w -X main.appVersion=$APP_VERSION" -o "$output" .)
  chmod 755 "$output"
}

build_linux_tray() {
  output=$1
  [ -f "$SCRIPT_DIR/cmd/unidrop-tray/main.go" ] || fail "Linux tray source is missing"
  [ -d "$SCRIPT_DIR/vendor/github.com/godbus/dbus/v5" ] || fail "vendored Linux D-Bus source is missing"
  prepare_go
  say "building the native Linux AppIndicator tray"
  (cd "$SCRIPT_DIR" && CGO_ENABLED=0 GOOS=linux GOARCH="$TARGET_ARCH" "$GO_CMD" build \
    -mod=vendor -trimpath -ldflags="-s -w -X main.appVersion=$APP_VERSION" -o "$output" ./cmd/unidrop-tray)
  chmod 755 "$output"
}

build_macos_menu() {
  output=$1
  portable="$SCRIPT_DIR/macos/UniDropMenu.universal"
  if [ -f "$portable" ]; then
    say "using the verified universal macOS menu-bar shell"
    verify_sha256 'd403bd6a4d8bd0dc91619223cae7dac4902e5bbea3f4449db7c0541ea966bede' "$portable"
    cp "$portable" "$output"
    chmod 755 "$output"
    return
  fi
  bundled="$SCRIPT_DIR/dist/unidrop-menu-darwin-$TARGET_ARCH"
  if [ -f "$bundled" ]; then
    say "using bundled macOS menu-bar shell"
    manifest="$SCRIPT_DIR/dist/SHA256SUMS"
    if [ -f "$manifest" ]; then
      expected=$(awk -v name="$(basename "$bundled")" '$2 == name || $2 == "*" name {print $1; exit}' "$manifest")
      [ -n "$expected" ] || fail "bundled menu-bar shell is missing from SHA256SUMS"
      verify_sha256 "$expected" "$bundled"
    fi
    cp "$bundled" "$output"
  else
    [ -f "$SCRIPT_DIR/macos/UniDropMenu.swift" ] || fail "macOS menu-bar source is missing"
    command -v xcrun >/dev/null 2>&1 && xcrun --find swiftc >/dev/null 2>&1 || \
      fail "the source installer needs Apple's Swift compiler; run xcode-select --install or use a bundled UniDrop release"
    say "building the native macOS menu-bar shell"
    case "$TARGET_ARCH" in
      amd64) swift_arch="x86_64" ;;
      arm64) swift_arch="arm64" ;;
    esac
    target="$swift_arch-apple-macosx13.0"
    xcrun swiftc -swift-version 5 -O -whole-module-optimization -target "$target" \
      -framework AppKit -framework WebKit "$SCRIPT_DIR/macos/UniDropMenu.swift" -o "$output"
  fi
  chmod 755 "$output"
}

install_macos() {
  app_dir="$INSTALL_HOME/Applications/UniDrop.app"
  contents="$app_dir/Contents"
  binary="$contents/Resources/unidrop-core"
  menu_binary="$contents/MacOS/UniDrop"
  launch_agents="$INSTALL_HOME/Library/LaunchAgents"
  plist="$launch_agents/com.unidrop.app.plist"
  cli_dir="$INSTALL_HOME/.local/bin"

  mkdir -p "$contents/MacOS" "$contents/Resources" "$launch_agents" "$cli_dir"
  rm -f "$contents/MacOS/unidrop"
  build_binary "$binary"
  build_macos_menu "$menu_binary"
  cat > "$contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "https://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleDisplayName</key><string>UniDrop</string>
  <key>CFBundleExecutable</key><string>UniDrop</string>
  <key>CFBundleIdentifier</key><string>com.unidrop.app</string>
  <key>CFBundleName</key><string>UniDrop</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>$APP_VERSION</string>
  <key>CFBundleVersion</key><string>$APP_VERSION</string>
  <key>LSMinimumSystemVersion</key><string>13.0</string>
  <key>LSUIElement</key><true/>
  <key>NSLocalNetworkUsageDescription</key><string>UniDrop searches for your nearby computers and sends files directly over your local network.</string>
  <key>NSBonjourServices</key><array><string>_unidrop._tcp</string></array>
  <key>NSAppTransportSecurity</key><dict><key>NSAllowsLocalNetworking</key><true/></dict>
</dict></plist>
PLIST
  cat > "$plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "https://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>com.unidrop.app</string>
  <key>ProgramArguments</key><array><string>$menu_binary</string></array>
  <key>AssociatedBundleIdentifiers</key><array><string>com.unidrop.app</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ProcessType</key><string>Interactive</string>
  <key>StandardOutPath</key><string>$INSTALL_HOME/Library/Logs/UniDrop.log</string>
  <key>StandardErrorPath</key><string>$INSTALL_HOME/Library/Logs/UniDrop.log</string>
</dict></plist>
PLIST
  ln -sf "$binary" "$cli_dir/unidrop"
  ensure_cli_path "$cli_dir"
  if command -v codesign >/dev/null 2>&1; then
    codesign --force --deep --sign - "$app_dir" >/dev/null
  fi
  if [ "$NO_START" != "1" ]; then
    launchctl bootout "gui/$(id -u)/com.unidrop.app" >/dev/null 2>&1 || :
    launchctl enable "gui/$(id -u)/com.unidrop.app" >/dev/null 2>&1 || :
    launchctl bootstrap "gui/$(id -u)" "$plist"
  fi
  say "installed $app_dir"
  if [ "$NO_START" = "1" ]; then
    say "startup files installed; automatic start was skipped"
  else
    say "UniDrop is running in your menu bar. Click the ⇅ icon to open it."
  fi
}

install_linux() {
  binary_dir="$INSTALL_HOME/.local/bin"
  binary="$binary_dir/unidrop"
  tray_binary="$binary_dir/unidrop-tray"
  apps_dir="${XDG_DATA_HOME:-$INSTALL_HOME/.local/share}/applications"
  icons_dir="${XDG_DATA_HOME:-$INSTALL_HOME/.local/share}/icons/hicolor/scalable/apps"
  autostart_dir="${XDG_CONFIG_HOME:-$INSTALL_HOME/.config}/autostart"
  mkdir -p "$binary_dir" "$apps_dir" "$icons_dir" "$autostart_dir"
  build_binary "$binary"
  build_linux_tray "$tray_binary"
  ensure_cli_path "$binary_dir"
  cp "$SCRIPT_DIR/linux/unidrop.svg" "$icons_dir/unidrop.svg"

  cat > "$apps_dir/unidrop.desktop" <<DESKTOP
[Desktop Entry]
Type=Application
Name=UniDrop
Comment=Secure local file sharing
Exec=$binary --open
Icon=unidrop
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
    cat > "$unit_dir/unidrop-tray.service" <<UNIT
[Unit]
Description=UniDrop Linux tray indicator
After=unidrop.service graphical-session.target
Requires=unidrop.service

[Service]
ExecStart=$tray_binary
Restart=on-failure
RestartSec=3

[Install]
WantedBy=default.target
UNIT
    if [ "$NO_START" != "1" ]; then
      systemctl --user daemon-reload
      systemctl --user enable unidrop.service unidrop-tray.service
      systemctl --user restart unidrop.service
      systemctl --user restart unidrop-tray.service
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
    cat > "$autostart_dir/unidrop-tray.desktop" <<DESKTOP
[Desktop Entry]
Type=Application
Name=UniDrop tray indicator
Exec=$tray_binary
Terminal=false
X-GNOME-Autostart-enabled=true
DESKTOP
    if [ "$NO_START" != "1" ]; then
      "$binary" stop >/dev/null 2>&1 || {
        command -v pkill >/dev/null 2>&1 && pkill -TERM -x unidrop >/dev/null 2>&1 || :
      }
      command -v pkill >/dev/null 2>&1 && pkill -TERM -x unidrop-tray >/dev/null 2>&1 || :
      if command -v pgrep >/dev/null 2>&1; then
        wait_count=0
        while pgrep -x unidrop >/dev/null 2>&1 && [ "$wait_count" -lt 5 ]; do
          sleep 1
          wait_count=$((wait_count + 1))
        done
      fi
      "$binary" --no-open >/dev/null 2>&1 &
      "$tray_binary" >/dev/null 2>&1 &
    fi
  fi
  say "installed $binary"
  if [ "$NO_START" = "1" ]; then
    say "startup files installed; automatic start was skipped"
  else
    say "UniDrop's Linux tray indicator is running. Click its icon or open UniDrop from the application menu."
    say "If your desktop hides StatusNotifier/AppIndicator icons, the application-menu browser panel remains available."
  fi
}

case "$TARGET_OS" in
  darwin) install_macos ;;
  linux) install_linux ;;
esac
