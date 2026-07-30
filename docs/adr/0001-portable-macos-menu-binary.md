# ADR 0001: Retain the portable macOS menu binary during alpha

- Status: accepted for alpha; must be replaced as a v0.4.0 release gate
- Date: 2026-07-30

## Context

The source installer must work for Mac users who do not have Xcode or the Swift
compiler. The menu shell is written in one auditable Swift file and links only
Apple system frameworks, but compiling it on each user's computer would otherwise
require a multi-gigabyte developer-tool installation.

## Decision

Retain `macos/UniDropMenu.universal` temporarily. The installer accepts it only if
its hard-coded SHA-256 equals
`d403bd6a4d8bd0dc91619223cae7dac4902e5bbea3f4449db7c0541ea966bede`.
`scripts/build-macos-menu.sh` reproducibly rebuilds both architectures from
`macos/UniDropMenu.swift` on a reviewed Mac toolchain, and CI rebuilds that source
to detect compilation failures.

This checksum proves repository integrity, not publisher identity. The portable
binary is ad-hoc signed only for local source installation and is not a trusted
public release artifact.

## Exit criteria

The v0.4.0 release pipeline must build the menu shell from the tagged source,
combine the reviewed architectures, sign every nested executable and the final
app with Developer ID under Hardened Runtime, notarize and staple the package,
and publish checksums, SBOM, and provenance. After clean-machine verification,
the checked-in portable binary will be removed or replaced by a release download
whose publisher signature and digest are verified before installation.
