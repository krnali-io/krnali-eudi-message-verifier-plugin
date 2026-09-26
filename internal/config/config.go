package config

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net"
	"net/mail"
	"net/url"
	"os"
	"strings"
	"time"
)

type Config struct {
	PublicURL, ListenAddr, VerifierURL, IssuerChain, BusinessName string
	Operators                                                     map[string]string
	OperatorUser, OperatorPassword, AccessIssuer, AccessAudience  string
	LinkTTL, VerifyWindow, ClaimsTTL                              time.Duration
	ResponseMode, WalletScheme                                    string
	RegistrationCertificate                                       string
	TrustProxy                                                    bool
	Demo                                                          bool
	HostedSandbox                                                 bool
	Env                                                           map[string]string
}

func Local(u string) bool {
	v, e := url.Parse(u)
	if e != nil {
		return false
	}
	h := v.Hostname()
	ip := net.ParseIP(h)
	return h == "localhost" || (ip != nil && ip.IsLoopback())
}
func Load() (Config, error)                         { return Parse(os.Getenv) }
func Parse(get func(string) string) (Config, error) { return parse(get, false) }

// ParseDemo is used only by the separate verifylink-demo executable. It retains
// operator authentication but does not load live verifier or channel credentials.
func ParseDemo(get func(string) string) (Config, error) {
	return parse(func(k string) string {
		if k == "VERIFIER_INTERNAL_URL" {
			return "http://127.0.0.1:1"
		}
		return get(k)
	}, true)
}

func parse(get func(string) string, demo bool) (Config, error) {
	c := Config{Operators: map[string]string{}, Env: map[string]string{}}
	def := func(k, v string) string {
		if s := get(k); s != "" {
			return s
		}
		return v
	}
	c.PublicURL = strings.TrimRight(get("PUBLIC_BASE_URL"), "/")
	c.VerifierURL = strings.TrimRight(get("VERIFIER_INTERNAL_URL"), "/")
	c.ListenAddr = def("LISTEN_ADDR", ":8080")
	c.BusinessName = get("BUSINESS_NAME")
	validURL := func(raw string, public bool) bool {
		u, e := url.Parse(raw)
		return e == nil && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.Path == "" && (u.Scheme == "https" || (u.Scheme == "http" && (!public || Local(raw))))
	}
	if !validURL(c.PublicURL, true) || !validURL(c.VerifierURL, false) || strings.TrimSpace(c.BusinessName) == "" || len(c.BusinessName) > 200 {
		return c, errors.New("invalid required URL or BUSINESS_NAME configuration")
	}
	mode := def("VERIFIER_MODE", "self-hosted")
	if mode != "self-hosted" && mode != "eudi-sandbox" {
		return c, errors.New("invalid VERIFIER_MODE")
	}
	c.HostedSandbox = mode == "eudi-sandbox"
	if c.HostedSandbox && (demo || c.VerifierURL != "https://verifier-backend.eudiw.dev") {
		return c, errors.New("eudi-sandbox requires the official hosted verifier and the live executable")
	}
	for _, item := range strings.Split(get("OPERATORS"), ",") {
		email, role, ok := strings.Cut(strings.TrimSpace(item), ":")
		a, e := mail.ParseAddress(email)
		if !ok || e != nil || a.Address != email || (role != "agent" && role != "supervisor") {
			return c, errors.New("invalid OPERATORS configuration")
		}
		c.Operators[strings.ToLower(email)] = role
	}
	c.OperatorUser = strings.ToLower(get("OPERATOR_USER"))
	c.OperatorPassword = get("OPERATOR_PASSWORD")
	c.AccessIssuer = strings.TrimRight(get("CF_ACCESS_ISSUER"), "/")
	c.AccessAudience = get("CF_ACCESS_AUDIENCE")
	if c.OperatorUser != "" || c.OperatorPassword != "" {
		if !Local(c.PublicURL) || c.OperatorPassword == "" || c.Operators[c.OperatorUser] == "" {
			return c, errors.New("basic auth requires localhost and a configured operator email")
		}
	}
	if c.OperatorUser == "" {
		u, e := url.Parse(c.AccessIssuer)
		if e != nil || u.Scheme != "https" || !strings.HasSuffix(u.Hostname(), ".cloudflareaccess.com") || u.Path != "" || u.RawQuery != "" || u.User != nil || c.AccessAudience == "" {
			return c, errors.New("CF_ACCESS_ISSUER and CF_ACCESS_AUDIENCE required")
		}
	}
	for _, v := range []struct {
		k string
		d time.Duration
		p *time.Duration
	}{{"LINK_TTL", 10 * time.Minute, &c.LinkTTL}, {"VERIFY_WINDOW", 5 * time.Minute, &c.VerifyWindow}, {"CLAIMS_TTL", 10 * time.Minute, &c.ClaimsTTL}} {
		*v.p = v.d
		if s := get(v.k); s != "" {
			d, e := time.ParseDuration(s)
			if e != nil || d <= 0 || d > 24*time.Hour {
				return c, errors.New("invalid TTL configuration")
			}
			*v.p = d
		}
	}
	c.ResponseMode = def("RESPONSE_MODE", "direct_post")
	if c.ResponseMode != "direct_post" && c.ResponseMode != "direct_post.jwt" {
		return c, errors.New("invalid RESPONSE_MODE")
	}
	c.WalletScheme = strings.TrimSuffix(def("WALLET_SCHEME", "haip-vp"), "://")
	if c.WalletScheme != "haip-vp" && c.WalletScheme != "openid4vp" {
		return c, errors.New("invalid WALLET_SCHEME")
	}
	if demo {
		c.Demo = true
		c.TrustProxy = get("TRUST_PROXY") == "true"
		return c, nil
	}
	b, e := os.ReadFile(get("ISSUER_CHAIN_FILE"))
	if e != nil {
		return c, errors.New("ISSUER_CHAIN_FILE missing or unreadable")
	}
	rest := b
	count := 0
	for len(strings.TrimSpace(string(rest))) > 0 {
		block, tail := pem.Decode(rest)
		if block == nil || block.Type != "CERTIFICATE" {
			return c, errors.New("invalid issuer certificate PEM")
		}
		cert, e := x509.ParseCertificate(block.Bytes)
		if e != nil || time.Now().Before(cert.NotBefore) || !time.Now().Before(cert.NotAfter) {
			return c, errors.New("issuer certificate invalid or outside validity period")
		}
		rest = tail
		count++
	}
	if count == 0 {
		return c, errors.New("issuer certificate chain empty")
	}
	c.IssuerChain = string(b)
	b, e = os.ReadFile(get("REGISTRATION_CERTIFICATE_FILE"))
	if e != nil || len(b) > 65536 {
		return c, errors.New("REGISTRATION_CERTIFICATE_FILE missing or unreadable")
	}
	c.RegistrationCertificate = strings.TrimSpace(string(b))
	parts := strings.Split(c.RegistrationCertificate, ".")
	if len(parts) != 3 {
		return c, errors.New("registration certificate must be a compact signed JWT")
	}
	var header struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	h, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil || json.Unmarshal(h, &header) != nil || header.Alg == "" || header.Alg == "none" || header.Typ != "rc-wrp+jwt" || parts[2] == "" {
		return c, errors.New("invalid registration certificate JWT header")
	}
	var payload map[string]any
	b, e = base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil || json.Unmarshal(b, &payload) != nil || payload == nil {
		return c, errors.New("invalid registration certificate JWT payload")
	}
	// This is configuration syntax validation, not certificate trust verification.
	// The registered certificate is configured by the deployer and carried to the wallet.
	if exp, ok := payload["exp"].(float64); ok && exp <= float64(time.Now().Unix()) {
		return c, errors.New("registration certificate expired")
	}
	c.TrustProxy = get("TRUST_PROXY") == "true"
	for _, k := range []string{"TELEGRAM_BOT_TOKEN", "TELEGRAM_WEBHOOK_SECRET", "WHATSAPP_ACCESS_TOKEN", "WHATSAPP_PHONE_NUMBER_ID", "WHATSAPP_APP_SECRET", "WHATSAPP_VERIFY_TOKEN", "WHATSAPP_API_VERSION", "WHATSAPP_TEMPLATE_NAME", "WHATSAPP_TEMPLATE_LANG", "SMS_ACCOUNT_SID", "SMS_AUTH_TOKEN", "SMS_FROM", "SMTP_HOST", "SMTP_PORT", "SMTP_USER", "SMTP_PASSWORD", "EMAIL_FROM"} {
		c.Env[k] = get(k)
	}
	return c, nil
}
