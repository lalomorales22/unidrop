# Encryption export-compliance release gate

This document records UniDrop's current cryptographic inventory and the decisions
that must be completed before a production macOS build is distributed. It is an
engineering compliance checklist, not legal advice or a final commodity
classification. The exporter remains responsible for the classification,
filings, destinations, end users, and end uses.

## Current technical determination

UniDrop **uses and implements encryption**. The macOS application embeds the Go
standard library rather than limiting cryptography to Apple's operating-system
APIs. It therefore must not be described as an application that uses no
encryption or only encryption supplied by the Apple operating system.

The current product contains:

- TLS 1.3 in Go's `crypto/tls` package for peer connections;
- locally generated ECDSA P-256 X.509 device certificates and SHA-256
  certificate pinning;
- HMAC-SHA-256 transcript binding and random secrets for pairing and control
  authentication;
- Ed25519 signatures and SHA-256 hashes for release metadata and artifacts; and
- HTTPS for update metadata and artifact downloads.

The algorithms are published industry-standard primitives. UniDrop also combines
them in its own pairing protocol. That protocol is public in the repository but
has not been standardized or independently reviewed. An exporter or qualified
export professional must decide whether any part is "non-standard cryptography"
under the applicable rules; this repository does not claim that determination.

UniDrop does not currently encrypt stored file contents or provide a general
cryptographic library, SDK, cryptanalytic tool, government-specific feature, or
user-modifiable cryptographic interface. Those facts must be rechecked against
the shipping revision rather than assumed from this document.

## Distribution-path requirements

### Public source code

EAR section 742.15(b) provides specific treatment for publicly available 5D002
encryption source code. Notification to BIS and the ENC Encryption Request
Coordinator is required when that source code provides or performs non-standard
cryptography. The owner-selected license and the actual terms of public
availability matter because the EAR's published-source rules address availability
without restrictions on further dissemination.

The project does not yet have an owner-approved license. Do not rely on the
public-source-code treatment until the license is explicit and a qualified person
has confirmed that the repository and pairing implementation meet the rule's
conditions. Public-source treatment also does not automatically classify compiled
macOS, Windows, or Linux binaries.

### Direct-download macOS DMG

Before making a Developer ID-signed or notarized DMG globally available, the
exporter must document:

1. whether the binary is subject to the EAR and its Category 5, Part 2/ECCN
   classification;
2. whether it qualifies as mass-market software and which paragraph of License
   Exception ENC applies;
3. whether self-classification is permitted or a BIS classification request
   (CCATS) is required;
4. any filing, reporting, destination, end-user, and end-use restrictions; and
5. the exporter of record and the person who approved the determination.

BIS states that items within section 740.17(b)(1) may be self-classified, but an
annual self-classification report is then required unless BIS has issued a CCATS
for the item. The report for an applicable export year is due by February 1 of
the following year. These rules can change, so verify them again for every public
release rather than relying only on this snapshot.

Apple code signing and notarization establish software provenance; they do not
replace the export classification or any required BIS submission.

### App Store or TestFlight distribution

If a future release uses App Store Connect, the Account Holder, Admin, or App
Manager must complete Apple's encryption questions before review. The shipping
binary is not limited to Apple operating-system encryption, so that shortcut does
not describe UniDrop. Apple's current documentation says an app using an industry
standard algorithm outside the operating system may require a French encryption
declaration for distribution in France, while proprietary or non-standard
encryption may require both a U.S. CCATS and the French declaration.

Do not add `ITSAppUsesNonExemptEncryption` or
`ITSEncryptionExportComplianceCode` to `macos/Info.plist` by guesswork. Add the
values that follow from the recorded determination and, when applicable, the code
issued by App Store Connect.

## Required owner record

The production-release evidence must include all of the following:

| Field | Required evidence |
| --- | --- |
| Exporter | Legal person/entity and country responsible for distribution |
| Shipping revision | Immutable Git tag and source commit reviewed |
| Crypto inventory | Algorithms, protocols, key sizes, purposes, and linked implementations |
| Classification | ECCN or documented reason the item is outside the relevant controls |
| Authorization | Applicable ENC paragraph, NLR basis, license, or other authorization |
| Mass-market analysis | Product availability, audience, price, support, and customizability |
| Non-standard crypto decision | Written conclusion addressing the UniDrop pairing protocol |
| Submission/report | CCATS identifier, notification evidence, or annual-report obligation |
| Destinations | Approved markets plus denied/restricted destination and end-user controls |
| Apple answer | App Store Connect determination and plist values, if that channel is used |
| Approval | Owner and qualified reviewer name, date, and scope |

Keep this evidence outside public source control if it contains personal,
account, filing, or restricted-party data. Record only non-sensitive identifiers
needed for the release audit trail.

## Release and change-control gate

A production release is blocked until the owner has approved the license and a
qualified exporter or export-control professional has completed the required
record above. The gate must be repeated when UniDrop adds or materially changes
cryptographic algorithms, pairing, remote relay, end-to-end encryption, key
management, product editions, distribution countries, or the exporter of record.

Primary references, reviewed July 30, 2026:

- [BIS encryption controls overview](https://www.bis.gov/learn-support/encryption-controls)
- [EAR part 734, including published software and encryption exports](https://www.bis.gov/regulations/ear/734)
- [EAR part 742, including section 742.15](https://www.bis.gov/regulations/ear/742)
- [BIS mass-market guidance](https://www.bis.gov/learn-support/encryption-controls/mass-market)
- [BIS License Exception ENC 740.17(b)(1) guidance](https://www.bis.gov/learn-support/encryption-controls/license-exception-enc-740.17-b-1)
- [BIS annual self-classification guidance](https://www.bis.gov/learn-support/encryption-controls/annual-self-classification)
- [BIS encryption review and CCATS guidance](https://www.bis.gov/learn-support/encryption-controls/encryption-review-ccats)
- [Apple export-compliance overview](https://developer.apple.com/help/app-store-connect/manage-app-information/overview-of-export-compliance)
- [Apple encryption-documentation reference](https://developer.apple.com/help/app-store-connect/reference/export-compliance-documentation-for-encryption/)
- [Apple encryption declaration keys](https://developer.apple.com/documentation/security/complying-with-encryption-export-regulations)
