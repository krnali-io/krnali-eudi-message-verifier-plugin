package config

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFailClosedConfiguration(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, _ := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	path := filepath.Join(t.TempDir(), "issuer.pem")
	_ = os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600)
	registration := filepath.Join(t.TempDir(), "registration.jwt")
	_ = os.WriteFile(registration, []byte(base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256","typ":"rc-wrp+jwt"}`))+"."+base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"fixture"}`))+".signature"), 0600)
	env := map[string]string{"PUBLIC_BASE_URL": "http://localhost:8080", "VERIFIER_INTERNAL_URL": "http://verifier:8080", "ISSUER_CHAIN_FILE": path, "BUSINESS_NAME": "krnali labs", "OPERATORS": "agent@example.test:agent", "OPERATOR_USER": "agent@example.test", "OPERATOR_PASSWORD": "test"}
	env["REGISTRATION_CERTIFICATE_FILE"] = registration
	get := func(k string) string { return env[k] }
	if _, e := Parse(get); e != nil {
		t.Fatal(e)
	}
	for k, v := range map[string]string{"PUBLIC_BASE_URL": "https://verify.krnali.io", "ISSUER_CHAIN_FILE": "missing", "REGISTRATION_CERTIFICATE_FILE": "missing", "LINK_TTL": "0s", "OPERATORS": "user:god", "RESPONSE_MODE": "invalid", "WALLET_SCHEME": "javascript:", "VERIFIER_MODE": "unrecognized", "VERIFIER_INTERNAL_URL": "http://verifier/ui/"} {
		old := env[k]
		env[k] = v
		if _, e := Parse(get); e == nil {
			t.Fatal("accepted", k)
		}
		env[k] = old
	}
	env["VERIFIER_MODE"] = "eudi-sandbox"
	if _, e := Parse(get); e == nil {
		t.Fatal("hosted sandbox allowed an unrelated verifier")
	}
	env["VERIFIER_INTERNAL_URL"] = "https://verifier-backend.eudiw.dev"
	if c, e := Parse(get); e != nil || !c.HostedSandbox || c.Demo {
		t.Fatal("hosted sandbox must use real verification", e)
	}
	if _, e := ParseDemo(get); e == nil {
		t.Fatal("synthetic executable cannot claim hosted sandbox verification")
	}

}

func TestDemoIsSeparateAndStillRequiresAuthentication(t *testing.T) {
	env := map[string]string{"PUBLIC_BASE_URL": "https://verify.krnali.io", "BUSINESS_NAME": "krnali labs", "OPERATORS": "agent@example.test:supervisor", "CF_ACCESS_ISSUER": "https://example.cloudflareaccess.com", "CF_ACCESS_AUDIENCE": "demo-audience", "TELEGRAM_BOT_TOKEN": "must-not-be-loaded", "VERIFYLINK_DEMO": "true"}
	get := func(k string) string { return env[k] }
	c, err := ParseDemo(get)
	if err != nil || !c.Demo || len(c.Env) != 0 || c.IssuerChain != "" || c.RegistrationCertificate != "" {
		t.Fatal(c, err)
	}
	if _, err = Parse(get); err == nil {
		t.Fatal("live configuration bypassed trust requirements")
	}
	env["CF_ACCESS_AUDIENCE"] = ""
	if _, err = ParseDemo(get); err == nil {
		t.Fatal("demo accepted missing authentication")
	}
	env["OPERATOR_USER"] = "agent@example.test"
	env["OPERATOR_PASSWORD"] = "test"
	if _, err = ParseDemo(get); err == nil {
		t.Fatal("demo accepted public basic auth")
	}
}
