## What changed

Describe the user-visible result and why it is needed.

## Security and privacy

- [ ] No secrets, real user filenames/paths, private keys, tokens, or customer data are included.
- [ ] Security-sensitive behavior has negative or abuse-case tests.
- [ ] New or updated dependencies have a recorded license, provenance, maintenance, and CVE/advisory review.
- [ ] Local-only use remains available without an account or cloud dependency.

## Verification

- [ ] `go test -mod=vendor ./...`
- [ ] `go test -race -mod=vendor ./...` where supported
- [ ] `go vet -mod=vendor ./...`
- [ ] Site and installer checks pass where applicable
- [ ] `git diff --check`
- [ ] Tested platforms and architectures are listed below

Tested platforms/architectures:

Known limitations or untested paths:
