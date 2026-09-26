# Real EUDI phone test

The acceptance target is a signed test PID issued into an installed wallet,
followed by a holder-approved OpenID4VP presentation to the message verifier. The hosted
sample-wallet chooser does not exercise that protocol and is not this acceptance
test. A reference wallet release labelled “Demo” can still perform real protocol
operations with test credentials.

## Install and obtain a test PID

- **Android:** use the APK from the [official wallet releases](https://github.com/eu-digital-identity-wallet/eudi-app-android-wallet-ui/releases/latest).
  The release checked on 26 September 2026 is `2026.09.42-Demo`, build 42,
  published 10 September. The reference app requires Android 10 or later.
- **iPhone, installable candidate:** open the [EUDI Reference Wallet Trinsic TestFlight invitation](https://testflight.apple.com/join/kCT1m465)
  on the phone and install TestFlight and the compatible beta build, if offered.
  This is Trinsic's distribution, configured to trust Trinsic's certificates.
  Its [documented setup](https://docs.trinsic.id/docs/eudi-reference-wallet)
  issues FormEU credentials, but presentation to our hosted verifier has not yet
  been confirmed. Do not describe it as a tested compatible wallet yet.
- **iPhone, official reference build:** the [official instructions](https://github.com/eu-digital-identity-wallet/eudi-app-ios-wallet-ui#how-to-use-the-application)
  require iOS 17 or later and an Xcode build, unless an organization supplies an
  approved TestFlight/App Store/MDM build. The latest GitHub release has no
  installable asset; no public invitation distributed directly by the official
  reference project has been verified.

For the Trinsic iPhone build, create a PIN, then select **Add my Digital ID →
issuer.eudiw.dev → PID Combined**. Continue to the issuer, choose **FormEU →
Submit**, enter fictional birth date, family name, given name and nationality,
then select **Authorize**, wait for issuance and tap **Done**.

For the official reference app, create the wallet PIN. In Documents, select
**+ → From list → PID**. At the
issuer, select **Country Selection → FormEU** and enter a fictional test person.
Use an adult date of birth for this test identity. Complete issuance
and confirm the PID appears in the wallet. The official issuer also supports
credential offers from [issuer.eudiw.dev](https://issuer.eudiw.dev/).

First present to the [official EUDI verifier](https://verifier.eudiw.dev/) to
isolate phone/credential setup from our integration. Record wallet version,
credential format, issuer, and whether this baseline succeeded; do not copy the
credential or raw presentation into a ticket or repository.

## Connect the message verifier

the message verifier now delegates the name presentation to the official EUDI hosted
verifier. Follow [the live setup page](https://verify.krnali.io/setup), create a
name request, then open its link on the phone or scan the wallet QR. The phone
will identify **Web Verifier (PROD)** as the requester. The test presentation is
validated by that official service and returned to our console.

A successful holder presentation remains to be recorded. Confirm its actual
result before recording a promotional walkthrough. Decline and invalid-credential
paths have been exercised against the hosted service.

For the recording, keep the console open on the computer and the wallet on the
phone. Create a name request with the test PID's exact name, open the verification
link, and display the QR. Scan it in the wallet, show the two requested name
attributes, approve, and show the console result. Rehearse once before recording.
The result is cleared automatically after the configured claims TTL, so capture
it promptly. Repeat with a different expected name to demonstrate a mismatch.

Krnali's own RP name and the over-18 preset require the separate
[registration and certificate workflow](eudi-onboarding.md). That registry uses
a test PID for login, so installation and issuance are still the first steps.

## What the result means

This version binds a presentation to a single-use verification request. It does
not prove that the wallet holder controls the messaging account that received
the link. A forwarded link remains usable by its first recipient. Expected-name
comparison is an additional check, not proof of conversation-account ownership.
Proving that relationship is a separate product requirement, still to be agreed
and tested; it must not be claimed by changing labels or adding a case reference.
