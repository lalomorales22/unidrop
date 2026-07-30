# UniDrop security model

UniDrop v0.2 is designed for direct file sharing between computers on the same trusted or semi-trusted local network. It encrypts transfers, requires an explicit first pairing, and asks the receiver before accepting file bytes by default. It is not yet an audited replacement for AirDrop in a hostile enterprise network.

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
- short-lived transfer offers bound to sender identity, safe filename, and exact content length
- explicit **Accept/Decline** receiver approval by default, with receiving-off and trusted-device modes
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
- Receiver approvals currently appear in the local control panel and an OS notification; the native tray/menu-bar approval surface is not implemented yet.

## Dependency and CVE policy

The application imports only Go standard-library packages. No package manager runs at application startup.

Installers pin the official Go 1.26.5 toolchain and the SHA-256 values published by `go.dev` for macOS, Linux, and Windows on AMD64 and ARM64. That release includes July 2026 security fixes in `crypto/tls` and `os`; older 1.26 releases fixed additional issues in `crypto/x509`, `net/http`, and related packages. Before updating the pinned compiler, review the [official release history](https://go.dev/doc/devel/release) and run Go's vulnerability tooling against the final module.

## Reporting

Until a private disclosure address is established, do not post exploitable security details in a public issue. Contact the project owner directly and include the affected version, platform, reproduction steps, and impact.
