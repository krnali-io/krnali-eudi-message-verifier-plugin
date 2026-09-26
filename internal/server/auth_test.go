package server

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/config"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAccessJWTDoesNotTrustEmailHeader(t *testing.T) {
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	a := authenticator{cfg: config.Config{AccessIssuer: "https://team.cloudflareaccess.com", AccessAudience: "audience", Operators: map[string]string{"agent@example.test": "agent"}}, keys: map[string]*rsa.PublicKey{"key": &key.PublicKey}, fetched: time.Now()}
	r := httptest.NewRequest("GET", "/api/me", nil)
	r.Header.Set("Cf-Access-Authenticated-User-Email", "agent@example.test")
	if _, e := a.identity(r); e == nil {
		t.Fatal("trusted unsigned email")
	}
	encode := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	sign := func(p map[string]any) string {
		h := encode(map[string]string{"alg": "RS256", "kid": "key"}) + "." + encode(p)
		sum := sha256.Sum256([]byte(h))
		sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
		return h + "." + base64.RawURLEncoding.EncodeToString(sig)
	}
	p := map[string]any{"iss": a.cfg.AccessIssuer, "aud": []string{"audience"}, "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "email": "agent@example.test", "type": "app"}
	r.Header.Set("Cf-Access-Jwt-Assertion", sign(p))
	if id, e := a.identity(r); e != nil || id.Role != "agent" {
		t.Fatal(id, e)
	}
	for k, v := range map[string]any{"iss": "https://wrong.cloudflareaccess.com", "aud": []string{"wrong"}, "exp": time.Now().Add(-time.Hour).Unix(), "type": "service_token", "nbf": time.Now().Add(time.Hour).Unix()} {
		bad := map[string]any{}
		for a, b := range p {
			bad[a] = b
		}
		bad[k] = v
		if _, e := a.jwt(context.Background(), sign(bad)); e == nil {
			t.Fatal("accepted", k)
		}
	}
	r.Header.Set("Cf-Access-Authenticated-User-Email", "boss@example.test")
	if _, e := a.identity(r); e == nil {
		t.Fatal("email mismatch accepted")
	}
}
