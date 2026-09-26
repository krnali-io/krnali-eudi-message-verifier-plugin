# krnali eudi message verifier plugin

A krnali labs project for requesting an EUDI test-wallet presentation from a
conversation. Create a single-use link, let the holder approve sharing in their
wallet, and return the verified result to the operator console.

The implementation is a standalone Go HTTP service with a browser console and
messaging adapters. It has no external Go modules. HTML, CSS, JavaScript and the
QR library are embedded in the executable.

[Hosted console](https://verify.krnali.io/console) ·
[Phone setup](https://verify.krnali.io/setup) ·
[Deployment guide](docs/deployment.md)

## Current status

The hosted application delegates real OpenID4VP requests and presentation
validation to the official EUDI sandbox verifier. The wallet identifies the
requester as **Web Verifier (PROD)**. The UI discloses this relationship.

The hosted registration covers the name check; age requests are disabled in
this mode. A dedicated relying-party registration is required for a custom
requester identity and the age preset.

Automated checks have exercised signed request creation, QR display,
invalid-credential rejection and the real decline/callback flow. A successful
presentation from an installed phone wallet remains pending. The iPhone
TestFlight option is documented as a candidate, not confirmed compatible.
See [phone acceptance](docs/phone-test-setup.md).

## Features

- Name verification, expected-name comparison, and SD-JWT/ISO mdoc alternatives.
- Single-use links, expiry, cancellation, resend and automatic result clearing.
- Copy-link, Telegram, WhatsApp, SMS and SMTP adapters; unconfigured channels
  remain disabled.
- Agent/supervisor roles, filtered activity and CSV export without claim values.
- Cloudflare Access JWT validation for public deployments; Basic auth only on
  localhost.
- An explicitly synthetic demo executable for UI development, separate from
  live credential verification.

## Build and check

Use Go 1.26.4 and Python 3.13 for the same versions as CI:

```sh
make test
make lint
make build
python3 -m unittest discover -s scripts -p 'test_*.py' -v
docker build -f deploy/Dockerfile -t message-verifier:local .
```

The container runs as a non-root user. The vendored qrcodejs license is included
in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) and in the container image.

## Run the synthetic UI locally

This mode does not verify credentials or send provider messages:

```sh
PUBLIC_BASE_URL=http://localhost:8080 \
BUSINESS_NAME='krnali labs' \
OPERATORS='agent@example.test:supervisor' \
OPERATOR_USER=agent@example.test \
OPERATOR_PASSWORD='replace-with-a-local-password' \
go run ./cmd/verifylink-demo
```

Open `http://localhost:8080/console` and sign in with the configured credentials.
For actual wallet presentations, use the live executable and the
[deployment guide](docs/deployment.md). The application reads environment
variables; it does not load dotenv files automatically.

## Verification boundaries

A wallet result verifies the presented credential under the configured test
trust. It does **not** prove control of the messaging account that received the
link. Forwarding the link lets its first recipient use it; an expected-name
comparison can detect a different name.

This is a test-credential integration, not production identity assurance.
Sessions and results live in memory: a restart loses them, and one replica is
required. Claims are cleared after their configured TTL; session metadata is
retained for up to 24 hours. Memory clearing does not provide cryptographic
zeroization. Name comparison folds case and whitespace, without Unicode NFC
normalization.

The hosted verifier is an external service that receives test presentations.
Use fictional identities and review your own hosting/logging configuration.

## Integration checks

Normal unit tests use local fixtures. `scripts/check-reference.py` separately
exercises the pinned reference-verifier container with disposable, untrusted
test certificates. It does not establish real wallet trust.

`scripts/check-eudi-hosted.cjs` is an opt-in browser/protocol check against the
public EUDI service. It requires a live local test instance, Playwright and a
disposable localhost HTTPS proxy. It rejects an invalid token and submits a
decline; it never fabricates a successful identity result.

See [EUDI onboarding](docs/eudi-onboarding.md) for a dedicated relying party.
