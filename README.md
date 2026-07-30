# Xendfile

**Private, secure, cross-platform file sharing for the computers around you.**

[Install](#install) · [Send a file](#send-your-first-file) ·
[Security](SECURITY.md) · [Roadmap](ROADMAP.md) · [Contributing](CONTRIBUTING.md)

Xendfile is a universal local-network file sender for macOS, Linux, and Windows. It automatically discovers nearby Xendfile computers, pairs them with a one-time key, and streams files directly between them over TLS 1.3. There is no cloud upload, account, subscription, tracking service, or internet relay.

The secure engine and responsive dark interface live in [`main.go`](main.go). Each operating system adds a small native shell: an AppKit/WebKit menu-bar app on macOS, a StatusNotifier/AppIndicator tray on Linux, and a Win32 notification-area companion on Windows. Xendfile does not use Electron, npm, a database, or an administrator-level background service.

> Current release: **v0.3.3** · Protocol: **v2** · Network scope: **same local IP network**

> **Maturity: alpha.** Installers are not yet signed or notarized and the
> protocol has not received an independent review. See the [public roadmap](ROADMAP.md)
> and [security limitations](SECURITY.md) before relying on Xendfile for sensitive
> or regulated work.

## Xendfile name and open-source status

Xendfile is the current name of the project formerly published as the UniDrop
alpha. The canonical repository is
[`github.com/lalomorales22/xendfile`](https://github.com/lalomorales22/xendfile),
and all new binaries, installers, application identities, documentation, and
release metadata use Xendfile.

The project is open-source under the [Apache License 2.0](LICENSE) and is
provided **as is**, without warranty. Existing pre-rename alpha installations
can upgrade without discarding their local identity or paired-device records.
A few former-name protocol strings remain frozen strictly for compatibility
with those installations; see [Upgrading from the former UniDrop alpha](#upgrading-from-the-former-unidrop-alpha).

## What's included in v0.3.3

The Windows experience is now a real background desktop app instead of only a browser-launched service:

- native Windows notification-area icon built directly on `Shell_NotifyIconW`
- left-click to open Xendfile and right-click for status and receive controls
- live nearby-device, pending-approval, reconnecting, and attention icon states
- incoming file-request balloons that open the approval panel when clicked
- **Ask before receiving**, **Auto-accept paired devices**, and **Receiving paused** controls
- one-click access to received files and a clean **Quit Xendfile** action
- automatic secure-core startup and recovery if the core temporarily stops
- single-instance protection and automatic icon restoration after Explorer restarts
- console-free Windows startup while keeping `xendfile peers` and `xendfile send` usable in PowerShell
- x64 and ARM64 Windows builds from the release builder
- Windows tray and core logs under `%APPDATA%\Xendfile`
- upgrade-safe Windows installer that replaces older launchers and restarts both components

v0.3.3 also retains the resilient discovery work from v0.3.1: Xendfile advertises on active LAN interfaces, retries multicast when interfaces change, keeps manually entered peers, and performs direct health checks when multicast is unavailable.

## Highlights

- secure core binaries for macOS, Linux, and Windows from one Go source
- automatic discovery on the same LAN over Wi-Fi or Ethernet
- native menu-bar or tray integration on all three operating systems
- compact, nearly-black interface designed to fit a menu-bar popover
- explicit first-time pairing with a random 64-bit one-time key
- receiver approval cards showing sender, filename, and size before file bytes are sent
- persistent trusted-device relationships with certificate pinning
- TLS 1.3 transport and random 256-bit bearer tokens
- **Ask every time**, **Trusted devices**, and **Receiving off** modes
- multiple-file drag and drop with upload progress
- streamed transfers that do not load an entire file into memory
- command-line sending such as `xendfile send photo.jpg minibrain.local`
- persistent manual-address fallback for multicast-blocked networks
- safe filenames, unique receive names, partial-file cleanup, and a 20 GiB per-file limit
- files saved to `Downloads/Xendfile`
- per-user installation, startup, and configuration; no root or administrator access required

## Requirements

- two or more computers running Xendfile on the same local IP network
- macOS 13 or newer, a modern Linux desktop, or Windows 10/11
- an x86-64/AMD64 or ARM64 processor
- permission for local/private network communication when the operating system asks

Guest Wi-Fi, client isolation, some corporate networks, VPNs, and strict firewalls can prevent automatic discovery even when internet access works. The manual-address fallback still works when the computers can directly reach one another.

## Install

Clone the public repository on each computer:

```sh
git clone https://github.com/lalomorales22/xendfile.git
cd xendfile
```

### macOS

```sh
chmod +x install.sh
./install.sh
```

The installer creates `~/Applications/Xendfile.app`, a per-user LaunchAgent, and the `xendfile` terminal command. Xendfile starts in the menu bar without a Dock icon. On first launch, choose **Allow** when macOS requests Local Network access.

### Linux

```sh
chmod +x install.sh
./install.sh
```

The installer creates the core, the native tray companion, an application-menu entry, and systemd user services when systemd is available. Otherwise it installs XDG autostart entries. No `sudo` is required.

The indicator appears automatically on desktops with a StatusNotifier/AppIndicator host. If the desktop does not provide one, Xendfile still runs and can be opened from the application menu or at [http://127.0.0.1:43337](http://127.0.0.1:43337).

### Windows 10/11

Open PowerShell in the cloned `xendfile` folder and run:

```powershell
Set-ExecutionPolicy -Scope Process Bypass
.\install.ps1
```

You can also run `./install.sh` from Git Bash/MSYS; it delegates to PowerShell. The installer creates:

- `%LOCALAPPDATA%\Xendfile\xendfile.exe` — secure core and command-line client
- `%LOCALAPPDATA%\Xendfile\xendfile-tray.exe` — native GUI tray companion
- a per-user Startup shortcut
- a Start-menu shortcut
- a user PATH entry for the `xendfile` command

Windows may ask once whether Xendfile may communicate on private networks. Allow **Private networks** so nearby computers can connect. The tray icon may initially be inside the notification area's `^` overflow menu; it can be dragged onto the visible taskbar area.

### Install without starting immediately

On macOS or Linux:

```sh
XENDFILE_NO_START=1 ./install.sh
```

On Windows:

```powershell
.\install.ps1 -NoStart
```

Startup integration is still installed; only the immediate launch is skipped.

### What the installer downloads

If Go 1.25 or newer is already installed, Xendfile builds with it. Otherwise the installer downloads the official Go 1.26.5 toolchain from `go.dev` into a temporary directory, verifies the pinned SHA-256 checksum, builds Xendfile, and deletes the temporary toolchain.

The installed app has no runtime dependencies. Linux's two small D-Bus source dependencies are reviewed, pinned, and checked into `vendor/`, so installation does not fetch third-party modules. The Windows companion uses only Go's standard library and Win32 system APIs. See [`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md) and [`SECURITY.md`](SECURITY.md).

The repository does not check in an opaque prebuilt macOS menu binary. A bundled
release can provide the architecture-specific menu shell with its checksum;
otherwise the source installer builds `macos/XendfileMenu.swift` reproducibly
with Apple's Swift compiler. Run `xcode-select --install` if that compiler is
not already available.

## Update an existing installation

Pull the newest source and run the installer again:

```sh
git pull --ff-only
./install.sh
```

On Windows:

```powershell
git pull --ff-only
Set-ExecutionPolicy -Scope Process Bypass
.\install.ps1
```

The installer preserves Xendfile's configuration and paired-device state, replaces the application binaries, refreshes startup integration, and restarts the installed components. Upgrade both computers together when the protocol version changes.

Check the installed version from a new terminal or PowerShell window:

```sh
xendfile --version
```

## Uninstall

On macOS or Linux, run the uninstaller from the source or release folder:

```sh
./uninstall.sh
```

This removes the application, startup integration, and Xendfile PATH entry while
preserving your local identity and paired-device records for a future reinstall.
To remove those records too, use `./uninstall.sh --remove-user-data`. Received
files are always preserved.

On Windows, open **Uninstall Xendfile** from the Start menu. The default preserves
paired-device data; run the installed `uninstall.ps1 -RemoveUserData` from
PowerShell to remove it. Received files are always preserved. The release-grade
MSIX Apps & Features experience remains a v0.4.0 release blocker.

## Send your first file

1. Install and open Xendfile on both computers.
2. Wait for the other computer to appear under **Nearby devices**.
3. Select it. The receiving computer displays a one-time pairing key.
4. Enter that key on the sending computer and choose **Pair**.
5. Drag one or more files into Xendfile, or choose them with the file picker.
6. Press **Send**.
7. With the default receive mode, the receiver checks the sender, filename, and size, then chooses **Accept** or **Decline**.
8. Accepted files appear in `Downloads/Xendfile`.

Pairing is mutual. After pairing once, either computer can send to the other whenever both are online. Reinstalling in place preserves pairing; deleting Xendfile's configuration or installing as another user creates a new identity and requires pairing again.

## Menu-bar and tray controls

| Platform | Open Xendfile | Background controls |
|---|---|---|
| macOS | Click the ⇅ menu-bar icon | Compact popover, nearby/pending count, receive settings, received files, full panel, Quit |
| Linux | Click the Xendfile indicator or use the application menu | Live status, receive mode, received files, Open, Quit |
| Windows | Left-click the Xendfile notification icon or open Xendfile from Start | Right-click for live status, receive mode, received files, Open, Quit |

The full local control panel is always available at [http://127.0.0.1:43337](http://127.0.0.1:43337). It is bound to loopback and is not exposed to other computers.

### Receive modes

- **Ask every time** is the safe default. Every incoming file creates an approval card.
- **Trusted devices** automatically accepts new offers from devices you already paired with.
- **Receiving off** declines new offers until receiving is enabled again.

Use **Receiving off** when you want Xendfile available for sending but do not want to receive anything. Use **Quit Xendfile** or `xendfile stop` when you want the entire service turned off.

## Command line

Open a new terminal after the first installation so the updated user PATH is available.

List online Xendfile devices and their command names:

```sh
xendfile peers
```

Send one file:

```sh
xendfile send /path/to/photo.jpg minibrain.local
```

Send several files, or target a name containing spaces:

```sh
xendfile send --to mini-brain.local photo.jpg notes.pdf
```

Stop the background core and native shell:

```sh
xendfile stop
```

Start Xendfile again and open its panel:

```sh
xendfile --open
```

The sending computer must have its background service running, and the destination must already be paired. The friendly `.local` command name is derived from Xendfile discovery and does not require the operating system to register the same mDNS hostname. A displayed IP address, real hostname, or `host:port` can also be used.

Only regular files are accepted by the command bridge. Folder transfer is on the roadmap.

## Automatic discovery and manual connection

Xendfile advertises a small device record over local UDP multicast and listens on each active IPv4 LAN interface. Macs also publish and browse the legacy protocol identifier `_unidrop._tcp` through Bonjour. Discovery retries when interfaces appear or change, and manually added computers are health-checked directly.

If a computer does not appear automatically:

1. Confirm both devices are on the same normal LAN, not separate guest networks.
2. Temporarily disconnect VPNs or confirm the VPN permits local-LAN access.
3. Allow Xendfile through the local/private-network firewall.
4. Keep Xendfile open for several seconds after Wi-Fi connects.
5. Expand **Connect by address instead**.
6. Enter the address displayed by the other computer, normally `192.168.x.x:43338`.
7. Select the peer when it appears, pair once, and send normally.

Manual addresses are saved and rechecked. A peer can briefly disappear while unreachable, changing networks, asleep, or blocked by a firewall; it returns automatically after a successful health check.

## Troubleshooting

### Common checks

```sh
xendfile --version
xendfile peers
```

Confirm that TCP port `43338` and UDP port `43339` are allowed on the local/private network. Do not expose the peer port directly to the public internet.

### macOS

- Open **System Settings → Privacy & Security → Local Network** and enable Xendfile.
- Quit and reopen `~/Applications/Xendfile.app` after changing Local Network permission.
- Inspect the service log:

```sh
tail -n 50 ~/Library/Logs/Xendfile.log
```

### Linux

Check both per-user services:

```sh
systemctl --user status xendfile.service xendfile-tray.service --no-pager
journalctl --user -u xendfile.service -n 50 --no-pager
journalctl --user -u xendfile-tray.service -n 50 --no-pager
```

A healthy tray logs `tray registered`. If it logs `no StatusNotifier host found`, the desktop does not currently expose an AppIndicator host; use the application-menu or browser panel while the secure core continues running.

### Windows

Check the installed processes and logs from PowerShell:

```powershell
Get-Process xendfile, xendfile-tray -ErrorAction SilentlyContinue
Get-Content "$env:APPDATA\Xendfile\tray.log" -Tail 50
Get-Content "$env:APPDATA\Xendfile\core.log" -Tail 50
```

If the process is running but the icon is missing, open the `^` notification overflow area. The companion restores its icon automatically after Explorer restarts. To launch it manually:

```powershell
Start-Process "$env:LOCALAPPDATA\Xendfile\xendfile-tray.exe"
```

If Windows Firewall denied the original prompt, allow `xendfile.exe` on private networks in **Windows Security → Firewall & network protection → Allow an app through firewall**.

## Platform locations

| Platform | Application | Configuration and identity | Received files | Startup |
|---|---|---|---|---|
| macOS | `~/Applications/Xendfile.app` | `~/Library/Application Support/Xendfile` | `~/Downloads/Xendfile` | `~/Library/LaunchAgents/io.github.lalomorales22.xendfile.plist` |
| Linux | `~/.local/bin/xendfile` and `xendfile-tray` | `${XDG_CONFIG_HOME:-~/.config}/Xendfile` | `~/Downloads/Xendfile` | systemd user units or XDG autostart |
| Windows | `%LOCALAPPDATA%\Xendfile` | `%APPDATA%\Xendfile` | `%USERPROFILE%\Downloads\Xendfile` | per-user Startup shortcut |

Back up the configuration directory if you want to preserve the device identity and pairing relationships across a manual migration.

### Upgrading from the former UniDrop alpha

The Xendfile installer removes the former app binaries, shortcuts, and startup
entries while preserving its local identity and paired-device directory.
Xendfile uses that legacy directory automatically when a new Xendfile directory
does not yet exist. The former `UNIDROP_CONFIG_DIR`, `UNIDROP_DOWNLOAD_DIR`, and
`UNIDROP_UI_URL` overrides remain accepted when their `XENDFILE_*` replacements
are unset.

Protocol v2 retains the former `_unidrop._tcp` Bonjour service type, pairing
domain separator, and private HTTP header names strictly as wire identifiers so
v0.4 can interoperate with v0.3.x peers. They are not the current product name.

## Security model

- Each installation creates an ECDSA P-256 certificate and a random device identity.
- Pairing binds both device identities and certificate fingerprints with an HMAC proof.
- Later transfers require pinned TLS 1.3 and a device-specific 256-bit token.
- Pair attempts are rate-limited, and the one-time key rotates after successful pairing.
- Every file is offered with an exact sanitized filename and byte count before upload.
- File bytes are streamed only after approval and are written to a temporary partial file first.
- Control-panel and command-bridge mutation routes are loopback-only and require private tokens.
- Xendfile does not open router ports, invoke UPnP, or send file contents to a third party.

Read [`SECURITY.md`](SECURITY.md) for the threat model and [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) for the protocol and operating-system design.

## Network ports

| Port | Purpose | Exposure |
|---|---|---|
| `127.0.0.1:43337/tcp` | local browser control panel and authenticated command bridge | loopback only |
| `0.0.0.0:43338/tcp` | TLS 1.3 peer API | local LAN |
| `239.255.77.77:43339/udp` | multicast discovery | local LAN multicast |

Xendfile currently works between devices on the same reachable IP network. Cross-internet relay, Bluetooth bootstrap, and Wi-Fi Direct are not implemented.

## Develop

Go 1.25 or newer is required. Use a current patched Go release for production builds.

```sh
go test -race -mod=vendor ./...
go vet -mod=vendor ./...
go run .
```

Build all macOS, Linux, and Windows release targets:

```sh
./scripts/build-all.sh
```

Generated binaries go into `dist/` and are intentionally ignored by Git. The release builder creates AMD64 and ARM64 core binaries, Linux and Windows tray companions, macOS menu shells, and `dist/SHA256SUMS`. Both installers verify bundled binaries against that manifest when release artifacts are present.

## Current limitations and roadmap

- same-LAN transfers only; no internet relay
- files only; folder transfer is not implemented yet
- no Finder, Explorer, Nautilus, Dolphin, or Thunar right-click extension yet
- Xendfile is not yet distributed as developer-signed/notarized release packages
- protocol v2 remains compatible with the pre-rename v0.2 through v0.3.3 clients; v0.1 peers are intentionally ignored

Planned work includes native **Send with Xendfile** file-manager actions, signed installers, QR/PAKE pairing, streamed folder transfer, resumable chunks with final hashes, and an optional clearly separated cross-network mode.

## License and name

Xendfile is open-source software under the [Apache License 2.0](LICENSE). It is
provided **as is**, without warranties or conditions; see Sections 7 and 8 of
the license for the complete warranty disclaimer and limitation of liability.
Vendored and packaged components are listed in
[`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md).

The license does not grant permission to impersonate the original project with
the Xendfile name or logo. See [`TRADEMARKS.md`](TRADEMARKS.md) for the short
name and logo policy.

## Feedback and contributing

Issues, testing notes, and contributions are welcome at [github.com/lalomorales22/xendfile](https://github.com/lalomorales22/xendfile). When reporting discovery or tray problems, include the operating system, desktop environment, Xendfile version, whether the devices share the same subnet, and the relevant platform log excerpt.
