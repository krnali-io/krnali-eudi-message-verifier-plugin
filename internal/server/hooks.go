package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/channel"
)

func readHook(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	b, e := io.ReadAll(r.Body)
	if e != nil {
		problem(w, 413, "body_too_large")
		return nil, false
	}
	return b, true
}
func (s *Server) inbound(r *http.Request, kind, sender, text, id string, t time.Time) {
	a, ok := s.Channels[kind].(*channel.Adapter)
	if !ok || a.Validate(sender) != nil {
		return
	}
	if !s.dedup.allow(kind+":"+id, 1, 24*time.Hour) {
		return
	}
	a.Seen(sender, t)
	if strings.EqualFold(strings.TrimSpace(text), "verify") {
		if !s.limits.allow("inbound:"+kind+":"+sender, 5, time.Hour) {
			return
		}
		_, _, _ = s.newSession(r.Context(), createRequest{Channel: kind, Recipient: sender, Preset: "confirm-name"}, "", "customer")
	} else {
		if !s.limits.allow("help:"+kind+":"+sender, 5, time.Hour) {
			return
		}
		_ = a.Send(r.Context(), sender, "", channel.Message{Text: "Send VERIFY to get a verification link."})
	}
}
func (s *Server) telegram(w http.ResponseWriter, r *http.Request) {
	secret := s.Config.Env["TELEGRAM_WEBHOOK_SECRET"]
	if s.Channels["telegram"] == nil || secret == "" || !equal(secret, r.Header.Get("X-Telegram-Bot-Api-Secret-Token")) {
		problem(w, 401, "unauthorized")
		return
	}
	b, ok := readHook(w, r)
	if !ok {
		return
	}
	var in struct {
		UpdateID int64 `json:"update_id"`
		Message  *struct {
			Date int64  `json:"date"`
			Text string `json:"text"`
			Chat struct {
				ID   int64  `json:"id"`
				Type string `json:"type"`
			} `json:"chat"`
		} `json:"message"`
	}
	if json.Unmarshal(b, &in) != nil {
		problem(w, 400, "invalid_body")
		return
	}
	if in.Message != nil && in.Message.Chat.Type == "private" {
		s.inbound(r, "telegram", strconv.FormatInt(in.Message.Chat.ID, 10), in.Message.Text, strconv.FormatInt(in.UpdateID, 10), time.Unix(in.Message.Date, 0))
	}
	jsonOut(w, 200, map[string]bool{"ok": true})
}
func (s *Server) whatsappChallenge(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	secret := s.Config.Env["WHATSAPP_VERIFY_TOKEN"]
	if s.Channels["whatsapp"] == nil || secret == "" || q.Get("hub.mode") != "subscribe" || !equal(secret, q.Get("hub.verify_token")) {
		problem(w, 401, "unauthorized")
		return
	}
	challenge := q.Get("hub.challenge")
	if len(challenge) > 256 {
		problem(w, 400, "invalid_challenge")
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = io.WriteString(w, challenge)
}
func (s *Server) whatsapp(w http.ResponseWriter, r *http.Request) {
	secret := s.Config.Env["WHATSAPP_APP_SECRET"]
	if s.Channels["whatsapp"] == nil || secret == "" {
		problem(w, 401, "unauthorized")
		return
	}
	b, ok := readHook(w, r)
	if !ok {
		return
	}
	raw, ok := strings.CutPrefix(r.Header.Get("X-Hub-Signature-256"), "sha256=")
	sig, e := hex.DecodeString(raw)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(b)
	if !ok || e != nil || !hmac.Equal(sig, mac.Sum(nil)) {
		problem(w, 401, "unauthorized")
		return
	}
	var in struct {
		Object string `json:"object"`
		Entry  []struct {
			Changes []struct {
				Value struct {
					Metadata struct {
						PhoneNumberID string `json:"phone_number_id"`
					} `json:"metadata"`
					Messages []struct {
						From, ID, Type, Timestamp string
						Text                      struct {
							Body string `json:"body"`
						} `json:"text"`
					} `json:"messages"`
				} `json:"value"`
			} `json:"changes"`
		} `json:"entry"`
	}
	if json.Unmarshal(b, &in) != nil {
		problem(w, 400, "invalid_body")
		return
	}
	if in.Object == "whatsapp_business_account" {
		for _, entry := range in.Entry {
			for _, change := range entry.Changes {
				if change.Value.Metadata.PhoneNumberID != s.Config.Env["WHATSAPP_PHONE_NUMBER_ID"] {
					continue
				}
				for _, m := range change.Value.Messages {
					if m.ID != "" {
						text := m.Text.Body
						if m.Type != "text" {
							text = ""
						}
						s.inbound(r, "whatsapp", "+"+strings.TrimPrefix(m.From, "+"), text, m.ID, parseUnix(m.Timestamp))
					}
				}
			}
		}
	}
	jsonOut(w, 200, map[string]bool{"ok": true})
}
