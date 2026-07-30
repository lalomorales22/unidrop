# Xendfile security model

Xendfile v0.3.3 is designed for direct file sharing between computers on the same trusted or semi-trusted local network. It encrypts transfers, requires an explicit first pairing, and asks the receiver before accepting file bytes by default. It is not yet an audited replacement for AirDrop in a hostile enterprise network.

## Protections implemented

- TLS 1.3 only for all peer traffic
- ECDSA P-256 device certificate generated locally; private key never leaves the device
- exact SHA-256 certificate pinning after pairing
- random 64-bit one-time pairing key with HMAC channel binding
- pairing proof binds the receiver certificate, sender ID, sender certificate, nonce, and return token
- eight pairing attempts per source IP per five-minute window
- automatic pairing-key rotation after successful use
- independent random 256-bit bearer tokens in each direction
- only token hashes retained by the receiving side
- trust store and private key created with user-only file permissions where the OS honors POSIX modes
- control API bound to loopback and mutating requests protected against ordinary cross-origin browser requests
- command bridge bound to loopback and authenticated with a random 256-bit user-only token
- Linux tray mutations and shutdown authenticated with that same user-only control token
- Windows notification-area mutations and shutdown authenticated with that same user-only control token
- short-lived transfer offers bound to sender identity, safe filename, and exact content length
- explicit **Accept/Decline** receiver approval by default, with receiving-off and trusted-device modes
- macOS local-network purpose string and LaunchAgent-to-bundle association
- native WebKit shell loads only the loopback UI; privileged shell messages require the exact loopback host and port
- no LAN traffic sent through environment-configured HTTP proxies
- filename path components, control characters, and Windows-reserved separators removed
- incomplete receive files removed; existing files get a unique name
- 20 GiB size limit and streaming I/O to prevent whole-file memory exhaustion
- no UPnP, router port forwarding, cloud storage, analytics, or telemetry

## Information visible on the LAN

Discovery announcements contain the device ID, display name, operating system family, peer port, and public certificate fingerprint. Anyone on the same multicast-capable LAN may observe this metadata. File names and file contents are carried only inside the paired TLS channel.

## Known limitations

- The application and installers are not yet code-signed or notarized.
- The cryptographic design and implementation have not received an independent audit.
- The pairing key provides 64 bits of offline guessing resistance. A future short human code should use a reviewed PAKE instead of reducing this key.
- A paired, compromised computer can send files until its trust record is removed. A trust-management screen is planned.
- Received files are not malware-scanned; the operating system's normal protections still apply.
- Availability is not guaranteed against a hostile LAN peer that floods the HTTPS port or discovery group.
- Tokens are protected by OS user permissions, not a hardware keystore/keychain yet.
- Transfer completion currently relies on TLS/TCP integrity and byte count; an explicit final content digest is planned.
- macOS receiver approvals appear directly in the native menu-bar popover. Linux and Windows highlight pending requests in their native status shells and open the local approval panel.
- A Linux tray icon requires the desktop session to provide a StatusNotifier/AppIndicator host. The application-menu and browser panel remain the fallback.

## Dependency and CVE policy

The transfer core and Windows Win32 shell import only Go standard-library packages; the Windows shell calls `user32.dll`, `shell32.dll`, `kernel32.dll`, and `gdi32.dll` supplied by the operating system. The macOS shell links only Apple’s system AppKit, Foundation, and WebKit frameworks. The Linux tray pins `github.com/godbus/dbus/v5` v5.2.2 (BSD-2-Clause) and its `golang.org/x/sys` v0.44.0 module dependency. Both are vendored, so no package manager or third-party download runs during installation or application startup. The current Linux tray binary links `godbus`; the upstream `x/sys` import is FreeBSD-only and is not linked into Xendfile's supported Linux targets.

The exact module versions were queried against OSV on July 30, 2026, with no known vulnerabilities returned. The older transitive `x/sys` v0.27.0 pin was explicitly rejected because it is affected by `GO-2026-5024`; Xendfile overrides it with the fixed v0.44.0 release even though that advisory's vulnerable symbol is Windows-only.

Installers pin the official Go 1.26.5 toolchain and the SHA-256 values published by `go.dev` for macOS, Linux, and Windows on AMD64 and ARM64. That release includes July 2026 security fixes in `crypto/tls` and `os`; older 1.26 releases fixed additional issues in `crypto/x509`, `net/http`, and related packages. Before updating the compiler or either Linux module, review the [official release history](https://go.dev/doc/devel/release), query the [Go vulnerability database](https://pkg.go.dev/vuln/), and rebuild `vendor/` from the reviewed module graph.

The implemented controls and their negative or abuse-case tests are mapped in
[`docs/SECURITY_TEST_MATRIX.md`](docs/SECURITY_TEST_MATRIX.md). This evidence does
not replace the open independent protocol and cryptographic review.

## Reporting

Do not post exploitable security details in a public issue. Submit a
[private GitHub security advisory](https://github.com/lalomorales22/xendfile/security/advisories/new)
and include the affected version, platform, reproduction steps, impact, and a
safe way to contact you. The public project contact is the repository's
[issue tracker](https://github.com/lalomorales22/xendfile/issues) for
non-sensitive bugs and questions.

The project aims to acknowledge private reports within three business days. That
target is not a guarantee or a claim of continuous monitoring while Xendfile is
maintained as alpha software.
