package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/channel"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/config"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/eventlog"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/session"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/verifier"
)

type fixture struct {
	s              *Server
	h              http.Handler
	clock          time.Time
	logs           bytes.Buffer
	ref            *httptest.Server
	mu             sync.Mutex
	response       any
	responseStatus int
	starts         int
	lastInit       verifier.Init
	code           string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{clock: time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC), response: map[string]string{"error": "PresentationNotSubmitted"}, responseStatus: 400, code: "correct-code"}
	f.ref = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == "POST" && r.URL.Path == "/ui/presentations/v2":
			f.starts++
			_ = json.NewDecoder(r.Body).Decode(&f.lastInit)
			jsonOut(w, 200, map[string]string{"transaction_id": "tx-1", "authorization_request_uri": "haip-vp://?client_id=test&request_uri=https%3A%2F%2Fexample.test%2Fwallet%2Frequest.jwt%2F1"})
		case strings.HasPrefix(r.URL.Path, "/ui/presentations/"):
			if code := r.URL.Query().Get("response_code"); code != "" && code != f.code {
				jsonOut(w, 400, map[string]string{"error": "InvalidResponseCode"})
				return
			}
			jsonOut(w, f.responseStatus, f.response)
		case strings.HasPrefix(r.URL.Path, "/wallet/"):
			_, _ = io.WriteString(w, "wallet")
		case r.URL.Path == "/utilities/validations/msoMdoc/deviceResponse":
			_ = r.ParseForm()
			if r.Form.Get("issuer_chain") != "PINNED ISSUER" {
				t.Error("missing utility issuer pin")
			}
			_, _ = io.WriteString(w, `[{"docType":"eu.europa.ec.eudi.pid.1","attributes":{"eu.europa.ec.eudi.pid.1":{"given_name":"Anna","family_name":"Larsen","age_over_18":true}}}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.ref.Close)
	c := config.Config{PublicURL: "http://localhost:8080", VerifierURL: f.ref.URL, IssuerChain: "PINNED ISSUER", RegistrationCertificate: "REGISTRATION CERTIFICATE", BusinessName: "krnali labs", OperatorUser: "agent@example.test", OperatorPassword: "local-test-password", Operators: map[string]string{"agent@example.test": "agent", "other@example.test": "agent", "boss@example.test": "supervisor"}, LinkTTL: 10 * time.Minute, VerifyWindow: 5 * time.Minute, ClaimsTTL: 10 * time.Minute, ResponseMode: "direct_post", WalletScheme: "haip-vp", Env: map[string]string{}}
	now := func() time.Time { return f.clock }
	f.s = New(c, session.New(now, c.LinkTTL, c.VerifyWindow, c.ClaimsTTL), verifier.New(f.ref.URL), channel.Build(c.Env, now), eventlog.New(&f.logs))
	f.h = f.s.Handler()
	return f
}
func (f *fixture) request(method, path, body string, auth, origin bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if auth {
		r.SetBasicAuth("agent@example.test", "local-test-password")
	}
	if origin {
		r.Header.Set("Origin", f.s.Config.PublicURL)
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w
}
func (f *fixture) create(t *testing.T, extra string) (session.Session, string) {
	t.Helper()
	w := f.request("POST", "/api/sessions", `{"channel":"copylink","recipient":"PRIVATE RECIPIENT","preset":"confirm-name"`+extra+`}`, true, true)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var v struct {
		session.Session
		Link string `json:"link"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	return v.Session, strings.TrimPrefix(v.Link, f.s.Config.PublicURL)
}
func (f *fixture) answer(format string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responseStatus = 200
	if format == "mdoc" {
		f.response = map[string]any{"vp_token": map[string][]string{"pid_mdoc": {"device-response"}}}
		return
	}
	p := base64.RawURLEncoding.EncodeToString([]byte(`{"vct":"urn:eudi:pid:1","given_name":"Anna","family_name":"Larsen","age_equal_or_over":{"18":true}}`))
	f.response = map[string]any{"vp_token": map[string][]string{"pid_sdjwt": {"header." + p + ".signature~"}}}
}

func TestHostedSandboxKeepsRegistrationScopeAndAuth(t *testing.T) {
	f := setup(t)
	f.s.Config.HostedSandbox = true
	if w := f.request("POST", "/api/sessions", `{"channel":"copylink","recipient":"Test","preset":"over-18"}`, true, true); w.Code != 400 || !strings.Contains(w.Body.String(), "preset_not_registered") {
		t.Fatal("hosted registration must not authorize an age claim", w.Code)
	}
	if w := f.request("GET", "/api/sessions", "", false, false); w.Code != 401 {
		t.Fatal("hosted verification removed operator authentication")
	}
	for _, path := range []string{"/wallet/request.jwt/test", "/ui/presentations", "/demo/wallet/test"} {
		if w := f.request("GET", path, "", false, false); w.Code != 404 {
			t.Fatal("unexpected public proxy or simulation route", path, w.Code)
		}
	}
	f.create(t, "")
	if f.starts != 0 {
		t.Fatal("creating a link started a wallet transaction")
	}
	if w := f.request("GET", "/console", "", true, false); !strings.Contains(w.Body.String(), "EUDI sandbox") || strings.Contains(w.Body.String(), `value="over-18"`) || strings.Contains(w.Body.String(), "Interactive demo") {
		t.Fatal("incorrect hosted verifier disclosure or available claims")
	}
}

func TestFlowPreviewSingleUseAndPrivacy(t *testing.T) {
	f := setup(t)
	v, link := f.create(t, `,"expected_name":"Anna Larsen"`)
	for i := 0; i < 2; i++ {
		if w := f.request("GET", link, "", false, false); w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	if f.starts != 0 {
		t.Fatal("GET started transaction")
	}
	w := f.request("POST", link+"/start", "", false, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var start map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &start)
	if f.lastInit.IssuerChain != "PINNED ISSUER" || f.lastInit.RegistrationCertificate != "REGISTRATION CERTIFICATE" || f.lastInit.Nonce == "" || !strings.Contains(f.lastInit.RedirectTemplate, "{RESPONSE_CODE}") {
		t.Fatal("missing start binding")
	}
	if w = f.request("POST", link+"/start", "", false, true); w.Code != 410 {
		t.Fatal(w.Code)
	}
	f.answer("sdjwt")
	f.s.PollOnce(context.Background())
	v, _ = f.s.Store.Get(v.ID)
	if v.Status != session.Verified || v.Result.GivenName != "Anna" {
		t.Fatal(v)
	}
	w = f.request("GET", "/s/"+start["poll_key"].(string), "", false, false)
	if strings.Contains(w.Body.String(), "Anna") || !strings.Contains(w.Body.String(), "verified") {
		t.Fatal(w.Body.String())
	}
	w = f.request("GET", "/api/sessions/"+v.ID, "", true, false)
	if strings.Contains(w.Body.String(), link) || strings.Contains(w.Body.String(), "poll_key") {
		t.Fatal("secret in API")
	}
	for _, s := range []string{"Anna", "Larsen", "PRIVATE RECIPIENT", strings.TrimPrefix(link, "/v/"), start["poll_key"].(string), "local-test-password"} {
		if strings.Contains(f.logs.String(), s) {
			t.Fatalf("private value in logs")
		}
	}
	f.clock = f.clock.Add(10 * time.Minute)
	w = f.request("GET", "/api/sessions/"+v.ID, "", true, false)
	if strings.Contains(w.Body.String(), "Anna") || !strings.Contains(w.Body.String(), `"result_wiped":true`) {
		t.Fatal(w.Body.String())
	}
}
func TestInvalidInputOriginAndPublicBoundary(t *testing.T) {
	f := setup(t)
	for _, body := range []string{`{"channel":"copylink","recipient":"label","preset":"anything"}`, `{"channel":"copylink","recipient":"label","preset":"confirm-name","claims":["birth_date"]}`, `{"channel":"copylink","recipient":"label","preset":"over-18","expected_name":"Anna"}`} {
		if w := f.request("POST", "/api/sessions", body, true, true); w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	if f.starts != 0 {
		t.Fatal("invalid query reached verifier")
	}
	_, link := f.create(t, "")
	if w := f.request("POST", link+"/start", "", false, false); w.Code != 403 {
		t.Fatal(w.Code)
	}
	for _, path := range []string{"/ui/presentations", "/utilities/validations/msoMdoc/deviceResponse", "/wallet/../ui/presentations", "/wallet/%2e%2e/ui/presentations"} {
		if w := f.request("GET", path, "", false, false); w.Code != 404 {
			t.Fatal(path, w.Code)
		}
	}
	if w := f.request("GET", "/wallet/request.jwt/123", "", false, false); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := f.request("GET", "/api/sessions", "", false, false); w.Code != 401 {
		t.Fatal(w.Code)
	}
}
func TestOutcomesAndInvalidRedirect(t *testing.T) {
	for _, outcome := range []string{"mismatch", "declined", "failed", "mdoc"} {
		t.Run(outcome, func(t *testing.T) {
			f := setup(t)
			extra := ""
			if outcome == "mismatch" {
				extra = `,"expected_name":"Different Person"`
			}
			v, link := f.create(t, extra)
			f.request("POST", link+"/start", "", false, true)
			f.answer("sdjwt")
			w := f.request("GET", "/done/"+v.ID+"?response_code=wrong", "", false, false)
			if strings.Contains(w.Body.String(), "Anna") {
				t.Fatal("claims on done page")
			}
			v, _ = f.s.Store.Get(v.ID)
			if v.Status != session.Verifying {
				t.Fatal("invalid response code changed state")
			}
			want := session.Mismatch
			switch outcome {
			case "declined":
				f.response = map[string]string{"error": "access_denied", "error_description": "PRIVATE DESCRIPTION"}
				want = session.Declined
			case "failed":
				f.response = map[string]string{"error": "PRIVATE ERROR CONTAINING NAME"}
				want = session.Failed
			case "mdoc":
				f.answer("mdoc")
				want = session.Verified
			}
			f.s.PollOnce(context.Background())
			v, _ = f.s.Store.Get(v.ID)
			if v.Status != want {
				t.Fatal(v.Status, want)
			}
			if strings.Contains(f.logs.String(), "PRIVATE") {
				t.Fatal("provider error leaked")
			}
		})
	}
}
func TestExpiryCancellationResendOwnershipExport(t *testing.T) {
	f := setup(t)
	v, link := f.create(t, `,"case_ref":"=formula"`)
	agent := Identity{"agent@example.test", "agent"}
	other := Identity{"other@example.test", "agent"}
	boss := Identity{"boss@example.test", "supervisor"}
	r := httptest.NewRequest("GET", "/api/sessions", nil)
	r.SetPathValue("id", v.ID)
	for _, fn := range []func(http.ResponseWriter, *http.Request, Identity){f.s.get, f.s.cancel, f.s.resend} {
		w := httptest.NewRecorder()
		fn(w, r, other)
		if w.Code != 404 && w.Code != 403 {
			t.Fatal("cross-agent access", w.Code)
		}
	}
	if len(f.s.selected(r, other)) != 0 || len(f.s.selected(r, boss)) != 1 {
		t.Fatal("list ownership")
	}
	w := httptest.NewRecorder()
	f.s.export(w, r, agent)
	if w.Code != 403 {
		t.Fatal("agent exported")
	}
	w = httptest.NewRecorder()
	f.s.export(w, r, boss)
	if strings.Contains(w.Body.String(), "PRIVATE RECIPIENT") || !strings.Contains(w.Body.String(), "'=formula") {
		t.Fatal("CSV privacy or formula escaping")
	}
	f.clock = f.clock.Add(10 * time.Minute)
	if w = f.request("POST", link+"/start", "", false, true); w.Code != 410 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	f.s.resend(w, r, boss)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var next struct {
		session.Session
		Link string
	}
	_ = json.Unmarshal(w.Body.Bytes(), &next)
	if next.ID == v.ID || strings.HasSuffix(next.Link, link) {
		t.Fatal("resend reused link")
	}
	if w = f.request("POST", link+"/start", "", false, true); w.Code != 410 {
		t.Fatal("old link revived")
	}
	r.SetPathValue("id", next.ID)
	w = httptest.NewRecorder()
	f.s.cancel(w, r, boss)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w = f.request("POST", strings.TrimPrefix(next.Link, f.s.Config.PublicURL)+"/start", "", false, true); w.Code != 410 {
		t.Fatal("cancelled link worked")
	}
}
func TestWebhookAuthenticationBeforeParsingAndLimits(t *testing.T) {
	f := setup(t)
	for _, p := range []string{"/hooks/telegram", "/hooks/whatsapp"} {
		w := f.request("POST", p, "not json", false, false)
		if w.Code != 401 {
			t.Fatal(p, w.Code)
		}
	}
	for i := 0; i < 10; i++ {
		f.create(t, "")
	}
	if w := f.request("POST", "/api/sessions", `{}`, true, true); w.Code != 429 {
		t.Fatal(w.Code)
	}
	f.clock = f.clock.Add(time.Minute)
	if w := f.request("POST", "/api/sessions", `{}`, true, true); w.Code == 429 {
		t.Fatal("rate limit did not reset")
	}
	for i := 0; i < 20; i++ {
		f.request("POST", fmt.Sprintf("/v/invalid%d/start", i), "", false, true)
	}
	if w := f.request("POST", "/v/invalid/start", "", false, true); w.Code != 429 {
		t.Fatal(w.Code)
	}
}
