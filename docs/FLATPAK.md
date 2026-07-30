# Flatpak development package and distribution gate

Xendfile has a source-built, offline development manifest at
`io.github.lalomorales22.xendfile.json`. It is deliberately **not** represented as
a Flathub submission. The manifest gives Linux contributors a reviewable sandbox
and build target while the business, policy, and toolchain gates below remain
open.

## Current development design

- Application ID: `io.github.lalomorales22.xendfile`, matching the owner-approved
  product name and GitHub account. It remains a development identity until the
  repository slug and any future public Flatpak identity are deliberately
  finalized.
- Runtime/SDK branch: Freedesktop `25.08`, the current development selection as
  of July 30, 2026. A future submission must re-check and use Flathub's latest
  supported runtime at that time.
- Compiler: `org.freedesktop.Sdk.Extension.golang`. The build fails unless the
  extension reports exactly the already reviewed Go `1.26.5` toolchain.
- Source: the local repository, excluding Git metadata, build state, generated
  website output, and release artifacts.
- Dependency resolution: `GOPROXY=off`, `GOSUMDB=off`, `-mod=vendor`, and cgo
  disabled. The build cannot fetch a module or substitute an unreviewed package.
- Output: the secure core, pure-Go tray, signed-update helper, desktop launcher,
  SVG icon, and AppStream metadata, all built from source.

The dependency-free `scripts/verify-flatpak.mjs` check locks the manifest's
identity, runtime branch, offline build environment, local sources, permissions,
commands, desktop file, Metainfo, and wrapper. It is a repository invariant, not
a substitute for `flatpak-builder-lint`, `appstreamcli`, or a real sandbox run.

## Sandbox permissions

| Permission | Reason |
| --- | --- |
| `--share=network` | Same-LAN TCP/TLS transfers, UDP multicast discovery, and the loopback control panel are the product's core function. |
| `--filesystem=xdg-download/Xendfile:create` | Accepted files are written only to the dedicated Xendfile folder under the user's Downloads directory. No broad home or host access is granted. |
| StatusNotifier watcher `talk-name` entries | Register the tray item with either the KDE or Freedesktop watcher used by the current desktop. |
| StatusNotifier item `own-name` entries | Own only Xendfile's per-process tray names. No session-bus or system-bus socket is exposed. |

Opening the panel or Downloads folder currently delegates to the runtime's
`xdg-open`; notifications delegate to `notify-send` when available. A real
sandbox test must prove those commands route correctly through the desktop portal
on each target desktop. No extra host D-Bus or filesystem permission is granted
to make an unverified fallback work. Configuration remains in Flatpak's
application-specific XDG directory under
`~/.var/app/io.github.lalomorales22.xendfile/`; host configuration from a source
installation is intentionally not shared.

## Tool adoption gate

No Flatpak package, runtime, SDK, extension, builder, linter, or container image
was downloaded while adding this manifest. Before anyone downloads or enables the
build path, record all of the following in `docs/DEPENDENCY_REVIEW.md`:

1. Exact Flatpak and `flatpak-builder` versions, publisher provenance, license,
   supported update channel, and current security-advisory results.
2. Exact Freedesktop `25.08` runtime/SDK commits and their security-support state.
3. The Go SDK extension commit and proof that it contains the reviewed Go 1.26.5
   compiler rather than another patch version.
4. Exact `org.flatpak.Builder`/`flatpak-builder-lint` identity and digest, its
   transitive contents, license, and current advisories.

After that review, a Linux maintainer can use the standard builder flow from the
repository root:

```sh
node scripts/verify-flatpak.mjs
flatpak-builder --force-clean --sandbox --user --install-deps-from=flathub \
  build-flatpak io.github.lalomorales22.xendfile.json
flatpak-builder --run build-flatpak io.github.lalomorales22.xendfile.json \
  /app/bin/xendfile --version
```

The real validation must also launch the app, discover a second physical
machine, pair, send and receive through `xdg-download/Xendfile`, exercise the tray
on GNOME and KDE, confirm the no-tray fallback, inspect `flatpak info
--show-permissions`, uninstall, and prove no host files outside the declared
locations changed.

## Flathub submission gate

A submission must not be prepared or opened yet:

- Apache-2.0 is approved and declared in the AppStream metadata, and Xendfile is
  the approved product name. Those former gates are closed.
- There is no stable tagged v0.4.0 source release to replace the local `dir`
  source with an immutable archive and SHA-256.
- The reviewed builder/linter/runtime evidence and physical Linux desktop tests
  above do not exist yet.
- Flathub's requirements currently prohibit AI-generated submission pull
  requests and AI-generated or AI-assisted application code and documentation.
  Xendfile is AI-assisted, including this development manifest and documentation.
- The owner explicitly deferred Flathub submission during the current alpha.

Therefore neither this repository content nor a derivative may be presented as
Flathub-ready. The owner must obtain a written policy exception from Flathub or
wait for a compatible policy change, then have a human author and review the
submission independently. Re-check the current official
[requirements](https://docs.flathub.org/docs/for-app-authors/requirements),
[manifest guidance](https://docs.flatpak.org/en/latest/manifests.html), and
[linter](https://docs.flathub.org/docs/for-app-authors/linter) before proceeding.
