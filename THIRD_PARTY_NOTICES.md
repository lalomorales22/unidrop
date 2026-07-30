# Third-party notices

UniDrop's transfer core, browser interface, and native Windows notification-area companion use only the Go standard library and operating-system APIs. The Linux tray companion includes these vendored source modules:

| Module | Version | License | Purpose |
|---|---:|---|---|
| [`github.com/godbus/dbus/v5`](https://github.com/godbus/dbus) | v5.2.2 | BSD-2-Clause | StatusNotifierItem and DBusMenu transport |
| [`golang.org/x/sys`](https://pkg.go.dev/golang.org/x/sys) | v0.44.0 | BSD-3-Clause | Upstream D-Bus module dependency; its FreeBSD-only import is not linked into UniDrop's Linux binaries |

Their complete license texts are retained beside the vendored code in `vendor/github.com/godbus/dbus/v5/LICENSE` and `vendor/golang.org/x/sys/LICENSE`.

These modules are compiled into `unidrop-tray`; no module or package manager runs on the installed computer. See [`SECURITY.md`](SECURITY.md) for the dependency and vulnerability-review policy.

Linux AppImages also contain the AppImage type-2 runtime. The runtime is MIT
licensed and statically includes components from musl libc, libfuse, squashfuse,
zstd, and zlib under the terms identified by the upstream AppImage project. A
copy of that upstream notice is stored at `linux/AppImage-runtime-LICENSE` and
embedded at `usr/share/doc/unidrop/AppImage-runtime-LICENSE` in every AppImage.
`appimagetool` is used only while building and is not shipped inside UniDrop.
