package server

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/config"
)

type Identity struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}
type authenticator struct {
	cfg     config.Config
	mu      sync.Mutex
	keys    map[string]*rsa.PublicKey
	fetched time.Time
	client  *http.Client
}

var errAuth = errors.New("unauthorized")

func equal(a, b string) bool {
	x := sha256.Sum256([]byte(a))
	y := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(x[:], y[:]) == 1
}
func (a *authenticator) identity(r *http.Request) (Identity, error) {
	email := ""
	if a.cfg.OperatorUser != "" {
		u, p, ok := r.BasicAuth()
		if !ok || !equal(u, a.cfg.OperatorUser) || !equal(p, a.cfg.OperatorPassword) {
			return Identity{}, errAuth
		}
		email = a.cfg.OperatorUser
	} else {
		var e error
		email, e = a.jwt(r.Context(), r.Header.Get("Cf-Access-Jwt-Assertion"))
		if e != nil {
			return Identity{}, e
		}
		if h := r.Header.Get("Cf-Access-Authenticated-User-Email"); h != "" && !strings.EqualFold(h, email) {
			return Identity{}, errAuth
		}
	}
	role, ok := a.cfg.Operators[strings.ToLower(email)]
	if !ok {
		return Identity{}, errAuth
	}
	return Identity{strings.ToLower(email), role}, nil
}
func (a *authenticator) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if time.Since(a.fetched) < time.Hour {
		if k := a.keys[kid]; k != nil {
			return k, nil
		}
		if time.Since(a.fetched) < time.Minute {
			return nil, errAuth
		}
	}
	r, e := http.NewRequestWithContext(ctx, "GET", a.cfg.AccessIssuer+"/cdn-cgi/access/certs", nil)
	if e != nil {
		return nil, errAuth
	}
	res, e := a.client.Do(r)
	if e != nil {
		return nil, errAuth
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, errAuth
	}
	var jwks struct {
		Keys []struct{ Kty, Kid, N, E, Alg, Use string }
	}
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&jwks) != nil {
		return nil, errAuth
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range jwks.Keys {
		if k.Kty != "RSA" || (k.Alg != "" && k.Alg != "RS256") || (k.Use != "" && k.Use != "sig") {
			continue
		}
		n, e1 := base64.RawURLEncoding.DecodeString(k.N)
		eb, e2 := base64.RawURLEncoding.DecodeString(k.E)
		if e1 != nil || e2 != nil || len(n) < 256 || len(eb) > 4 {
			continue
		}
		ex := 0
		for _, b := range eb {
			ex = ex*256 + int(b)
		}
		if ex < 3 || ex%2 == 0 {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: ex}
	}
	a.keys = keys
	a.fetched = time.Now()
	if keys[kid] == nil {
		return nil, errAuth
	}
	return keys[kid], nil
}
func (a *authenticator) jwt(ctx context.Context, raw string) (string, error) {
	if len(raw) > 16384 {
		return "", errAuth
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return "", errAuth
	}
	decode := func(s string, v any) error {
		b, e := base64.RawURLEncoding.DecodeString(s)
		if e != nil {
			return e
		}
		return json.Unmarshal(b, v)
	}
	var h struct{ Alg, Kid string }
	if decode(parts[0], &h) != nil || h.Alg != "RS256" || h.Kid == "" {
		return "", errAuth
	}
	key, e := a.key(ctx, h.Kid)
	if e != nil {
		return "", errAuth
	}
	sig, e := base64.RawURLEncoding.DecodeString(parts[2])
	if e != nil {
		return "", errAuth
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig) != nil {
		return "", errAuth
	}
	var p struct {
		Iss   string   `json:"iss"`
		Aud   []string `json:"aud"`
		Exp   int64    `json:"exp"`
		Nbf   int64    `json:"nbf"`
		Iat   int64    `json:"iat"`
		Email string   `json:"email"`
		Type  string   `json:"type"`
	}
	if decode(parts[1], &p) != nil || p.Iss != a.cfg.AccessIssuer || p.Exp <= time.Now().Unix() || p.Nbf > time.Now().Unix()+30 || p.Iat > time.Now().Unix()+30 || p.Email == "" || p.Type != "app" {
		return "", errAuth
	}
	for _, aud := range p.Aud {
		if equal(aud, a.cfg.AccessAudience) {
			return p.Email, nil
		}
	}
	return "", errAuth
}
