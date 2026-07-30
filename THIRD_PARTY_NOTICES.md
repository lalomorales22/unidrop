# Third-party notices

UniDrop's transfer core and browser interface use only the Go standard library. The Linux tray companion includes these vendored source modules:

| Module | Version | License | Purpose |
|---|---:|---|---|
| [`github.com/godbus/dbus/v5`](https://github.com/godbus/dbus) | v5.2.2 | BSD-2-Clause | StatusNotifierItem and DBusMenu transport |
| [`golang.org/x/sys`](https://pkg.go.dev/golang.org/x/sys) | v0.44.0 | BSD-3-Clause | Upstream D-Bus module dependency; its FreeBSD-only import is not linked into UniDrop's Linux binaries |

Their complete license texts are retained beside the vendored code in `vendor/github.com/godbus/dbus/v5/LICENSE` and `vendor/golang.org/x/sys/LICENSE`.

These modules are compiled into `unidrop-tray`; no module or package manager runs on the installed computer. See [`SECURITY.md`](SECURITY.md) for the dependency and vulnerability-review policy.
