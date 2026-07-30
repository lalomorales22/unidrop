# Signed update metadata design

Automatic update installation is not enabled in the alpha client. This document
defines the fail-closed format and acceptance rules that must be implemented and
tested before it is enabled.

## Release manifest

`scripts/release-metadata.mjs` creates canonical JSON containing schema version,
product version, immutable Git tag and source commit, protocol version, minimum
compatible client version, and a sorted artifact list. Each artifact binds its
URL, component, platform, architecture, byte size, and SHA-256 digest.

`cmd/unidrop-release` signs the exact manifest bytes with Ed25519. Its detached
JSON envelope identifies the algorithm and public-key fingerprint and repeats the
manifest SHA-256. GitHub OIDC/Sigstore attestations provide separate build
provenance and SBOM evidence; neither replaces the offline-controlled update key.

## Key rotation

Clients ship with at least one trusted public-key fingerprint. Rotation metadata
must name the old and new keys, activation time, overlap window, and minimum
client version. During the overlap it must be signed by the existing trusted key
and the new key. Removing the last trusted key requires an application update or
an explicitly documented threshold recovery procedure; a server response alone
must never replace all trust anchors.

## Client acceptance sequence

Before showing or installing an update, the client must:

1. Fetch metadata over HTTPS to a newly created temporary location with size and
   time limits.
2. Reject unknown schemas, algorithms, keys, duplicate JSON fields, malformed
   versions, non-HTTPS URLs, redirects outside approved release hosts, and
   metadata outside its validity window.
3. Verify the detached signature before using any manifest field.
4. Require an exact product, OS, architecture, component, protocol, and minimum
   compatible-version match.
5. Reject any version at or below the highest accepted version unless separately
   signed recovery metadata explicitly authorizes it.
6. Download to a non-executable temporary path, enforce the declared byte size,
   compute SHA-256 while streaming, and reject any mismatch.
7. Verify OS-native package signatures/notarization and GitHub provenance in
   addition to the manifest hash.
8. Preserve the currently working installation until the replacement launches
   and passes a health check. On failure, restore it without altering user data.

Update preferences will be `notify` by default, `automatic`, or `disabled`.
Checking must not require administrator/root access. The chosen native package
format may request elevation only at the final OS-controlled installation step,
with the reason shown before consent.

## Required abuse tests

Coverage must include unsigned and non-canonical metadata, unknown/rotated/revoked
keys, manifest and artifact hash changes, wrong OS/architecture, protocol
downgrade, expired/future metadata, rollback and freeze attempts, redirect and URL
confusion, oversized responses, partial/corrupt downloads, offline startup,
interrupted replacement, health-check failure, and preservation of the previous
working version.
