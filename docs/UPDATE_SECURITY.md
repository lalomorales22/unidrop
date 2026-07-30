# Signed update metadata design

Automatic update installation is not enabled in the alpha client. The
dependency-free `cmd/xendfile-update` helper implements signed metadata acceptance,
non-executable artifact staging, private update preferences, and a crash-safe
replacement/rollback engine. Native package signature or notarization checks and
package-specific installer wiring must still be completed before any preference
can activate unattended installation.

## Release manifest

`scripts/release-metadata.mjs` creates canonical JSON containing schema version,
product version, immutable Git tag and source commit, protocol version, minimum
compatible client version, publication and expiration timestamps, a signed
revoked-version list, and a sorted artifact list. Each artifact binds its
URL, component, platform, architecture, byte size, and SHA-256 digest.

`cmd/xendfile-release` signs the exact manifest bytes with Ed25519. Its detached
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

Checking must not require administrator/root access. The chosen native package
format may request elevation only at the final OS-controlled installation step,
with the reason shown before consent.

## Preferences and activation boundary

The helper stores `notify-only` (the default), `automatic`, or `disabled` in a
private per-user JSON file. Writes use a synchronized temporary file and a
two-generation rename so an interrupted preference change restores the last
complete value. Unknown fields, duplicate fields, invalid values, oversized
state, symlinks, and group/other-readable POSIX files fail closed.

```sh
go run ./cmd/xendfile-update preference get
go run ./cmd/xendfile-update preference set notify-only
go run ./cmd/xendfile-update preference set automatic
go run ./cmd/xendfile-update preference set disabled
```

Selecting `automatic` records intent only. It does not install, execute, or grant
privileges to an artifact, and it never bypasses the open native-verification and
installer-integration gates.

## Crash-safe replacement primitive

The updater package includes a replacement primitive for the future native
installer integration. After an OS-specific installer independently verifies its
native package, the primitive accepts the signed manifest size and SHA-256:

1. Recover any earlier journal before starting new work.
2. Re-hash the staged file, copy it to the installed file's directory, re-hash
   while copying, preserve executable permissions, and synchronize it.
3. Write a private atomic journal before renaming the working installation.
4. Move the working file to `.previous.pending`, activate the candidate, and
   record every durable phase transition.
5. Run a caller-supplied health check against the activated path.
6. On success, retain the previous file as `.last-working`; on any earlier or
   failed-health phase, restore the previous file and leave user data alone.

Recovery verifies the journaled hashes before removing or renaming a managed
file. It rejects colliding paths, altered candidates, changed pending backups,
symlinks, public journal permissions on POSIX, unknown phases, and malformed
state. `xendfile-update recover --journal FILE` provides an idempotent recovery
entry point for the eventual native installers.

## Required abuse tests

Coverage must include unsigned and non-canonical metadata, unknown/rotated/revoked
keys, manifest and artifact hash changes, wrong OS/architecture, protocol
downgrade, expired/future metadata, rollback and freeze attempts, redirect and URL
confusion, oversized responses, partial/corrupt downloads, offline startup,
interrupted preference writes and every replacement phase, health-check failure,
tampered recovery state, idempotent recovery, and preservation of the previous
working version.

## Manual staging during development

The helper is intentionally separate from the running client until native
package verification and installer integration are complete. A developer with
the reviewed release public key can exercise the full metadata and
artifact-verification path:

```sh
go run ./cmd/xendfile-update stage \
  --manifest-url https://github.com/lalomorales22/unidrop/releases/download/vVERSION/xendfile-VERSION-manifest.json \
  --public-key release/release-public.pem
```

Success writes a private, non-executable staging file and advances the local
highest-accepted-version record. It does not execute the file, request elevated
privileges, replace the installed client, or remove the last working version.
