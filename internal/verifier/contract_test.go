package verifier

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/preset"
	"os"
	"slices"
	"testing"
	"time"
)

// Run explicitly against the pinned reference image, using only a synthetic request.
// This checks the real HTTP contract; it does not claim wallet interoperability.
func TestReferenceContract(t *testing.T) {
	base := os.Getenv("REFERENCE_VERIFIER_URL")
	if base == "" {
		t.Skip("reference container not configured")
	}
	chain, e := os.ReadFile(os.Getenv("REFERENCE_ISSUER_CHAIN_FILE"))
	if e != nil {
		t.Fatal(e)
	}
	registration, e := os.ReadFile(os.Getenv("REFERENCE_REGISTRATION_CERTIFICATE_FILE"))
	if e != nil {
		t.Fatal(e)
	}
	c := New(base)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p, _ := preset.Get("confirm-name")
	nonce := make([]byte, 32)
	_, _ = rand.Read(nonce)
	tx, e := c.Start(ctx, Init{DCQL: p.DCQL, Nonce: base64.RawURLEncoding.EncodeToString(nonce), ResponseMode: "direct_post", JARMode: "by_reference", RequestURIMethod: "get", RedirectTemplate: "https://verify.krnali.io/done/contract?response_code={RESPONSE_CODE}", IssuerChain: string(chain), Scheme: "haip-vp", Profile: "openid4vp", RegistrationCertificate: string(registration)})
	if e != nil {
		in := Init{DCQL: p.DCQL, Nonce: base64.RawURLEncoding.EncodeToString(nonce), ResponseMode: "direct_post", JARMode: "by_reference", RequestURIMethod: "get", RedirectTemplate: "https://verify.krnali.io/done/contract?response_code={RESPONSE_CODE}", IssuerChain: string(chain), Scheme: "haip-vp", Profile: "openid4vp", RegistrationCertificate: string(registration)}
		body, _ := json.Marshal(in)
		raw, code, _ := c.request(ctx, "POST", "/ui/presentations/v2", "application/json", bytes.NewReader(body))
		var diagnostic map[string]json.RawMessage
		_ = json.Unmarshal(raw, &diagnostic)
		keys := []string{}
		for k := range diagnostic {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		t.Fatalf("reference init failed: %v; HTTP %d; fields=%v; protocol error=%s", e, code, keys, diagnostic["error"])
	}
	if _, e = c.Response(ctx, tx.ID, ""); !errors.Is(e, ErrPending) {
		t.Fatalf("pending response contract: %v", e)
	}
	if _, e = c.DecodeMDoc(ctx, "invalid-device-response", string(chain)); !errors.Is(e, ErrVerifier) {
		t.Fatal("reference accepted an invalid DeviceResponse")
	}
}
