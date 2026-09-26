package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/channel"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type hookTransport func(*http.Request) (*http.Response, error)

func (f hookTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestSignedHooksDeduplicateAndCapBodies(t *testing.T) {
	for _, kind := range []string{"telegram", "whatsapp"} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t)
			env := map[string]string{"TELEGRAM_BOT_TOKEN": "bot", "TELEGRAM_WEBHOOK_SECRET": "telegram-secret", "WHATSAPP_ACCESS_TOKEN": "access", "WHATSAPP_PHONE_NUMBER_ID": "123", "WHATSAPP_APP_SECRET": "app-secret", "WHATSAPP_VERIFY_TOKEN": "verify-secret", "WHATSAPP_API_VERSION": "vTEST"}
			f.s.Config.Env = env
			f.s.Channels = channel.Build(env, f.s.Store.Now)
			calls := 0
			a := f.s.Channels[kind].(*channel.Adapter)
			a.HTTP = &http.Client{Transport: hookTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: make(http.Header)}, nil
			})}
			request := func(body string, valid bool) *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", "/hooks/"+kind, strings.NewReader(body))
				if kind == "telegram" && valid {
					r.Header.Set("X-Telegram-Bot-Api-Secret-Token", "telegram-secret")
				}
				if kind == "whatsapp" && valid {
					mac := hmac.New(sha256.New, []byte("app-secret"))
					_, _ = mac.Write([]byte(body))
					r.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
				}
				w := httptest.NewRecorder()
				f.h.ServeHTTP(w, r)
				return w
			}
			if w := request("not-json", false); w.Code != 401 {
				t.Fatal("parsed unauthenticated body", w.Code)
			}
			if w := request(strings.Repeat("x", 65537), true); w.Code != 413 {
				t.Fatal("oversized body", w.Code)
			}
			body := fmt.Sprintf(`{"update_id":1,"message":{"date":%d,"text":" VeRiFy ","chat":{"id":1234821,"type":"private"}}}`, f.clock.Unix())
			if kind == "whatsapp" {
				body = fmt.Sprintf(`{"object":"whatsapp_business_account","entry":[{"changes":[{"value":{"metadata":{"phone_number_id":"123"},"messages":[{"from":"4521216602","id":"message-1","type":"text","timestamp":"%d","text":{"body":" VeRiFy "}}]}}]}]}`, f.clock.Unix())
			}
			for i := 0; i < 2; i++ {
				if w := request(body, true); w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
			}
			items := f.s.Store.List()
			if calls != 1 || len(items) != 1 || items[0].Origin != "customer" || items[0].Preset != "confirm-name" {
				t.Fatal("hook or dedup failed", calls, len(items))
			}
			if len(f.s.selected(httptest.NewRequest("GET", "/api/sessions", nil), Identity{"agent@example.test", "agent"})) != 0 {
				t.Fatal("customer request visible to unrelated agent")
			}
		})
	}
}
