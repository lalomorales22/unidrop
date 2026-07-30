# Xendfile release notes

Xendfile is currently an alpha project. These notes describe source snapshots;
they are not a substitute for a signed, tagged release.

## 0.4.0 in progress

- Renamed the product from UniDrop to Xendfile after the owner's preliminary
  brand decision.
- Adopted the Apache License 2.0 and published third-party and name/logo notices.
- Renamed binaries, installers, desktop identities, release metadata, and the
  landing page while retaining v0.3 wire identifiers for compatibility.
- Added migration cleanup that removes former application files but preserves
  the existing device identity and paired-device state.

## 0.3.3 alpha

- Added native menu-bar, notification-area, and AppIndicator companions for
  macOS, Windows, and Linux.
- Added certificate-pinned pairing, receiver approval, manual peer addresses,
  and direct TLS 1.3 LAN file transfer.
- Added per-user installers and uninstallers that preserve device identity and
  received files by default.
- Added cross-platform CI, deterministic release metadata, checksums, an SPDX
  SBOM, and the offline Ed25519 release-signing workflow.

### Known limitations

- Public signed installers and automatic updates are not available yet.
- Folder transfer, resume, mobile clients, and remote/cloud transfer are not
  implemented.
- The current short pairing code has not received an independent cryptographic
  review and will be replaced or strengthened before production.
