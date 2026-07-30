# Contributing to UniDrop

Thanks for helping make private cross-platform file sharing better. UniDrop is
currently alpha software. Security, data preservation, and behavior across macOS,
Windows, and Linux take priority over feature count.

## Before opening a change

- Use an issue for a substantial feature or protocol change before investing in
  an implementation.
- Report exploitable security problems through a
  [private security advisory](https://github.com/lalomorales22/unidrop/security/advisories/new),
  not a public issue.
- Keep same-LAN transfers local, free, and usable without an account.
- Prefer the Go standard library and small auditable implementations.
- Do not add or update a dependency until its provenance, license, maintenance,
  and current CVE/advisory status are recorded in `docs/DEPENDENCY_REVIEW.md`.

## Development checks

Use the current patched Go toolchain named in `SECURITY.md`. The repository
vendors its Go dependencies, so normal builds do not need network access.

```sh
gofmt -w main.go main_test.go cmd internal
go test -mod=vendor ./...
go test -race -mod=vendor ./...
go vet -mod=vendor ./...
(cd site && npm test)
./scripts/build-all.sh
git diff --check
```

Windows contributors can run the Go checks from PowerShell. The race detector is
required where the platform toolchain supports it.

## Pull requests

Keep changes focused, explain user-visible and security effects, add negative
tests for security-sensitive behavior, and document any platform that was not
tested. Never include pairing secrets, private keys, bearer tokens, signing
credentials, filenames from real users, or production/customer data.

By contributing, you certify that you have the right to submit the work under the
project's eventual published license. A formal Developer Certificate of Origin or
contributor agreement may be adopted after owner and legal review; no such claim
is implied before that decision is made.
