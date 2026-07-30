# ADR 0001: Remove the checked-in portable macOS menu binary

- Status: accepted
- Date: 2026-07-30

## Context

The repository previously carried a universal menu-bar executable so a source
installer could run without Apple's Swift compiler. Its checksum detected file
changes, but did not establish a reproducible source relationship or publisher
identity. The executable also embedded the retired UniDrop brand and could not
truthfully ship as Xendfile.

## Decision

Remove the checked-in portable binary. `macos/XendfileMenu.swift` is now the
only source for the menu shell. `scripts/build-macos-menu.sh` builds both
architectures on macOS, and the package builder combines those reviewed outputs.

The source installer uses a bundled menu artifact only when it is present in a
release bundle and covered by that bundle's `SHA256SUMS`. Otherwise it compiles
the Swift source with the installed Apple toolchain and clearly reports the
requirement. No opaque prebuilt menu binary is accepted from the repository.

## Consequences

Developers installing directly from source need Xcode Command Line Tools unless
the exact source archive also contains reviewed release binaries. Normal users
should eventually receive a Developer ID signed and notarized package built from
the tagged source. Until signing access exists, macOS output remains a visibly
unsigned development artifact and is not approved for public distribution.
