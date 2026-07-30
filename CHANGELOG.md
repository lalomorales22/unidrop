# UniDrop release notes

UniDrop is currently an alpha project. These notes describe source snapshots;
they are not a substitute for a signed, tagged release.

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

