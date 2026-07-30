# UniDrop

UniDrop is a working cross-platform local file sender for macOS, Linux, and Windows. It discovers nearby computers, pairs them with a one-time key, and streams files directly over a TLS 1.3 connection. There is no cloud upload and no account.

The application core and responsive control panel are intentionally contained in [`main.go`](main.go). It uses only the Go standard library: no npm tree, Electron runtime, PHP server, database, or third-party Go module is installed.

## What works in v0.1

- macOS, Linux, and Windows binaries from one source file
- automatic peer discovery on the same LAN using local multicast
- manual IP address fallback when a network blocks multicast
- mutual pairing with a 64-bit one-time key
- TLS 1.3, certificate pinning, 256-bit bearer tokens, and pairing rate limits
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

## Use

1. Open UniDrop on both computers. The control panel is always available at [http://127.0.0.1:43337](http://127.0.0.1:43337).
2. Choose the nearby device on the sender.
3. Copy the one-time pairing key shown by the receiver into the sender.
4. Drop one or more files and press **Send**.
5. The receiver finds them in `Downloads/UniDrop`.

The first launch may trigger an operating-system firewall prompt. Allow private/local network access. If the other computer does not appear, enter its LAN address in the manual field, for example `192.168.1.20:43338`.

## Platform behavior

| Platform | Startup and app access | Received files | Current native shell integration |
|---|---|---|---|
| macOS | `~/Applications/UniDrop.app` plus a LaunchAgent | `~/Downloads/UniDrop` | Background app with no Dock icon; browser control panel; native notification |
| Linux | application-menu entry plus systemd user service or XDG autostart | `~/Downloads/UniDrop` | Browser control panel; `notify-send` when available |
| Windows | Start-menu and Startup shortcuts | `%USERPROFILE%\Downloads\UniDrop` | Browser control panel; private-network firewall prompt may appear |

A true native menu-bar/system-tray adapter is the next shell layer. The network and security core is already independent of it. Native tray APIs are different on every target—AppKit on macOS, AppIndicator/StatusNotifier on Linux, and Shell_NotifyIcon on Windows—and Linux has no single universal tray API. Keeping that adapter separate preserves the dependency-free, easily cross-compiled core.

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
