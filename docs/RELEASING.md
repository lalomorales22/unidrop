# UniDrop release procedure

UniDrop releases are fail-closed. A tag must not be created until CI, native
packaging, signing/notarization, clean-machine tests, release notes, and the owner
approval gate are complete for that version.

## One-time repository setup

1. Enable branch rules for `main`: pull requests, the `Required CI` check, no
   force pushes, no deletions, and restricted bypass permission.
2. Add a tag rule for `v*` that prevents update and deletion after creation.
3. Generate the Ed25519 release-manifest key offline:

   ```sh
   go run ./cmd/unidrop-release keygen \
     --private unidrop-release-private.pem \
     --public release/release-public.pem
   ```

4. Store the base64-encoded private PEM as the protected
   `UNIDROP_RELEASE_SIGNING_KEY_B64` Actions secret. Never commit or upload the
   private PEM. Keep an offline recovery copy under two-person or owner-approved
   control.
5. Commit only `release/release-public.pem`, record its SHA-256 fingerprint in the
   website and release documentation, and rehearse key rotation before relying on
   automatic updates.
6. Configure Apple and Microsoft signing identities only in the trusted release
   environment. Fork and pull-request workflows must never receive them.

## Prepare a release

1. Update `internal/version/VERSION`. The core, trays, installers, website, SBOM,
   and manifest all read this source of truth.
2. Update the changelog/release notes, compatibility floor, security limitations,
   third-party notices, dependency review, and platform support evidence. Complete
   the owner and qualified-reviewer record in
   [`EXPORT_COMPLIANCE.md`](EXPORT_COMPLIANCE.md) before publishing encryption
   software or answering App Store Connect's encryption questions.
3. Run the full local checks:

   ```sh
   go test -mod=vendor ./...
   go test -race -mod=vendor ./...
   go vet -mod=vendor ./...
   (cd site && npm test)
   ./scripts/build-all.sh
   node scripts/verify-release.mjs
   git diff --check
   ```

   On macOS, set `UNIDROP_BUILD_DEVELOPMENT_DMG=1` to also produce and verify the
   universal development DMG. That artifact is ad-hoc signed for structural CI
   checks only; it is not approved for public distribution.

   On Linux, fetch the reviewed, hash-pinned AppImage tools into a temporary
   directory and pass their paths to `scripts/build-linux-appimage.sh`. CI runs
   this process natively and reproducibly for both AMD64 and ARM64; the trusted
   tag workflow carries only those exact packages into the signed manifest.

4. Build and test the signed native packages on clean macOS, Windows, and Linux
   machines. Verify install, launch, discovery, pairing, sending, receiving,
   upgrade, rollback rejection, uninstall, and reinstall.
5. Confirm Gatekeeper/notarization, SmartScreen/App Control, AppImage/Flatpak
   checks, artifact checksums, signature verification, SBOM, and provenance.
6. Obtain explicit owner launch approval. Create a signed annotated tag only from
   the reviewed commit, then push that tag. The trusted tag workflow builds,
   signs, attests, and creates the GitHub Release.

## Independent verification

Do not accept the release job as the only verification environment. On a clean
machine, download `SHA256SUMS`, the versioned manifest and signature, the committed
release public key, and the target artifact. Verify all hashes, then run:

```sh
go run ./cmd/unidrop-release verify \
  --public release/release-public.pem \
  --manifest dist/unidrop-VERSION-manifest.json \
  --signature dist/unidrop-VERSION-manifest.sig.json
gh attestation verify ARTIFACT --repo lalomorales22/unidrop
```

Compare the manifest's `sourceCommit` with the immutable tag and retain the
verification record with the release evidence.

## Rollback

Never move or overwrite a published tag and never silently replace release
assets. If a release is defective:

1. Mark it affected in the release notes and website without deleting evidence.
2. Disable automatic offering of that version in signed update metadata.
3. Publish a new patch version from a reviewed fix, signed with the current valid
   key, and identify the affected version explicitly.
4. Preserve the last working installation during update and provide documented
   recovery instructions. Do not bypass downgrade protection; publish narrowly
   scoped, signed recovery metadata if a downgrade is truly required.

## Signing-key compromise

On suspected compromise, stop releases and update publication immediately.
Preserve logs and key fingerprints, remove the affected secret from CI, revoke the
Apple/Microsoft credentials through their providers, and communicate the affected
key ID and version range. Generate a new key offline, publish a rotation statement
signed by every still-trusted old key when possible, and require clients to accept
the new key only through the documented threshold/rotation mechanism. Reissue
clean artifacts under a new version after incident review; never replace old
assets in place.
