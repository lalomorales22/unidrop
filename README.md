# UniDrop

UniDrop is a working cross-platform local file sender for macOS, Linux, and Windows. It discovers nearby computers, pairs them with a one-time key, and streams files directly over a TLS 1.3 connection. There is no cloud upload and no account.

The secure engine and responsive interface are intentionally contained in [`main.go`](main.go). macOS adds a tiny native AppKit/WebKit menu-bar shell from [`macos/UniDropMenu.swift`](macos/UniDropMenu.swift). UniDrop uses no third-party runtime, npm tree, Electron bundle, database, or third-party Go module.

## What works in v0.3

- macOS, Linux, and Windows binaries from one source file
- automatic peer discovery on the same LAN using local multicast
- native macOS menu-bar icon that stays running and opens a compact popover
- Bonjour-assisted Mac-to-Mac discovery plus resilient multicast retry
- nearby-device and pending-approval count in the menu bar
- compact nearly-black UI designed for the menu-bar popover
- explicit **Pair** action; IP entry is now an expandable fallback
- manual IP address fallback when a network blocks multicast
- mutual pairing with a 64-bit one-time key
- TLS 1.3, certificate pinning, 256-bit bearer tokens, and pairing rate limits
- receiver approval cards showing sender, filename, and size before any file bytes are accepted
- three receive modes: **Ask every time**, **Trusted devices**, and **Receiving off**
- a local command bridge: `unidrop send photo.jpg minibrain.local`
- friendly `.local` aliases derived from nearby UniDrop device names
- multiple-file drag and drop with browser upload progress
- streamed transfers instead of loading whole files into memory
- safe filenames, unique receive names, partial-file cleanup, and a 20 GiB per-file limit
- received files in `Downloads/UniDrop`
- per-user startup integration and application shortcuts
- no administrator/root requirement

## Install

Clone UniDrop, then run the installer:

```sh
git clone https://github.com/lalomorales22/unidrop.git
cd unidrop
```

On macOS or Linux:

```sh
chmod +x install.sh
./install.sh
```

On Windows, `install.sh` delegates to PowerShell when launched from Git Bash/MSYS. You can also right-click `install.ps1`, choose **Run with PowerShell**, or run:

```powershell
Set-ExecutionPolicy -Scope Process Bypass
.\install.ps1
```

If Go is already installed, the installer uses it. Otherwise it downloads the official Go 1.26.5 toolchain from `go.dev` into a temporary directory, verifies its pinned SHA-256 checksum, builds UniDrop, then deletes that temporary toolchain. The installed application itself has no runtime dependencies.

Go 1.26.5 is intentionally pinned because it is the current July 2026 security release and includes the latest fixes in `crypto/tls` and `os`. See the [Go release history](https://go.dev/doc/devel/release) and [Go vulnerability database](https://pkg.go.dev/vuln/).

The macOS installer includes a SHA-256-pinned universal menu-bar binary built from the checked-in Swift source, so Xcode is not required. If that binary is intentionally omitted, the installer can rebuild it with Apple’s Swift compiler. No third-party package is downloaded.

## Use

1. Open UniDrop on both computers. On macOS, click the ⇅ icon in the menu bar; the full panel remains available at [http://127.0.0.1:43337](http://127.0.0.1:43337).
2. UniDrop searches automatically. Choose the nearby device that appears.
3. Copy the one-time pairing key shown by the receiver, paste it on the sender, and press **Pair**.
4. Drop one or more files and press **Send**.
5. The receiver reviews the filename, size, and paired sender, then presses **Accept** or **Decline**.
6. Accepted files appear in `Downloads/UniDrop`.

The first macOS launch asks for Local Network access. Choose **Allow** so automatic discovery can see nearby Macs. UniDrop now retries discovery after the decision instead of silently giving up. If a managed, guest, or multicast-blocked network still hides peers, expand **Connect by address instead** and enter the address displayed by the other computer.

### Send from the command line

The installer adds the `unidrop` command to your user PATH. Open a new terminal after the first install so the shell sees it.

List nearby devices and their command names:

```sh
unidrop peers
```

Send a file using the nearby computer's UniDrop name:

```sh
unidrop send /path/to/photo.jpg minibrain.local
```

Send several files, especially when the device name contains spaces:

```sh
unidrop send --to mini-brain.local photo.jpg notes.pdf
```

The background UniDrop service must be running and the devices must already be paired. The `.local` value is a friendly UniDrop discovery alias, so it works even when the operating system has not registered that exact mDNS hostname. A real hostname or `host:port` is also accepted as a fallback. The command waits for the receiver's decision and reports a clear declined, expired, or completed result.

Incoming behavior is controlled from the receiver panel:

- **Ask every time** is the safe default and displays an approval card.
- **Trusted devices** automatically accepts files from already-paired machines.
- **Receiving off** declines new offers until receiving is enabled again.

## Platform behavior

| Platform | Startup and app access | Received files | Current native shell integration |
|---|---|---|---|
| macOS | `~/Applications/UniDrop.app` plus a LaunchAgent | `~/Downloads/UniDrop` | Native menu-bar popover, Bonjour discovery, count badge, compact UI, notifications |
| Linux | application-menu entry plus systemd user service or XDG autostart | `~/Downloads/UniDrop` | Browser control panel; `notify-send` when available |
| Windows | Start-menu and Startup shortcuts; command added to user PATH | `%USERPROFILE%\Downloads\UniDrop` | Browser control panel; private-network firewall prompt may appear |

macOS now has the first native shell layer. Windows and Linux tray shells are next. They will reuse the same loopback summary, discovery, approval, and command APIs while adapting to Shell_NotifyIcon on Windows and StatusNotifier/AppIndicator where the Linux desktop supports it.

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the OS breakdown and [SECURITY.md](SECURITY.md) for the threat model and remaining hardening work.

## Develop

Go 1.22 or newer is required; use the current patched release for production builds.

```sh
go test -race ./...
go vet ./...
go run .
```

Build all release targets:

```sh
./scripts/build-all.sh
```

Generated binaries go into `dist/` and are intentionally ignored by Git. Put them next to the installers for an offline installation; otherwise the installer builds from source.
The release builder also writes `dist/SHA256SUMS`, which both installers verify when bundled binaries are present.

## Network ports

- `127.0.0.1:43337/tcp`: browser control panel; loopback only
- `0.0.0.0:43338/tcp`: TLS 1.3 peer API
- `239.255.77.77:43339/udp`: local multicast discovery

UniDrop is currently for devices on the same local IP network. It does not open router ports, use UPnP, or provide an internet relay.

Protocol v2 is used by UniDrop v0.2 and v0.3. Upgrade both computers together; v0.1 peers are intentionally ignored because v0.1 did not negotiate receiver approval before sending bytes.
