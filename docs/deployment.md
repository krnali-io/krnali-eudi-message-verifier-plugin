# Deployment

Build the live executable with `make build` or the image with
`docker build -f deploy/Dockerfile -t message-verifier:local .`.
`deploy/.env.example` contains placeholders; replace the hostname, operator
identity and authentication settings for your deployment.

## Hosted EUDI sandbox

Use the live executable `cmd/verifylink` with:

```text
VERIFIER_MODE=eudi-sandbox
VERIFIER_INTERNAL_URL=https://verifier-backend.eudiw.dev
PUBLIC_BASE_URL=https://your-verifier.example
ISSUER_CHAIN_FILE=/secrets/issuer-chain.pem
REGISTRATION_CERTIFICATE_FILE=/registration/registration.jwt
```

The callback must use a real, reachable HTTPS origin. The hosted service rejects
HTTP callback templates. This mode deliberately accepts only the exact official
verifier endpoint and cannot be enabled in the synthetic executable.

The official service publishes intended uses at `GET /ui/intended-uses`. Select
the name-authorized test registration and provide its `registration_certificate`
as a file. Inspect its requested scope and current validity. The service holds
its own signing key; do not copy or generate a substitute key for its identity.

Confirm the PID issuer chain against the credential on the test phone. For the
FormEU setup checked on 26 September 2026, the EU PID CA fingerprint was
`3B:0A:22:3E:87:48:F3:C4:22:17:29:8A:D9:05:EC:F1:A1:04:E7:3C:D2:C5:4F:F2:1D:A1:6F:DA:80:70:B2:97`.
Source: [reference-wallet CA](https://github.com/eu-digital-identity-wallet/eudi-app-android-wallet-ui/blob/534e2ab01a7927e75ba8c96184002525ee5189ae/resources-logic/src/main/res/raw/pidissuerca02_eu.pem).
Verify the current trust material before using it. Certificate comparison alone
is not a complete trusted-list validation implementation.

Wallets contact the hosted verifier directly. `/wallet/*`, `/ui/*` and
`/utilities/*` are not public proxies in this mode. The hosted registration
currently enables name verification only. The UI must retain the disclosure
that the requester is Web Verifier (PROD).

## Operator authentication

Set `OPERATORS` to a comma-separated list of `email:agent` or
`email:supervisor` entries. Configure a Cloudflare Access application covering
`/console*` and `/api/*`, then set its `CF_ACCESS_ISSUER` and
`CF_ACCESS_AUDIENCE`. The app validates the JWT signature and allowed operator.
Leave handoff, status, callback and authenticated provider webhook paths outside
Access. Restrict origin access to the trusted ingress.

Remove `OPERATOR_USER` and `OPERATOR_PASSWORD` for a public deployment; Basic
authentication is only supported when the public URL is localhost.
Enable `TRUST_PROXY` only when network controls prevent callers from spoofing
forwarded client-address headers. Keep it false otherwise.

## Runtime

Provide configuration with container environment variables or `--env-file`.
Mount the issuer PEM and registration JWT at their configured read-only paths.
For a private reverse-proxy origin, bind the app's port to loopback, for example
`-p 127.0.0.1:8080:8080`. Use a read-only filesystem, drop Linux capabilities,
disable privilege escalation, and keep one replica because state is in memory.

No messaging credentials are required for copy-link. Add provider credentials
only for the channels you intend to use. They belong in a protected secret
store, not source control. The application does not log claims or raw provider
errors; review reverse-proxy and platform logs separately.

## Dedicated relying party

For your own wallet requester identity or an age check, complete
[EUDI onboarding](eudi-onboarding.md) and run your registered verifier service.
Set `VERIFIER_MODE=self-hosted` and its internal origin in
`VERIFIER_INTERNAL_URL`. The app proxies only the required wallet paths.
The tested reference image is pinned in `scripts/check-reference.py`.

## Acceptance

Check `/healthz`, protected operator routes and public handoff routes first.
Complete an actual issuer-signed phone presentation before claiming the
integration works end to end. Repeat with a wrong expected name, a decline,
an expired link and a consumed link. See the [phone guide](phone-test-setup.md).
Automated fixtures do not replace this acceptance step.
