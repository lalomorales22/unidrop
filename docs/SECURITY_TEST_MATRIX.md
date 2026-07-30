# Security negative-test matrix

This matrix maps UniDrop's implemented security-sensitive behavior to executable
negative or abuse-case evidence. It covers the current alpha client, packaging,
release metadata, and staged updater. It does not claim an independent
cryptographic review, trusted OS signing, updater activation, or completion of
future phases.

## Current client controls

| Surface | Required failure behavior | Executable evidence |
| --- | --- | --- |
| TLS peer transport | Reject TLS below 1.3 and a certificate that differs from the paired pin; never inherit a shell proxy for LAN traffic | `TestTLS13PinningAndProxyIsolation`, `TestChangedFingerprintBlocksTrustedPeer` |
| Pairing transcript | Bind receiver certificate, sender ID and certificate, nonce, and return token | `TestPairProofBindsEveryIdentityValue` |
| Pairing authentication | Reject a changed transcript, a replayed one-time proof, and more than eight attempts from one address | `TestPairingRejectsTamperingReplayAndBruteForce` |
| Pairing secrets | Rotate the one-time code and retain only the incoming token hash while creating independent 256-bit directional tokens | `TestPairAndTransferEndToEnd`, `TestPairingRejectsTamperingReplayAndBruteForce` |
| Browser-local mutations | Reject missing UI intent header, non-loopback Host, wrong method, unknown JSON fields, trailing objects, and oversized JSON without changing state | `TestLocalMutationGuardsRejectBrowserAndRemoteAbuse` |
| Native command bridge | Reject a remote Host, incorrect bearer token, and wrong method without executing the command | `TestCLIControlRequiresPrivateToken`, `TestCommandBridgeRejectsWrongHostTokenAndMethod`, `TestAuthorizedTrayShutdown` |
| Platform tray bridges | Reject non-loopback control URLs, missing/wrong authentication, and invalid state mutations | `cmd/unidrop-tray: TestCoreClientRejectsNonLoopbackControlURL`, `cmd/unidrop-tray-windows: TestCoreClientRequiresLoopback`, both authenticated bridge tests |
| Secret files | Create the config directory, state, device certificate/key, and control token as non-symlink paths without group/other permissions on POSIX | `TestSecretFilesArePrivateRegularFiles` |
| Sender authentication | Reject an unknown sender or incorrect directional bearer token | `TestReceiveRejectsUnpairedSender`, `TestOfferAuthorizationIsolationAndFloodLimits` |
| Transfer offers | Bind the accepted sender, filename, and byte count; expire and consume an offer once; prevent another peer from observing or consuming it | `TestAcceptedOfferBindsMetadataAndCanBeConsumedOnce`, `TestOfferAuthorizationIsolationAndFloodLimits` |
| Offer abuse limits | Reject an eleventh pending offer from one sender and decline pending offers when receiving is disabled | `TestOfferAuthorizationIsolationAndFloodLimits`, `TestReceivingOffDeclinesPendingOffers` |
| Receive writes | Reject partial, oversized, and unsafe-name receives; remove partial files and mark the offer failed | `TestFailedAndOversizedReceivesLeaveNoFiles`, `TestSanitizeFilename` |
| Existing files | Select a new safe destination rather than choosing an existing filename | `TestUniqueDestinationPreservesExistingFiles` |
| Protocol compatibility | Keep the oldest supported protocol/version contract explicit and reject incompatible update metadata | `TestOldestSupportedProtocolContract`, updater manifest abuse table |

## Release and staged-update controls

| Surface | Required failure behavior | Executable evidence |
| --- | --- | --- |
| Release signing keys | Reject manifest tampering and private keys with unsafe POSIX permissions | `cmd/unidrop-release: TestManifestSigningRoundTripAndTamperRejection`, `TestPrivateKeyPermissionsAreEnforced` |
| Signed update metadata | Reject unsigned/tampered metadata, unknown keys, wrong platform or architecture, protocol downgrade, malformed compatibility floors/revocations, excessive validity, and rollback | `cmd/unidrop-update: TestUpdateManifestAbuseCasesFailClosed`, `TestSignedDuplicateAndUnknownJSONFieldsAreRejected`, `TestUnknownSigningKeyIsRejected` |
| Update network boundary | Reject unapproved initial hosts and redirects to unapproved hosts | `TestInitialMetadataHostMustBeApproved`, `TestRedirectToUnapprovedHostIsRejected` |
| Update resource limits | Reject oversized metadata and artifact declarations without changing local state | `TestOversizedMetadataIsRejectedWithoutChangingState`, updater abuse table |
| Update downloads | Remove corrupt or interrupted downloads and do not advance rollback state | `TestCorruptAndPartialDownloadsAreRemoved` |
| Update state | Reject symlinked/insecure state and staging paths; advance highest-accepted version monotonically | `TestStagingDirectoryRejectsSymlink`, `TestUpdateStateRejectsSymlinkAndPublicPermissions`, `TestRollbackStateAdvancesAcrossMultipleWrites` |
| Offline behavior | Leave startup/local state usable when the update endpoint is offline | `TestOfflineCheckDoesNotAffectStartupState` |

## Verification boundary

The required CI workflow runs the Go suite on macOS, Windows, and Linux, including
race tests where supported. It separately verifies installers, release metadata,
website behavior, universal macOS packaging, and both native AppImage
architectures. A green run proves the cases above executed on that revision; it
does not replace the open tasks for an independent protocol review, real
mixed-machine interoperability, Developer ID/notarization, Windows publisher
trust, clean-machine release validation, or full update activation and recovery.

Run the local evidence with the reviewed toolchain:

```sh
go test -mod=vendor ./...
go test -race -mod=vendor ./...
go vet -mod=vendor ./...
git diff --check
```
