# UniDrop local-client privacy notice

Last updated: July 30, 2026

This notice describes the current open-source UniDrop alpha client. It does not
describe a future hosted account, relay, billing, waitlist, or support service.
Those services do not exist in the current product.

## What the client does

UniDrop discovers other UniDrop clients on your private local network and sends
files directly between devices after pairing and receiver approval. A UniDrop
server, account, or cloud-storage service is not involved in that transfer.

## Data stored on your computer

The client stores its device name and identifier, device certificate and private
key, paired-device identifiers and certificate fingerprints, pairing tokens,
manual peer addresses, receive preference, download location, and recent local
transfer status. Received files are stored in the download location you select
or your operating system's Downloads folder.

This information remains on the computer where UniDrop is installed. Uninstall
preserves device identity and pairing data by default so a reinstall does not
silently break trust. The uninstaller's explicit remove-user-data option deletes
that local client state. Received files are never removed by the uninstaller.

## Network information

Nearby devices can see the device name, device identifier, operating-system
family, listening port, protocol version, and certificate fingerprint needed for
discovery and pairing. Paired devices exchange filenames, file sizes, transfer
status, and the encrypted transfer stream. People who control your local network
may observe connection metadata such as IP addresses, ports, timing, and traffic
volume, but TLS protects file contents in transit.

## Data UniDrop does not collect

The current client contains no analytics, advertising, telemetry, crash-report
upload, cloud file storage, account tracking, or background contact with a
UniDrop-operated service. The source installer may download an official Go
toolchain from `go.dev` when a compatible compiler or bundled binary is absent;
that request is governed by the Go website's privacy practices.

The GitHub source archive, repository, issues, and releases are provided by
GitHub and are governed by GitHub's own terms and privacy statement.

## Questions and security reports

General project questions may be opened in the public GitHub repository. Send
security-sensitive reports privately to `security@southbayitsolutions.com` as
described in `SECURITY.md`.

This notice will be updated before any hosted service begins collecting account,
waitlist, relay, billing, support, or operational data.
