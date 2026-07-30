# Dependency and build-tool review log

Every dependency or downloaded build tool must be reviewed before adoption or
upgrade. A green automated advisory check is evidence, not permission to merge an
upgrade without human review.

## 2026-07-30 baseline review

| Component | Pin | Provenance and license | Advisory result | Decision |
| --- | --- | --- | --- | --- |
| Go toolchain | 1.26.5 | Official `go.dev` release; BSD-3-Clause toolchain | Go release history confirms the July 2026 `crypto/tls` and `os` security fixes. The pinned macOS ARM64 archive SHA-256 matched the official downloads API. | Approved for build and CI use. Installer hashes are pinned for every supported OS/architecture. |
| `github.com/godbus/dbus/v5` | v5.2.2 | Upstream GitHub repository; BSD-2-Clause; vendored | OSV query returned no known vulnerability for this exact version. | Approved only for the Linux tray. |
| `golang.org/x/sys` | v0.44.0 | Go project module; BSD-3-Clause; vendored | OSV query returned no known vulnerability for this exact version. The older v0.27.0 transitively requested upstream was rejected because of GO-2026-5024. | Approved as the fixed override; supported Linux builds do not link its FreeBSD-only D-Bus import. |
| `actions/checkout` | v6.0.2 / `de0fac2e4500dabe0009e67214ff5f5447ce83dd` | GitHub-maintained; MIT | GitHub repository advisory API returned no published advisories. | Approved with immutable commit pin and `persist-credentials: false`. |
| `actions/setup-go` | v6.4.0 / `4a3601121dd01d1626a1e23e37211e3254c1c06c` | GitHub-maintained; MIT | GitHub repository advisory API returned no published advisories. | Approved with immutable commit pin, exact Go version, and cache disabled because dependencies are vendored. |
| `actions/attest` | v4.1.0 / `59d89421af93a897026c735860bf21b6eb4f7b26` | GitHub-maintained; MIT | GitHub repository advisory API returned no published advisories. | Approved only in the trusted tag release workflow with scoped OIDC and attestation permissions. |
| `actions/upload-artifact` | v7.0.1 / `043fb46d1a93c77aae656e7c1c64a875d1fc6a0a` | GitHub-maintained; MIT | GitHub repository advisory API returned no published advisories. | Approved only to pass hashed AppImages into the trusted release job, with one-day retention. |
| `actions/download-artifact` | v8.0.1 / `3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c` | GitHub-maintained; MIT | CVE-2024-42471 affects versions 4.0.0 through 4.1.2; the pinned v8.0.1 is outside the affected range. No newer repository advisory was published. | Approved only to merge the two expected AppImage artifacts into the trusted release job. |
| AppImage `appimagetool` | `continuous` commit `8c8c91f762b412a19f4e8d2c4b35afb98f2d7c81` | AppImage project; MIT; x86-64 SHA-256 `a6d71e...d13e0`, AArch64 `1b0052...007fe` | GitHub repository advisory API returned no published advisories. | Approved as a Linux CI/release build tool. Downloads remain on HTTPS and fail closed on the complete pinned hashes in `fetch-appimage-tools.sh`. |
| AppImage type-2 runtime | `continuous` commit `75849dce7cc37e4319b633df1f116ca895c71a12` | AppImage project; MIT plus bundled musl, libfuse, squashfuse, zstd, and zlib terms; x86-64 SHA-256 `1cc49b...aebbf`, AArch64 `7d5d77...81372` | GitHub repository advisory API returned no published advisories. | Approved only as the verified runtime prepended to each AppImage; its upstream license notice is embedded in every package. |

The macOS icon and development DMG builders use only tools shipped with macOS
and Xcode Command Line Tools (`qlmanage`, `sips`, `iconutil`, `lipo`, `plutil`,
`codesign`, and `hdiutil`). No package or executable is downloaded for this path.

Sources checked: the Go release history and downloads JSON, the Go vulnerability
database/OSV API, the upstream action release tags and immutable Git objects, each
action and AppImage repository's license, upstream asset digests, and GitHub's
repository security-advisory API.

The action review intentionally excludes unpinned major-version references. When
Dependabot proposes an action update, resolve the new tag to its immutable commit,
repeat this review, update this table, and only then change the workflow pin.

## Pending Flatpak toolchain — not approved for download

The development manifest names the following future build environment, but none
of it was downloaded, enabled in CI, or approved for release use on July 30,
2026. Branch names are not immutable supply-chain pins.

| Component | Development selection | Missing evidence | Decision |
| --- | --- | --- | --- |
| Flatpak and `flatpak-builder` | Unselected host versions | Exact version/package provenance, license inventory, maintainer status, current CVEs/advisories, and integrity mechanism | Block download and use. |
| Freedesktop runtime and SDK | `25.08` branch | Exact commits, supported/EOL state, contents/SBOM, current advisories, and trusted remote summary/signature verification | Block download and use. |
| Go SDK extension | `org.freedesktop.Sdk.Extension.golang` | Exact commit, license/content inventory, current advisories, trusted remote verification, and proof its compiler is Go 1.26.5 | Block download and use; the manifest also fails closed on any other compiler version. |
| `org.flatpak.Builder` and `flatpak-builder-lint` | Unselected | Exact image/ref and digest, publisher provenance, transitive contents, licenses, current advisories, and isolation expectations | Block download and use. |

Complete the review checklist in `docs/FLATPAK.md` before changing any of these
decisions. The dependency-free static manifest verifier is not a replacement for
the official builder, linter, runtime, or physical sandbox tests.
