# EUDI test-bed onboarding for Verify Link

These steps register krnali as its own relying party. The [hosted EUDI sandbox integration](deployment.md#hosted-eudi-sandbox) uses the official reference verifier under its own identity and does not require a krnali private key.

## 1. Authenticate and select the registered entity

Open the [EUDI RP registry](https://registry.serviceproviders.eudiw.dev/) and authenticate with an EUDI reference wallet containing a test PID. This wallet action needs the holder's participation. The [registry guide](https://registry.serviceproviders.eudiw.dev/guide) describes the person → legal entity → provider → wallet relying party → intended-use workflow.

Use the existing registered entity if there is one. Otherwise supply its actual legal name, country, registration identifier, contact details and the registry's required policy/support information. `krnali labs` is the project brand; it does not establish the legal entity, identifier, legal basis, public-body status or supervisory authority. Those fields remain unfilled until the owner confirms them. The registry session identifier (`hash_pid`) belongs in protected local session storage, not chat, Git or command logs.

## 2. Register the technical scope

| Field | Project value |
|---|---|
| Display/trade name | krnali labs · Verify Link |
| Application URL | `https://verify.krnali.io` |
| Service description | Test-wallet verification of a person's name or over-18 status through a single-use link. |
| Intended use | Confirm the name or over-18 status requested by a staff member during a test conversation. |
| Credential types | EUDI test PID, SD-JWT and mdoc |
| SD-JWT format / VCT | `dc+sd-jwt` / `urn:eudi:pid:1` |
| mdoc format / doctype | `mso_mdoc` / `eu.europa.ec.eudi.pid.1` |

Cover only these claims in the intended use. These are the application's actual DCQL claim paths; use the registry's claim picker/schema for its own representation:

| Purpose | SD-JWT path | mdoc path |
|---|---|---|
| Given name | `["given_name"]` | `["eu.europa.ec.eudi.pid.1", "given_name"]` |
| Family name | `["family_name"]` | `["eu.europa.ec.eudi.pid.1", "family_name"]` |
| Over 18 | `["age_equal_or_over", "18"]` | `["eu.europa.ec.eudi.pid.1", "age_over_18"]` |

The name presets request only the two name attributes. The age preset requests only the age boolean. Each permits one supported credential format; a holder does not have to disclose both. No date of birth, address or national identifier is requested by Verify Link. The registry's own login may request different attributes.

## 3. Export both certificates

The [live API schema](https://registry.serviceproviders.eudiw.dev/apispec_1.json) provides:

- `POST /wallet_rp/certificate`: `hash_pid`, `wrp_id`, and a locally chosen password. Its `data.file_base64` is a standard-base64 PKCS#12 archive containing the RP key and certificate.
- `POST /intended_use/certificate`: `hash_pid` and `intended_use_id`. Its `data.file_base64` is a URL-safe-base64 **compact JWT**. `data.cose_base64` is a separate format that our verifier does not accept for this field.

The registry may label both outputs `document_with_signature.json`. Preserve which API produced each download. The [registry source at `54e77e8`](https://github.com/eu-digital-identity-wallet/eudi-srv-web-relyingparty-registration-py/blob/54e77e85c27d3771b4db43c588d487eea961b7cc/app/RPR_routes.py) constructs the compact JWT from the JAdES result before wrapping it in base64. The published schema's generic signed-document description is less precise; the running service's output still needs checking.

If the download contains the API JSON response, unpack it locally without printing its contents:

```sh
umask 077
mkdir -p secrets/eudi
python3 scripts/unpack-eudi-export.py p12 /secure/path/rp-response.json \
  --out secrets/eudi/rp.p12
python3 scripts/unpack-eudi-export.py registration /secure/path/registration-response.json \
  --out secrets/eudi/registration.jwt
```

Already-decoded P12/JWT files can be used directly. The helper does not overwrite existing files and creates outputs with mode `0600`. It checks the export shape only; it does **not** verify certificate trust, signature validity, expiry or attribute authorization. It neither contacts the registry nor modifies Kubernetes.

## 4. Inspect the access certificate before selecting a client ID

Inspect the P12 using `keytool -list -v -keystore secrets/eudi/rp.p12 -storetype PKCS12`, entering the password at the prompt. Record the private-key entry's alias, signing algorithm, leaf certificate validity and subject alternative names. The source currently generates a P-256 key, suitable for ES256; inspect the actual issued material before relying on that assumption.

The [registry's CSR builder](https://github.com/eu-digital-identity-wallet/eudi-srv-web-relyingparty-registration-py/blob/54e77e85c27d3771b4db43c588d487eea961b7cc/app/EJBCA_and_DB_func.py) requests a **URI SAN** from the RP's first support URI, not a DNS SAN. The issuing CA could add extensions, so inspect the issued certificate. Do not assume it contains `DNS:verify.krnali.io`.

- If the leaf contains that DNS SAN, use the staged `x509_san_dns` configuration.
- Otherwise use `VERIFIER_CLIENTIDPREFIX=x509_hash` and set `VERIFIER_ORIGINALCLIENTID` to the unpadded base64url SHA-256 of the **leaf certificate's DER bytes**. The public URL remains `https://verify.krnali.io`. The wallet must support this client-ID scheme.

One way to inspect the public leaf and calculate its hash is:

```sh
umask 077
openssl pkcs12 -in secrets/eudi/rp.p12 -clcerts -nokeys -out secrets/eudi/rp-leaf.pem
openssl x509 -in secrets/eudi/rp-leaf.pem -noout -dates -ext subjectAltName
openssl x509 -in secrets/eudi/rp-leaf.pem -outform DER |
  openssl dgst -sha256 -binary |
  openssl base64 -A | tr '+/' '-_' | tr -d '='
```

The password is prompted for; the final output is a public certificate identifier, not a private key. Confirm the downloaded registration JWT's signature/type/chain against the pinned verifier and its requested attributes against the registration before activation. A syntactically accepted JWT is not proof that a wallet trusts its issuer.

## 5. Finish deployment and phone acceptance

Provide the protected file paths for `rp.p12`, `registration.jwt`, the RP settings env file, and the confirmed test-PID issuer chain. Do not paste passwords into chat. The RP signing chain, registration-signing chain and PID-issuer chain serve different purposes; the PID issuer chain must match the credentials on the test phones.

Continue with [installing secrets and activating](deployment.md#dedicated-relying-party). After successful readiness, publish this hostname's tunnel/DNS route and test copy-link first: SD-JWT name, mdoc name, over-18, decline, wrong expected name, expired link and replay. Record phone/wallet versions, certificate fingerprints and timings. Live messaging adapters remain a subsequent test requiring their configured provider credentials.
