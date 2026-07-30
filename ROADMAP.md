# Xendfile public roadmap

Xendfile is **alpha software**. It is useful for direct same-network transfers, but
its installers are not yet signed or notarized and its protocol has not received
an independent security review. Do not represent the current release as audited,
production-ready, or suitable for regulated deployment.

## Now: trusted desktop distribution

- mandatory macOS, Windows, and Linux CI
- reproducible AMD64 and ARM64 builds with checksums, SBOMs, and provenance
- native signed installers, macOS notarization, and secure updates
- authentic cross-platform beta testing and independent protocol review

## Next: exceptional local transfer

- trusted-device management and secure OS credential storage
- reviewed short-code and QR pairing
- final content hashes, resumable large transfers, and safe folder transfer
- file-manager integrations, accessibility work, and mobile prototypes

## Later: optional private cloud service

- direct-first remote discovery with an opaque end-to-end encrypted relay
- personal and team device directories, policy, audit events, and billing
- production security review, privacy operations, support, and measured launch

The detailed dependency-ordered execution plan is maintained in `tasks.md`.
Dates and pricing will be published only after engineering evidence, owner
decisions, and customer research make them credible.
