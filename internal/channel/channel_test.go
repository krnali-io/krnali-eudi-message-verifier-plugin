package channel

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestValidationAndMask(t *testing.T) {
	for _, tc := range []struct{ kind, valid, invalid string }{{"email", "anna@example.com", "Anna <anna@example.com>"}, {"sms", "+4521216602", "4521216602"}, {"whatsapp", "+4521216602", "+000000000"}, {"telegram", "1234821", "username"}, {"copylink", "Anna on Signal", ""}} {
		a := &Adapter{Kind: tc.kind}
		if a.Validate(tc.valid) != nil || a.Validate(tc.invalid) == nil {
			t.Fatal(tc.kind)
		}
		if tc.kind != "copylink" && a.Mask(tc.valid) == tc.valid {
			t.Fatal("not masked")
		}
		if a.Validate(tc.valid+"\r\nInjected: value") == nil {
			t.Fatal("injection accepted")
		}
	}
}
func TestWhatsAppWindowAndTemplate(t *testing.T) {
	now := time.Now()
	a := &Adapter{Kind: "whatsapp", Now: func() time.Time { return now }, seen: map[string]time.Time{}, Env: map[string]string{"WHATSAPP_ACCESS_TOKEN": "secret", "WHATSAPP_PHONE_NUMBER_ID": "123", "WHATSAPP_API_VERSION": "vTEST"}}
	var payload map[string]any
	calls := 0
	a.HTTP = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing auth")
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	})}
	send := func() error {
		return a.Send(context.Background(), "+4521216602", "https://verify.example/v/token", Message{Business: "Test", Minutes: 10})
	}
	if e := send(); e != ErrWindow || calls != 0 {
		t.Fatal(e, calls)
	}
	a.Seen("+4521216602", now)
	if e := send(); e != nil || payload["type"] != "text" {
		t.Fatal(e, payload)
	}
	now = now.Add(24 * time.Hour)
	if send() != ErrWindow {
		t.Fatal("expired window allowed")
	}
	a.Env["WHATSAPP_TEMPLATE_NAME"] = "verify_identity"
	if e := send(); e != nil || payload["type"] != "template" {
		t.Fatal(e, payload)
	}
}
func TestDisabledChannels(t *testing.T) {
	m := Build(map[string]string{"TELEGRAM_BOT_TOKEN": "partial"}, time.Now)
	if len(m) != 1 || m["copylink"] == nil {
		t.Fatal(m)
	}
}
