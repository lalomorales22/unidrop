# Xendfile release notes

## v0.3.3 source alpha

Xendfile v0.3.3 is a source-based alpha for direct same-LAN file transfer across
macOS, Windows, and Linux. The website currently links to the immutable reviewed
source snapshot at commit `0f4295268e704cadf40f9b892e19115a672960cd`, not to a
signed native release.

### Included

- TLS 1.3 peer transport with exact paired-certificate pinning.
- Mutual one-time-key pairing and receiver-controlled incoming offers.
- UDP multicast discovery plus saved manual-address fallback.
- Native macOS menu-bar, Linux StatusNotifier/AppIndicator, and Windows tray
  companions.
- Per-user installers, lifecycle commands, and data-preserving upgrade/uninstall
  behavior.
- AMD64 and ARM64 source builds for all three desktop operating systems.
- Dependency-free landing page and documented privacy/security limitations.

### Important limitations

- The source snapshot is immutable and has a published byte size and SHA-256, but
  it is **not** publisher-signed, notarized, or a substitute for the planned
  v0.4.0 release.
- macOS and Windows can display trust warnings for unsigned locally built apps.
- Transfers require two directly reachable computers on the same LAN. There is
  no relay, account, or cross-internet mode.
- Files only: folders, resumable chunks, mobile clients, and file-manager context
  menus are not implemented.
- The pairing and transfer protocol has extensive automated abuse tests but has
  not received the required independent cryptographic/security review.

### Verify the website snapshot

Expected archive metadata:

```text
Commit:  0f4295268e704cadf40f9b892e19115a672960cd
Bytes:   3205035
SHA-256: b3219892170a3d6cdc413edc6d1126588e844a1b1c5753a07e447c19f711b4d5
```

After downloading, compare the locally computed SHA-256 before extracting it:

```sh
shasum -a 256 unidrop-0f4295268e704cadf40f9b892e19115a672960cd.zip
```

PowerShell:

```powershell
Get-FileHash -Algorithm SHA256 .\unidrop-0f4295268e704cadf40f9b892e19115a672960cd.zip
```

GitHub currently names this source archive after the unchanged repository slug,
`unidrop`. Its contents and product identity are Xendfile.

The hash proves that the bytes match the documented snapshot. It does not prove
publisher identity because this alpha archive has no Xendfile signature.

## v0.4.0 trust and distribution work

Apache-2.0 licensing and the Xendfile brand decision are now recorded. The next
release remains gated on Apple and Windows signing identities,
notarization/package trust, clean-machine tests, signed immutable release
metadata, and public-site approval. The active, evidence-backed status is
maintained in [`tasks.md`](../tasks.md).
