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

The macOS icon and development DMG builders use only tools shipped with macOS
and Xcode Command Line Tools (`qlmanage`, `sips`, `iconutil`, `lipo`, `plutil`,
`codesign`, and `hdiutil`). No package or executable is downloaded for this path.

Sources checked: the Go release history and downloads JSON, the Go vulnerability
database/OSV API, the upstream action release tags and immutable Git objects, each
action repository's license, and GitHub's repository security-advisory API.

The action review intentionally excludes unpinned major-version references. When
Dependabot proposes an action update, resolve the new tag to its immutable commit,
repeat this review, update this table, and only then change the workflow pin.
