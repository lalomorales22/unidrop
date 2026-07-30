# UniDrop Startup Execution Plan

This is the authoritative, forward-only plan for turning UniDrop from a strong
cross-platform alpha into a trustworthy open-source product and sustainable SaaS
business. Complete phases in order. Security work begins in Phase 1 and continues
throughout; Phase 4 is the formal public-SaaS security gate.

## Mission

Build the fastest, simplest, most trustworthy way to move files between macOS,
Windows, Linux, and eventually mobile devices.

UniDrop must remain local-first:

- Same-LAN transfers stay free, direct, and available without an account.
- File contents and device private keys never become visible to UniDrop servers.
- The paid service may provide identity, remote discovery, encrypted rendezvous,
  policy, billing, and an opaque encrypted relay.
- Direct connections are always preferred over relaying.
- Telemetry is opt-in, minimal, documented, and never includes filenames, paths,
  file contents, pairing secrets, private keys, or bearer tokens.
- New packages and build tools must receive a CVE/advisory review before download
  or adoption. Prefer standard-library and small, auditable implementations.
- Cross-platform behavior is a release requirement, not a future cleanup task.

## Current Baseline

- [x] Go secure core supports macOS, Windows, and Linux.
- [x] TLS 1.3 peer transport, certificate pinning, mutual pairing, and receiver
  approval are implemented.
- [x] Automatic LAN discovery and persistent manual-address fallback exist.
- [x] macOS menu-bar, Linux AppIndicator/StatusNotifier, and Windows tray shells
  exist.
- [x] CLI supports peer listing, file sending, starting, and stopping.
- [x] A dark responsive landing page exists under `site/`.
- [ ] The landing page is committed to the public repository.
- [ ] The project has an explicit open-source license.
- [ ] GitHub Releases contain native installers and checksums.
- [x] Automated cross-platform CI and release workflows exist.
- [ ] macOS and Windows artifacts are code-signed; macOS is notarized.
- [ ] The protocol and cryptographic implementation have received an independent
  review.
- [ ] The SaaS control plane, relay, accounts, teams, billing, and operations do
  not exist yet.

## Product Editions to Validate

| Edition | Proposed value |
| --- | --- |
| UniDrop Free | Open-source, unlimited same-network transfers, tray apps, and CLI |
| UniDrop Pro | Remote E2EE transfers, resumable folders, and a personal device directory |
| UniDrop Teams | Team directory, roles, device revocation, policies, and audit events |
| UniDrop Enterprise | SSO/SCIM, managed deployment, compliance controls, and priority support |

Pricing is a hypothesis until Phase 6 customer interviews validate willingness to
pay. Do not weaken the free local product merely to force conversion.

## Global Engineering Definition of Done

Every completed engineering task must satisfy the applicable items below:

- [x] Code is formatted and passes `git diff --check`.
- [ ] Unit, integration, race, and platform-specific tests pass where applicable.
- [ ] Security-sensitive behavior has negative and abuse-case tests.
- [x] Dependencies and downloaded tools have pinned versions, integrity checks,
  license review, and CVE/advisory review recorded in the repository.
- [x] User-facing behavior and troubleshooting steps are documented.
- [ ] Upgrade, rollback, and uninstall behavior preserve user data as documented.
- [ ] No secrets, signing credentials, production tokens, or customer information
  appear in source, logs, fixtures, artifacts, or CI output.
- [x] macOS ARM64/AMD64, Windows ARM64/AMD64, and Linux ARM64/AMD64 are either
  verified or explicitly blocked from release.
- [x] Relevant architecture, threat model, and security limitations are updated.

---

# Phase 1 — v0.4.0 Trust and Distribution

## Objective

Turn the current source-based alpha into a product normal people can safely
download, install, verify, update, and remove.

## 1.1 Repository, license, and brand foundation

- [ ] Commit the complete `site/` directory without overwriting unrelated work.
- [ ] Choose the client license with owner approval and legal review when needed.
  Apache-2.0 is the initial recommendation because it is permissive and includes
  an explicit patent grant; this is a decision, not an automatic selection.
- [ ] Add `LICENSE` and ensure every vendored dependency is represented in
  `THIRD_PARTY_NOTICES.md`.
- [ ] Document that the UniDrop name and logo are not granted for confusing or
  impersonating distributions, if that is the owner's chosen trademark policy.
- [ ] Search relevant trademark records and domain availability before spending
  materially on the brand.
- [ ] Add a public project contact, private security-reporting address, Code of
  Conduct, contributing guide, issue templates, and pull-request template.
- [ ] Configure the GitHub repository homepage to point to the production landing
  page.
- [x] Add a public roadmap and clearly label alpha/beta/production maturity.

## 1.2 Continuous integration baseline

- [x] Add GitHub Actions for Go tests on macOS, Windows, and Linux.
- [x] Run `go test ./...`, `go test -race ./...` where supported, `go vet ./...`,
  formatting checks, installer linting, and website tests.
- [x] Add protocol compatibility tests covering the oldest supported version.
- [x] Add clean install, in-place upgrade, stop/start, uninstall, and reinstall
  smoke tests for each operating system.
- [ ] Test LAN discovery and manual pairing across at least macOS↔Linux,
  macOS↔Windows, and Windows↔Linux.
- [x] Pin third-party GitHub Actions to immutable commit SHAs after advisory and
  publisher review.
- [x] Use least-privilege workflow permissions and isolate signing workflows from
  untrusted pull requests.
- [x] Enable Dependabot or an equivalent advisory monitor for Go modules and
  Actions, while retaining manual CVE review before upgrades.

## 1.3 Reproducible release pipeline

- [x] Create a version source of truth used by the core, trays, installers,
  landing page, and release metadata.
- [x] Build AMD64 and ARM64 artifacts for all three desktop platforms.
- [x] Produce a versioned manifest containing artifact URL, platform,
  architecture, protocol version, minimum compatible version, size, and SHA-256.
- [ ] Generate `SHA256SUMS` and sign the release manifest.
- [x] Generate an SBOM for every release.
- [ ] Generate GitHub build-provenance and SBOM attestations.
- [ ] Create immutable Git tags and GitHub Releases with human-readable notes.
- [ ] Verify release artifacts from a clean machine rather than only inside CI.
- [x] Document a release rollback and signing-key compromise procedure.

## 1.4 Native packaging and trust

### macOS

- [ ] Enroll the owner/company in the Apple Developer Program.
- [x] Replace the checked-in portable menu binary with a reproducible signed build
  artifact or document why it remains necessary.
- [ ] Enable Hardened Runtime with the minimum required entitlements.
- [ ] Sign every nested executable and the final app with Developer ID.
- [ ] Create a polished universal `.dmg` or signed `.pkg`.
- [ ] Submit with `notarytool`, inspect the notary log, staple the ticket, and test
  Gatekeeper behavior on a clean Mac.
- [ ] Determine and document encryption export-compliance requirements.

### Windows

- [ ] Create a stable application identity and publisher identity.
- [ ] Prefer MSIX and Microsoft Store distribution unless a signed direct-download
  installer is demonstrably better for required shell integration.
- [ ] Ensure firewall rules are narrow, scoped to private networks, and removed on
  uninstall.
- [ ] Sign the core, tray, installer/package, and update artifacts.
- [ ] Verify clean installation without SmartScreen or App Control failures.
- [ ] Add Start menu, tray, PATH, uninstall, repair, and upgrade validation.

### Linux

- [ ] Produce an AppImage with desktop file, icon, and AppStream metadata.
- [ ] Create and lint a Flatpak manifest, then prepare a Flathub submission.
- [ ] Evaluate `.deb` and `.rpm` only after AppImage/Flatpak are reliable.
- [ ] Test GNOME, KDE Plasma, and at least one tray-host-limited desktop.
- [ ] Verify XDG paths, Wayland behavior, user services, autostart fallback, and
  clean uninstall.

## 1.5 Secure updates

- [x] Design and document a signed update-manifest format with key rotation.
- [ ] Reject unsigned manifests, hash mismatches, wrong architectures, protocol
  downgrades, expired metadata, and rollback attempts.
- [ ] Download updates to a temporary location, verify them before execution, and
  preserve the last working version for recovery.
- [ ] Never require administrator/root privileges unless the chosen package format
  explicitly requires them and the UI explains why.
- [ ] Provide automatic, notify-only, and disabled update preferences.
- [ ] Test interrupted downloads, corrupt packages, revoked releases, offline
  startup, and rollback.

## 1.6 Landing page and beta distribution

- [ ] Make the production landing page public after explicit owner approval.
- [ ] Point Mac, Windows, and Linux buttons at real release assets rather than the
  source archive.
- [ ] Detect operating system and architecture without preventing manual choices.
- [ ] Add version, file size, checksum, installation instructions, system
  requirements, release notes, privacy policy, and source links.
- [ ] Add a short product demo and authentic screenshots from all three platforms.
- [ ] Add a privacy-respecting beta/waitlist form with explicit consent and a
  published retention rule.
- [ ] Recruit at least ten beta testers covering all supported OS/architecture
  combinations and at least three mixed-platform transfer pairs.
- [ ] Resolve every release-blocking beta defect and document accepted limitations.

## Phase 1 tests

- [ ] CI is green on supported OS/architecture matrices.
- [ ] Every release asset matches its published checksum and attestation.
- [ ] Clean machines install, launch, discover, pair, send, receive, update,
  downgrade-reject, and uninstall successfully.
- [ ] macOS Gatekeeper, Windows SmartScreen/App Control, and Linux package checks
  pass for the chosen distribution paths.
- [ ] Landing-page downloads select valid signed artifacts.

## Phase 1 completion gate

- [ ] A tagged v0.4.0 release is downloadable and verifiable on macOS, Windows,
  and Linux without cloning the repository.
- [ ] The license is explicit, CI is mandatory, installers are trusted by the OS,
  updates are signed, and the public website links to the release.
- [ ] Ten real beta users have completed a mixed-platform transfer.

---

# Phase 2 — Exceptional Desktop and Mobile Product

## Objective

Make UniDrop feel faster and more native than generic file-sharing tools while
closing the current protocol and usability limitations.

## 2.1 Trust and device management

- [ ] Add a native-friendly trusted-device screen showing name, OS, fingerprint,
  first paired, last seen, and permissions.
- [ ] Support rename, revoke, forget, and re-pair flows.
- [ ] Detect and clearly block unexpected certificate or device-identity changes.
- [ ] Store device private keys and long-lived secrets in macOS Keychain, Windows
  DPAPI/Credential Manager, and Linux Secret Service when available, with a secure
  file-permission fallback.
- [ ] Add a complete local reset workflow with explicit warnings and recovery
  instructions.

## 2.2 Stronger pairing

- [ ] Select a reviewed PAKE or equivalent standard protocol for short-code
  pairing; do not invent a cryptographic construction.
- [ ] Bind pairing to both device identities and the full transcript.
- [ ] Add QR pairing that encodes only short-lived public/bootstrap information.
- [ ] Add expiration, replay protection, rate limiting, cancellation, and clear
  confirmation on both devices.
- [ ] Preserve a manual fingerprint-verification option for high-assurance users.

## 2.3 Reliable large transfers

- [ ] Add an explicit final SHA-256 or BLAKE3 content digest and receiver
  verification before marking a transfer complete.
- [ ] Design chunked, resumable transfers with stable transfer IDs and per-chunk
  integrity.
- [ ] Persist minimal resume metadata and clean it after success, expiry, decline,
  or cancellation.
- [ ] Implement pause, resume, retry, cancellation, speed, ETA, and failure reason.
- [ ] Enforce disk-space, concurrent-transfer, bandwidth, size, and temporary-file
  limits.
- [ ] Test sleep/wake, Wi-Fi changes, process restart, dropped packets, full disk,
  sender disappearance, and duplicate chunks.

## 2.4 Folder and multi-item transfer

- [ ] Choose a streaming archive/container format with an explicit threat model.
- [ ] Reject absolute paths, parent traversal, symlink escapes, device nodes,
  unsafe permissions, excessive expansion, and duplicate/colliding entries.
- [ ] Preserve directory structure and safe timestamps without granting executable
  permissions unexpectedly.
- [ ] Show aggregate and per-file progress.
- [ ] Allow the receiver to inspect top-level contents and total size before
  acceptance.

## 2.5 Native operating-system integration

- [ ] Finder: add a supported Quick Action or Share extension for “Send with
  UniDrop.”
- [ ] Explorer: add a supported Windows context-menu integration with clean
  install/uninstall behavior.
- [ ] Linux: integrate with Nautilus, Dolphin, and Thunar using supported extension
  or desktop-action mechanisms.
- [ ] Route every integration through the authenticated local command bridge.
- [ ] Show a compact peer picker instead of immediately sending to the last peer.
- [ ] Add native incoming Accept/Decline notifications where platform APIs permit.
- [ ] Add drag-and-drop onto the tray/menu icon where practical.

## 2.6 UI quality, accessibility, and localization

- [ ] Create a documented design system for color, typography, spacing, status,
  icons, motion, empty states, and errors.
- [ ] Meet WCAG 2.2 AA for the web-based panel and equivalent native accessibility
  expectations.
- [ ] Complete keyboard navigation, focus visibility, reduced motion, screen-reader
  names, scalable text, and high-contrast testing.
- [ ] Make device identity and security state understandable without jargon.
- [ ] Add exportable diagnostic reports that automatically redact secrets and
  personal file information.
- [ ] Extract strings and ship the first community translations only after the
  English UI stabilizes.

## 2.7 Mobile MVP

- [ ] Write an architecture decision record for native versus cross-platform iOS
  and Android clients.
- [ ] Prototype discovery, pairing, send, receive, background limitations, and OS
  share-sheet integration on both platforms.
- [ ] Support sharing into UniDrop from Photos, Files, and other applications.
- [ ] Document background-transfer limitations honestly.
- [ ] Complete store privacy disclosures and encryption declarations.

## Phase 2 tests

- [ ] Multi-gigabyte files and deep folder trees transfer without unbounded memory.
- [ ] Every received file is content-verified before final rename.
- [ ] Resume succeeds after network interruption and process restart.
- [ ] Archive traversal, symlink, collision, decompression-bomb, quota, and malformed
  metadata tests fail safely.
- [ ] Accessibility checks and hands-on keyboard/screen-reader tests pass.
- [ ] Context-menu integrations survive upgrades and disappear on uninstall.

## Phase 2 completion gate

- [ ] A normal user can right-click or share a file/folder, choose a nearby trusted
  device, approve it natively, observe accurate progress, recover from an
  interruption, and verify successful completion.
- [ ] Device revocation, secure secret storage, and reviewed short-code/QR pairing
  are complete.

---

# Phase 3 — UniDrop Cloud Private Alpha

## Objective

Build the smallest useful SaaS control plane for remote discovery and end-to-end
encrypted transfer without turning UniDrop into cloud storage.

## 3.1 Architecture and privacy boundaries

- [ ] Write architecture decision records for the control plane, data plane,
  identity model, metadata model, relay, encryption boundaries, and deletion.
- [ ] Document exactly what the server can and cannot observe.
- [ ] Keep file-decryption keys and device private keys exclusively on end-user
  devices.
- [ ] Prefer direct LAN, then direct internet peer-to-peer, then opaque relay.
- [ ] Define metadata minimization and short retention before collecting any
  production data.
- [ ] Define protocol negotiation so old local-only clients continue working.
- [ ] Decide control-plane stack after a threat, operational, and dependency
  review. Prefer a compact PHP service where it remains secure and maintainable;
  use a small specialized relay only where the transport requires it.

## 3.2 Accounts and authentication

- [ ] Implement email verification and passkeys as the preferred authentication
  method.
- [ ] Add recovery codes and a secure account-recovery policy that cannot silently
  seize device encryption identity.
- [ ] Add session listing, revocation, CSRF protection, secure cookies, rate
  limiting, and suspicious-login controls.
- [ ] Require step-up authentication for billing, organization ownership, device
  revocation, and security changes.
- [ ] Separate human account identity from device cryptographic identity.

## 3.3 Device enrollment and remote directory

- [ ] Enroll devices using signed challenges and short-lived bootstrap tokens.
- [ ] Store only public device identity, routing metadata, owner/organization,
  capabilities, and minimal last-seen state.
- [ ] Provide personal and organization device directories.
- [ ] Support remote rename, disable, revoke, and lost-device actions.
- [ ] Notify users of new enrollment and sensitive device changes.
- [ ] Prevent enumeration of accounts, organizations, and device identities.

## 3.4 Remote connection and relay

- [ ] Spike standards-based NAT traversal and transport options before committing
  to WebRTC, QUIC, or another approach.
- [ ] Authenticate rendezvous messages end to end between enrolled devices.
- [ ] Attempt direct connections first with strict timeouts and safe fallback.
- [ ] Build an opaque relay that forwards encrypted frames without plaintext keys.
- [ ] Bind transfers to sender, receiver, offer, size, expiry, and content digest.
- [ ] Add relay quotas, concurrency limits, bandwidth limits, timeouts, abuse
  detection, and immediate revocation.
- [ ] Prove through tests and packet inspection that the relay cannot decrypt a
  transferred file.

## 3.5 Optional offline delivery

- [ ] Treat offline delivery as a separate opt-in feature after live relay is
  reliable.
- [ ] Encrypt files on the sender before upload with recipient-bound keys.
- [ ] Use short expiration, one-time retrieval, deletion receipts, quotas, and
  cryptographic content verification.
- [ ] Make metadata and retention visible before the user sends.
- [ ] Test deletion, expiration, interrupted upload, partial retrieval, revoked
  recipient, and compromised storage credentials.

## 3.6 Organizations and administration

- [ ] Add organizations, invitations, owner/admin/member roles, and least-privilege
  authorization.
- [ ] Add organization device inventory and lost-device revocation.
- [ ] Add append-only security/audit events without filenames or file contents.
- [ ] Add policy controls for remote transfer, external recipients, size limits,
  receiving mode, and required client version.
- [ ] Design SSO/SCIM boundaries now but defer implementation until customer demand
  validates Enterprise scope.

## 3.7 Billing and entitlement

- [ ] Select a payment provider after fee, tax, webhook, security, and operational
  review.
- [ ] Keep card data out of UniDrop systems using hosted checkout/customer portal.
- [ ] Implement server-side plan entitlements rather than trusting client flags.
- [ ] Verify signed webhooks, handle replay/idempotency, and reconcile subscription
  state.
- [ ] Support trials, upgrades, downgrades, cancellation, grace periods, refunds,
  invoices, and account deletion.
- [ ] Ensure billing failure never disables free LAN transfers.

## Phase 3 tests

- [ ] Authorization tests prove users cannot access another account, organization,
  device, event, subscription, or relay allocation.
- [ ] Direct remote transfers and relay fallback work across representative NAT and
  firewall environments.
- [ ] Relay compromise simulations expose ciphertext and minimal metadata only.
- [ ] Billing webhook replay, reordering, duplication, forgery, and provider outage
  tests pass.
- [ ] Account and device deletion honor documented retention behavior.
- [ ] Local-only use works fully while the cloud is unreachable.

## Phase 3 completion gate

- [ ] A private alpha user can create an account, enroll two devices on different
  networks, find them automatically, transfer directly or through an opaque E2EE
  relay, manage/revoke devices, and purchase/cancel a test subscription.
- [ ] No server component can decrypt file contents, and cloud failure does not
  break free LAN transfers.

---

# Phase 4 — Security, Reliability, and Production Readiness

## Objective

Prove that UniDrop can responsibly operate a public encrypted-transfer service and
respond when systems, dependencies, keys, or assumptions fail.

## 4.1 Formal threat model and security requirements

- [ ] Expand the threat model to cover hostile LAN peers, malicious paired devices,
  compromised accounts, stolen devices, malicious relays, insiders, dependency
  compromise, CI compromise, denial of service, and metadata leakage.
- [ ] Map every trust boundary, secret, privileged operation, data flow, attacker
  goal, mitigation, test, owner, and accepted residual risk.
- [ ] Align the secure development process with NIST SSDF practices.
- [ ] Establish release security criteria that cannot be waived silently.
- [ ] Review every cryptographic protocol and format against published standards.

## 4.2 Independent review and remediation

- [ ] Freeze a reviewable protocol version and create complete protocol
  documentation and test vectors.
- [ ] Commission an independent cryptography/protocol review.
- [ ] Commission an application, API, desktop, installer, updater, and cloud
  penetration test.
- [ ] Triage findings by exploitability and impact, fix validated findings, add
  regression tests, and retest.
- [ ] Publish a transparent audit summary and unresolved limitations after the
  reviewer approves disclosure.
- [ ] Do not market UniDrop as independently audited until this gate is complete.

## 4.3 Secure development and supply chain

- [ ] Protect default branches, require reviews and passing checks, and restrict
  release permissions.
- [ ] Use short-lived CI identity where possible and hardware-backed protection for
  long-lived signing keys.
- [ ] Generate and retain SBOMs, provenance, checksums, source commit, compiler
  version, and build logs for releases.
- [ ] Add secret scanning, static analysis, dependency review, fuzzing, and periodic
  dynamic testing.
- [ ] Establish patch SLAs and a documented emergency-release process.
- [ ] Conduct a signing-key compromise and malicious-dependency tabletop exercise.

## 4.4 Production operations

- [ ] Define SLOs for control-plane availability, relay success, API latency,
  transfer setup time, and support response.
- [ ] Add health checks, structured logs, metrics, tracing, and alerts with privacy
  filtering at collection time.
- [ ] Create public status and incident-history pages.
- [ ] Automate encrypted backups and test restoration regularly.
- [ ] Document capacity limits and test relay saturation, regional failure, queue
  buildup, and dependency outages.
- [ ] Use infrastructure as code, separate environments, least privilege, and
  reviewed production changes.
- [ ] Create disaster-recovery objectives and conduct a recovery rehearsal.

## 4.5 Abuse and incident response

- [ ] Add protections for account creation abuse, relay theft, bandwidth fraud,
  enumeration, brute force, spam invitations, and denial of service.
- [ ] Define alert severity, on-call ownership, containment, customer notification,
  forensic preservation, and postmortem processes.
- [ ] Publish a vulnerability disclosure policy and `security.txt`.
- [ ] Create a private reporting channel with acknowledgement and remediation
  targets.
- [ ] Plan a scoped bug-bounty or researcher-recognition program after the external
  audit.
- [ ] Rehearse account takeover, relay credential compromise, signing-key theft,
  and customer-data exposure scenarios.

## 4.6 Privacy engineering

- [ ] Inventory every collected data element, purpose, legal/business need, owner,
  location, access path, retention period, and deletion behavior.
- [ ] Remove fields that are not required for the product to work.
- [ ] Add data export and account deletion with auditable completion.
- [ ] Review subprocessors and prevent production customer data from entering
  development or support tools unnecessarily.
- [ ] Verify logs, crash reports, support bundles, analytics, and backups honor the
  same privacy boundaries.

## Phase 4 tests

- [ ] Fuzzers run continuously against protocol parsing, pairing, offers, archives,
  manifests, and relay framing without known crashes or hangs.
- [ ] Load and chaos tests meet the documented SLOs and recovery objectives.
- [ ] Backup restoration, region loss, credential revocation, and emergency release
  rehearsals succeed.
- [ ] Independent review findings required for launch are closed and retested.
- [ ] Privacy deletion and export tests cover live stores, logs, queues, and backup
  expiration.

## Phase 4 completion gate

- [ ] External security review is complete, critical/high launch blockers are
  remediated and retested, production monitoring and response are operational,
  disaster recovery has been rehearsed, and the privacy data map matches runtime
  behavior.

---

# Phase 5 — Company and Customer Foundation

## Objective

Create the legal, financial, support, documentation, and trust foundation required
to accept paying customers. Licensed professionals must review legal, tax, export,
and insurance decisions; completing a software checklist is not a substitute.

## 5.1 Company and ownership

- [ ] Decide LLC versus corporation with an attorney and CPA based on ownership,
  tax, fundraising, and hiring plans.
- [ ] Form the entity, obtain required tax identifiers, and establish registered
  agent and annual filing calendars.
- [ ] Open dedicated business banking and bookkeeping.
- [ ] Assign relevant code, designs, domains, marks, and other IP to the company.
- [ ] Use contributor agreements or a Developer Certificate of Origin if counsel
  recommends them for the chosen license and business model.
- [ ] Establish contractor/employee IP, confidentiality, and access-offboarding
  procedures before anyone else receives sensitive access.

## 5.2 Brand and public identity

- [ ] Complete trademark clearance before a major launch or paid advertising.
- [ ] Secure the primary domain and defensive social/developer handles.
- [ ] Define correct logo/name usage for community builds and forks.
- [ ] Create a press kit with approved logos, screenshots, founder bio, product
  description, and security claims.
- [ ] Ensure every public security/privacy statement is supported by implemented
  and tested behavior.

## 5.3 Legal and compliance documents

- [ ] Have counsel review Terms of Service, Privacy Policy, Acceptable Use Policy,
  refund/cancellation terms, and open-source notices.
- [ ] Prepare a Data Processing Addendum and subprocessor list for business
  customers.
- [ ] Determine applicable state, federal, international, encryption export, sales
  tax/VAT, records, and breach-notification obligations.
- [ ] Complete app-store privacy and encryption declarations accurately.
- [ ] Establish retention and deletion schedules consistent with product behavior.
- [ ] Review accessibility commitments and procurement requirements for target
  business customers.

## 5.4 Vendor and financial controls

- [ ] Inventory hosting, DNS, email, payment, analytics, support, monitoring,
  identity, and backup vendors.
- [ ] Review each vendor's security, privacy, availability, data location,
  subprocessors, breach terms, and exit/export path.
- [ ] Require MFA, least privilege, separate accounts, recovery owners, and regular
  access reviews.
- [ ] Set operating budgets, relay cost alerts, fraud limits, revenue recognition,
  refund handling, and tax workflows.
- [ ] Evaluate appropriate general liability, technology E&O, and cyber insurance.

## 5.5 Documentation and customer support

- [ ] Publish installation, onboarding, transfer, security, privacy, CLI, admin,
  troubleshooting, update, uninstall, and account-deletion documentation.
- [ ] Create a searchable support center and support contact with response targets.
- [ ] Build secret-redacted diagnostic collection and consented support-upload
  flows.
- [ ] Create support playbooks for discovery, firewall, pairing, transfer, billing,
  account recovery, suspected compromise, and outage cases.
- [ ] Add in-product links to relevant help without requiring an account for local
  use.
- [ ] Train any support collaborator on privacy, identity verification, escalation,
  and never requesting private keys or file contents.

## Phase 5 tests

- [ ] A new customer can find and understand pricing, terms, privacy, security,
  support, cancellation, data export, and deletion before purchasing.
- [ ] Counsel/CPA-owned decisions are documented as approved or explicitly pending;
  no placeholder legal text is presented as final.
- [ ] A support drill resolves common cross-platform issues using published docs
  and a redacted diagnostic bundle.
- [ ] Vendor access and account-recovery exercises confirm the company cannot be
  locked out by one lost device or individual.

## Phase 5 completion gate

- [ ] The operating entity owns the product IP and accounts, can accept and account
  for payments, has reviewed customer terms and privacy disclosures, maintains a
  vendor/subprocessor inventory, and can support, refund, export, and delete a
  customer account predictably.

---

# Phase 6 — Go-to-Market, Validation, and Public Launch

## Objective

Find a narrow group that repeatedly needs UniDrop, prove they will pay, launch
responsibly, and create a measurable learning loop.

## 6.1 Ideal customer research

- [ ] Interview at least five people in each promising segment: creative teams,
  IT consultants, mixed-platform offices, schools/labs, privacy-conscious small
  businesses, and field teams with unreliable internet.
- [ ] Record current tools, transfer frequency, file sizes, failures, security
  concerns, procurement constraints, and willingness to pay.
- [ ] Identify one primary initial customer profile and one secondary profile.
- [ ] Write the top three jobs-to-be-done and explicit reasons UniDrop wins.
- [ ] Maintain a competitor matrix covering local-only, remote transfer, mobile,
  CLI, native integration, team controls, security claims, pricing, and support.

## 6.2 Positioning and onboarding

- [ ] Test a clear promise centered on local-first privacy, cross-platform native
  speed, and secure remote fallback.
- [ ] Produce a short authentic demo showing Mac↔Windows↔Linux and remote transfer.
- [ ] Make the first-run path guide installation, firewall permission, device
  naming, pairing, first transfer, and optional account creation.
- [ ] Keep account creation out of the free LAN-transfer critical path.
- [ ] Add sample files or a safe guided transfer to reach value quickly.
- [ ] Make failure states explain the likely cause and next exact action.

## 6.3 Measurement without surveillance

- [ ] Define the activation event: two devices installed, paired, and one completed
  transfer.
- [ ] Measure only consented, minimal events needed for activation, reliability,
  retention, and conversion decisions.
- [ ] Prefer aggregate counts and coarse technical dimensions over durable user or
  device tracking.
- [ ] Publish the analytics event schema and retention period.
- [ ] Track activation rate, transfer success, time to first transfer, weekly
  returning users, remote usage, support rate, relay cost, trial conversion,
  churn, and deletion completion.
- [ ] Provide an always-visible opt-out that does not degrade core functionality.

## 6.4 Pricing and design partners

- [ ] Recruit at least ten design partners from the selected primary segment.
- [ ] Test product packaging and willingness to pay before final pricing.
- [ ] Validate that Free is useful indefinitely and Pro/Teams charge for remote
  convenience, administration, reliability, and support.
- [ ] Model gross margin using real relay bandwidth, storage, payment, support, and
  infrastructure costs.
- [ ] Establish clear limits without surprise overages or hostage-style access to
  customer files.
- [ ] Convert at least three design partners into paying customers or document why
  the offer failed and revise it before broad launch.

## 6.5 Community and launch system

- [ ] Enable GitHub Discussions and create contribution, translation, feature
  request, and support boundaries.
- [ ] Publish a transparent roadmap, changelog, release cadence, security posture,
  and known limitations.
- [ ] Create launch assets for the website, GitHub, relevant communities, product
  directories, press, and social channels without making unsupported claims.
- [ ] Prepare FAQ and support capacity before launch traffic.
- [ ] Schedule launch monitoring with rollback authority and incident contacts.
- [ ] Thank contributors visibly and establish a sustainable maintainer process.

## 6.6 Controlled public launch

- [ ] Run an invite beta, then a public beta, before declaring general availability.
- [ ] Verify capacity, alerts, billing, refunds, support, status communication, and
  incident response under expected launch load.
- [ ] Freeze nonessential changes before launch and publish signed final artifacts.
- [ ] Monitor activation, failures, support volume, abuse, infrastructure cost, and
  conversion throughout launch.
- [ ] Hold a factual launch review and prioritize improvements from observed user
  behavior rather than vanity metrics.
- [ ] Publish a post-launch roadmap and service health summary.

## Phase 6 tests

- [ ] At least ten design partners complete onboarding without founder intervention.
- [ ] Cross-platform and remote transfer success meet the Phase 4 SLOs.
- [ ] A full purchase, invoice, upgrade, downgrade, cancellation, refund, data
  export, and deletion journey succeeds in production-like conditions.
- [ ] Launch-day load, provider outage, rollback, customer communication, and abuse
  drills pass.
- [ ] Customer interviews and behavior demonstrate repeat use and a credible paid
  need in the chosen initial segment.

## Phase 6 completion gate

- [ ] UniDrop is publicly downloadable, signed, independently reviewed,
  supportable, legally and operationally prepared, and used repeatedly by a
  defined customer segment.
- [ ] At least three real customers pay for validated Pro or Teams value.
- [ ] The company can measure reliability, activation, retention, cost, and
  conversion without collecting file contents or unnecessary personal data.

---

# Final Startup Readiness Gate

UniDrop is a legitimate SaaS startup—not merely a working application—when all of
the following are true:

- [ ] Free local transfers remain reliable without an account or cloud dependency.
- [ ] Native signed installers and secure updates work on all supported platforms.
- [ ] Release artifacts have checksums, SBOMs, provenance, and reproducible source
  linkage.
- [ ] Remote transfers are end-to-end encrypted and the relay cannot decrypt them.
- [ ] Independent reviewers have assessed the security-sensitive implementation.
- [ ] The service has measurable SLOs, monitoring, backups, incident response, and
  tested recovery.
- [ ] Legal, privacy, billing, support, and deletion workflows match actual product
  behavior.
- [ ] A specific customer segment repeatedly uses UniDrop and pays for the SaaS
  value.
- [ ] Marketing claims are precise, current, and supported by evidence.

# External Prerequisites Requiring Owner Action

These items may block completion and cannot be fabricated or bypassed by code:

- [ ] Approve the open-source and trademark policy.
- [ ] Create or provide the Apple Developer account and signing access.
- [ ] Create or provide the Microsoft Partner Center/signing account.
- [ ] Approve making the production landing page publicly accessible.
- [ ] Approve domain purchases and company/brand registrations.
- [ ] Engage qualified legal, tax, export-compliance, insurance, and security
  professionals where identified above.
- [ ] Approve hosting, payment, email, monitoring, and support vendors before they
  receive production data or incur charges.
- [ ] Participate in customer interviews, pricing decisions, and final launch
  authorization.

# Reference Standards and Platform Guidance

- NIST Secure Software Development Framework:
  <https://csrc.nist.gov/pubs/sp/800/218/final>
- GitHub artifact attestations:
  <https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/use-artifact-attestations>
- Apple notarization:
  <https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution>
- Apple encryption export compliance:
  <https://developer.apple.com/help/app-store-connect/manage-app-information/overview-of-export-compliance>
- Microsoft Windows distribution and signing:
  <https://learn.microsoft.com/en-us/windows/apps/package-and-deploy/code-signing-options>
- Flathub submission guidance:
  <https://docs.flathub.org/docs/for-app-authors/submission>
- FTC security guidance:
  <https://www.ftc.gov/business-guidance/resources/start-security-guide-business>
- California Privacy Protection Agency FAQ:
  <https://cppa.ca.gov/faq>

# Fresh-Chat Execution Instructions

When beginning implementation in a new chat:

1. Read `AGENTS.md`, this entire file, `README.md`, `SECURITY.md`, and
   `docs/ARCHITECTURE.md` before editing.
2. Inspect the current worktree and preserve unrelated or pre-existing changes.
3. Start at the first unchecked task in the earliest incomplete phase.
4. Work forward; do not claim a phase is complete when its tests or completion
   gate are incomplete.
5. Record important architectural/security decisions in the repository.
6. Update checkboxes only after implementation and verification.
7. Stop and request owner action only for an item listed under External
   Prerequisites or another genuinely irreversible/business decision.
8. Never download or adopt a package before checking current CVEs/advisories,
   provenance, license, maintenance status, and whether a standard-library option
   is sufficient.
9. Commit and push only when explicitly requested, using focused commits that do
   not absorb unrelated user changes.
