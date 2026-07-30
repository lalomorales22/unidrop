# Xendfile architecture

## Why local IP instead of cloning AirDrop's radio stack

AirDrop is not simply Bluetooth file transfer. Apple combines identity services, Bluetooth Low Energy discovery, and a proprietary peer-to-peer Wi-Fi link. Windows and Linux expose different Bluetooth, Wi-Fi Direct, firewall, notification, startup, and tray APIs; some capabilities also depend on hardware drivers and privileges.

The universal layer available on all three systems is IP networking. Xendfile therefore uses:

1. UDP multicast to advertise a small device record on each active IPv4 LAN interface.
2. A local browser panel for choosing peers and files.
3. TLS 1.3 over TCP for direct machine-to-machine streaming.
4. Persistent, periodically rechecked `host:port` entries as a deterministic fallback.

Bluetooth can later serve as a discovery or IP-bootstrap channel, but it should not be the bulk-data transport: platform APIs are fragmented and its throughput is substantially worse than Wi-Fi/Ethernet.

## Process model

```mermaid
flowchart LR
    UIA["Native menu or tray shell\nor browser UI"] --> DA["Sender Xendfile daemon"]
    CLIA["xendfile send file\nminibrain.local"] -->|"loopback + private token"| DA
    DA -. "UDP multicast discovery" .-> DB["Receiver Xendfile daemon"]
    UIA -. "macOS Bonjour assist" .-> DB
    DA == "TLS 1.3 pinned HTTPS\noffer, approval, streamed file" ==> DB
    DB --> DL["Downloads/Xendfile"]
    UIB["Receiver approval UI\nAccept or Decline"] --> DB
```

Only the loopback interface can reach the control-panel API. Browser mutations require a private request header. CLI routes additionally require a random 256-bit token stored with user-only permissions. LAN peers can reach a deliberately small HTTPS API: identity inspection, pairing, authenticated offers, and accepted file receive.

## Pairing and trust

Each installation creates an ECDSA P-256 self-signed device certificate and random 128-bit device ID. The certificate's SHA-256 fingerprint is its transport identity.

On first pairing:

1. The receiver displays a random 64-bit one-time key.
2. The sender creates a random nonce and a 256-bit return token.
3. The sender sends an HMAC proof bound to the receiver fingerprint, both sender identity values, the nonce, and return token.
4. The receiver rate-limits attempts, verifies the proof, creates its own 256-bit token, persists mutual trust, and rotates the one-time key.
5. Both sides pin the other's certificate fingerprint for every later request.

This makes pairing mutual: after pairing once, either machine can send when it can discover the other. An in-place reinstall preserves the certificate and trust store; explicitly deleting user data creates a new identity and requires pairing again.

## Former-name compatibility boundary

Xendfile v0.4 changes every user-facing product, binary, package, application,
and desktop-service name. It deliberately retains the v0.3 `_unidrop._tcp`
Bonjour type, `X-UniDrop-UI` and `X-UniDrop-Sender-ID` private HTTP headers, and
`unidrop-pair-v1` HMAC domain separator. These strings are protocol identifiers,
not current branding; changing them inside protocol v2 would silently break
pairing and transfer interoperability.

If a new Xendfile configuration directory does not exist, the core and tray may
reuse an existing non-symlink former-name directory so identity and paired-device
state survive the rename. New `XENDFILE_*` environment overrides take priority,
while the corresponding `UNIDROP_*` names remain fallback aliases for v0.3
automation. Installers remove former binaries and startup entries but never the
former identity directory unless the user explicitly requests user-data removal.

## Offer and approval lifecycle

Every file has a separate, short-lived offer bound to its paired sender ID, sanitized filename, and exact byte count. The receiver chooses one persistent mode:

1. `ask` creates a pending card and sends no file bytes until **Accept** is pressed.
2. `trusted` immediately approves offers from paired devices.
3. `off` refuses new offers and declines anything still pending.

The sender polls the authenticated offer status for up to two minutes. Only an accepted, unexpired offer can be consumed, and it can enter the receiving state once. Changing the filename, sender, or content length causes the receiver to reject the upload.

## Friendly command bridge

`xendfile peers` gets the live discovery view from the loopback daemon. `xendfile send <files...> <device>` resolves a case-insensitive device ID, display name, friendly `name.local` alias, discovered address, or a manually reachable hostname. It then opens each local regular file inside the daemon and uses the same offer and streaming path as the browser.

The token in `control-token` prevents an unrelated web page from invoking filesystem paths through the bridge. The bridge accepts only loopback HTTP, absolute regular-file paths, and a correctly authenticated caller running as the same OS user.

## OS-specific shell layer

### macOS

- Install target: `~/Applications/Xendfile.app`
- Startup: `~/Library/LaunchAgents/io.github.lalomorales22.xendfile.plist`
- Configuration: `~/Library/Application Support/Xendfile`
- Background behavior: `LSUIElement` hides the Dock icon while `NSStatusItem` stays visible
- Native shell: universal Swift/AppKit executable with a transient WebKit popover
- Discovery: cross-platform UDP multicast plus native `_unidrop._tcp` Bonjour publish/browse
- Local-network privacy: purpose string in the app and `AssociatedBundleIdentifiers` in the LaunchAgent
- User actions: compact popover, full panel, CLI, pause receiving, and Quit
- Receive notification: AppleScript notification

### Linux

- Install target: `~/.local/bin/xendfile`
- Startup: systemd user service where available, otherwise XDG autostart
- Application launcher: `~/.local/share/applications/xendfile.desktop`
- Configuration: `${XDG_CONFIG_HOME:-~/.config}/Xendfile`
- Native shell: pure-Go StatusNotifierItem plus DBusMenu companion, without GTK/Qt/Electron
- Dynamic states: nearby count, pending approvals, reconnecting, and attention icon
- User actions: Open, receive-mode selection, received files, and authenticated Quit
- Process model: separate user services keep the secure core independent from desktop tray availability
- Receive notification: `notify-send` when installed
- Fallback: application-menu/browser control panel when the desktop has no StatusNotifier host
- Development Flatpak: source-built offline under the provisional
  `io.github.lalomorales22.xendfile` ID, with only LAN networking, the dedicated
  Downloads subdirectory, and scoped StatusNotifier D-Bus names exposed; see
  `docs/FLATPAK.md` for the unfulfilled distribution gates

### Windows

- Install target: `%LOCALAPPDATA%\Xendfile\xendfile.exe` plus `xendfile-tray.exe`
- Startup: per-user Startup shortcut
- Application launcher: per-user Start-menu shortcut
- Configuration: `%APPDATA%\Xendfile`
- Native shell: pure-Go Win32 hidden window plus `Shell_NotifyIconW`, without .NET, WebView2, or Electron
- Dynamic states: nearby count, pending approvals, reconnecting, attention icon, and request balloons
- User actions: Open, receive-mode selection, received files, and authenticated Quit
- Process model: the GUI-subsystem tray starts and monitors the console-capable secure core
- Recovery: stable single instance and automatic icon restoration after Windows Explorer restarts

## Protocol surface

The LAN server exposes these versioned routes:

- `GET /api/v1/info`: protocol/device metadata and certificate fingerprint
- `POST /api/v1/pair`: certificate-bound mutual pairing
- `POST /api/v1/offers`: authenticated filename/size offer
- `GET /api/v1/offers/{id}`: authenticated approval-status polling
- `POST /api/v1/files?name=...&offer=...`: authenticated, pre-approved raw file stream

Files are deliberately sent as raw request bodies rather than multipart forms. That keeps memory use bounded and makes the sender proxy each selected browser file directly to the receiver.

## Roadmap

1. Finder, Explorer, Dolphin, Nautilus, and Thunar **Send with Xendfile** entry points backed by the command bridge.
2. Optional compact Windows WebView2 panel with browser fallback.
3. Signed/notarized installers and an update manifest with binary checksums.
4. Optional QR pairing and a stronger PAKE-based short-code mode.
5. Folder transfer through a streamed, validated archive format.
6. Resumable chunked transfers and per-file BLAKE2/SHA-256 result verification.
7. Optional relay/WebRTC mode for different networks, clearly separated from LAN-only mode.

## Release trust path

`internal/version/VERSION`, `PROTOCOL`, and `MIN_COMPATIBLE_VERSION` are embedded
into every Go binary and read directly by installers, the website build, and
release tooling. `scripts/build-all.sh` cross-compiles every desktop architecture,
rebuilds both macOS menu architectures when Swift is available, and produces a
versioned manifest, SPDX 2.3 SBOM, and `SHA256SUMS` without third-party build
packages.

On macOS, `XENDFILE_BUILD_DEVELOPMENT_DMG=1` additionally creates a universal
`Xendfile.app`, signs every nested executable and the app with Hardened Runtime,
and builds a compressed DMG containing the app plus an Applications shortcut.
The default ad-hoc identity proves package structure in CI only. A public release
still requires the separately configured Developer ID identity, notarization,
stapling, and clean-machine Gatekeeper verification.

Linux AppImages are assembled from an AppDir with the native core, tray, updater,
desktop entry, scalable icon, AppStream metadata, and runtime license notice.
AMD64 and ARM64 packages build and execute on matching clean GitHub runners. The
AppRun entry point starts the core for that AppImage instance and keeps the tray
in the foreground; `--cli` dispatches an explicit command to the bundled core.
The AppStream project license is `Apache-2.0`, matching the owner-approved
repository `LICENSE`. Runtime and vendored component notices remain separately
embedded and documented in `THIRD_PARTY_NOTICES.md`.

The trusted tag workflow is isolated from pull requests. It requires an annotated
immutable version tag and protected Ed25519 signing key, signs the exact manifest
bytes, requests short-lived GitHub OIDC/Sigstore provenance and SBOM attestations,
then publishes a new release without replacing prior assets. OS publisher signing
and notarization are separate mandatory layers; a manifest signature does not
make an unsigned macOS or Windows package trustworthy to the operating system.

The update acceptance and key-rotation contract is documented in
`docs/UPDATE_SECURITY.md`. Automatic update code remains disabled until all
negative tests and clean-machine recovery checks in that document pass.
