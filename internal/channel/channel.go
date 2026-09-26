package channel

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var ErrRecipient = errors.New("invalid_recipient")
var ErrSend = errors.New("send_failed")
var ErrWindow = errors.New("whatsapp_window_closed")
var phone = regexp.MustCompile(`^\+[1-9]\d{7,14}$`)
var chat = regexp.MustCompile(`^-?[1-9]\d{0,19}$`)

type Message struct {
	Business string
	Minutes  int
	Text     string
}

func (m Message) Body(link string) string {
	if m.Text != "" {
		return m.Text
	}
	return fmt.Sprintf("%s asked to confirm your identity. Open this link to verify with your EU wallet: %s\nThe link works once and expires in %d minutes. If you did not expect this, ignore it.", m.Business, link, m.Minutes)
}

type Channel interface {
	Name() string
	Validate(string) error
	Mask(string) string
	Send(context.Context, string, string, Message) error
}
type Adapter struct {
	Kind string
	Env  map[string]string
	HTTP *http.Client
	Now  func() time.Time
	mu   sync.Mutex
	seen map[string]time.Time
}

func Build(env map[string]string, now func() time.Time) map[string]Channel {
	m := map[string]Channel{}
	required := map[string][]string{"copylink": {}, "telegram": {"TELEGRAM_BOT_TOKEN", "TELEGRAM_WEBHOOK_SECRET"}, "whatsapp": {"WHATSAPP_ACCESS_TOKEN", "WHATSAPP_PHONE_NUMBER_ID", "WHATSAPP_APP_SECRET", "WHATSAPP_VERIFY_TOKEN", "WHATSAPP_API_VERSION"}, "sms": {"SMS_ACCOUNT_SID", "SMS_AUTH_TOKEN", "SMS_FROM"}, "email": {"SMTP_HOST", "SMTP_PORT", "SMTP_USER", "SMTP_PASSWORD", "EMAIL_FROM"}}
	for name, keys := range required {
		ok := true
		for _, k := range keys {
			if env[k] == "" {
				ok = false
			}
		}
		if ok {
			m[name] = &Adapter{Kind: name, Env: env, Now: now, seen: map[string]time.Time{}, HTTP: &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
		}
	}
	return m
}
func (a *Adapter) Name() string { return a.Kind }
func (a *Adapter) Validate(s string) error {
	if len(s) > 320 || strings.ContainsAny(s, "\r\n\x00") {
		return ErrRecipient
	}
	switch a.Kind {
	case "sms", "whatsapp":
		if !phone.MatchString(s) {
			return ErrRecipient
		}
	case "telegram":
		if !chat.MatchString(s) {
			return ErrRecipient
		}
	case "email":
		m, e := mail.ParseAddress(s)
		if e != nil || m.Address != s {
			return ErrRecipient
		}
	case "copylink":
		if strings.TrimSpace(s) == "" || utf8.RuneCountInString(s) > 100 {
			return ErrRecipient
		}
	default:
		return ErrRecipient
	}
	return nil
}
func (a *Adapter) Mask(s string) string {
	switch a.Kind {
	case "email":
		local, domain, _ := strings.Cut(s, "@")
		r, _ := utf8.DecodeRuneInString(local)
		return string(r) + "•••@" + domain
	case "sms", "whatsapp":
		if len(s) > 6 {
			return s[:3] + " •• •• " + s[len(s)-4:len(s)-2] + " " + s[len(s)-2:]
		}
	case "telegram":
		if len(s) > 4 {
			return "Telegram user •••" + s[len(s)-4:]
		}
		return "Telegram user •••"
	}
	return s
}
func (a *Adapter) Seen(sender string, t time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if t.After(a.Now()) || t.Before(a.Now().Add(-24*time.Hour)) {
		return
	}
	for k, v := range a.seen {
		if !a.Now().Before(v.Add(24 * time.Hour)) {
			delete(a.seen, k)
		}
	}
	if len(a.seen) < 10000 {
		if t.After(a.seen[sender]) {
			a.seen[sender] = t
		}
	}
}
func (a *Adapter) Recent() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := []string{}
	for k, t := range a.seen {
		if a.Now().Before(t.Add(24 * time.Hour)) {
			out = append(out, k)
		} else {
			delete(a.seen, k)
		}
	}
	sort.Strings(out)
	return out
}
func (a *Adapter) within(sender string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	t, ok := a.seen[sender]
	return ok && a.Now().Before(t.Add(24*time.Hour))
}
func (a *Adapter) Send(ctx context.Context, to, link string, msg Message) error {
	if e := a.Validate(to); e != nil {
		return e
	}
	if a.Kind == "copylink" {
		return nil
	}
	if a.Kind == "email" {
		return a.email(ctx, to, msg.Body(link))
	}
	var endpoint, typ string
	var body io.Reader
	switch a.Kind {
	case "telegram":
		endpoint = "https://api.telegram.org/bot" + a.Env["TELEGRAM_BOT_TOKEN"] + "/sendMessage"
		b, _ := json.Marshal(map[string]any{"chat_id": to, "text": msg.Body(link), "link_preview_options": map[string]bool{"is_disabled": true}})
		body = bytes.NewReader(b)
		typ = "application/json"
	case "whatsapp":
		endpoint = "https://graph.facebook.com/" + a.Env["WHATSAPP_API_VERSION"] + "/" + a.Env["WHATSAPP_PHONE_NUMBER_ID"] + "/messages"
		payload := map[string]any{"messaging_product": "whatsapp", "to": strings.TrimPrefix(to, "+"), "type": "text", "text": map[string]any{"body": msg.Body(link), "preview_url": false}}
		if !a.within(to) {
			if a.Env["WHATSAPP_TEMPLATE_NAME"] == "" || link == "" {
				return ErrWindow
			}
			lang := a.Env["WHATSAPP_TEMPLATE_LANG"]
			if lang == "" {
				lang = "en"
			}
			delete(payload, "text")
			payload["type"] = "template"
			payload["template"] = map[string]any{"name": a.Env["WHATSAPP_TEMPLATE_NAME"], "language": map[string]string{"code": lang}, "components": []any{map[string]any{"type": "body", "parameters": []any{map[string]string{"type": "text", "text": link}}}}}
		}
		b, _ := json.Marshal(payload)
		body = bytes.NewReader(b)
		typ = "application/json"
	case "sms":
		endpoint = "https://api.twilio.com/2010-04-01/Accounts/" + a.Env["SMS_ACCOUNT_SID"] + "/Messages.json"
		body = strings.NewReader(url.Values{"To": {to}, "From": {a.Env["SMS_FROM"]}, "Body": {msg.Body(link)}}.Encode())
		typ = "application/x-www-form-urlencoded"
	default:
		return ErrSend
	}
	r, e := http.NewRequestWithContext(ctx, "POST", endpoint, body)
	if e != nil {
		return ErrSend
	}
	r.Header.Set("Content-Type", typ)
	if a.Kind == "sms" {
		r.SetBasicAuth(a.Env["SMS_ACCOUNT_SID"], a.Env["SMS_AUTH_TOKEN"])
	}
	if a.Kind == "whatsapp" {
		r.Header.Set("Authorization", "Bearer "+a.Env["WHATSAPP_ACCESS_TOKEN"])
	}
	res, e := a.HTTP.Do(r)
	if e != nil {
		return ErrSend
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return ErrSend
	}
	if a.Kind == "telegram" {
		var p struct {
			OK bool `json:"ok"`
		}
		if json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(&p) != nil || !p.OK {
			return ErrSend
		}
	}
	return nil
}
func (a *Adapter) email(ctx context.Context, to, body string) error {
	from, e := mail.ParseAddress(a.Env["EMAIL_FROM"])
	if e != nil || strings.ContainsAny(a.Env["EMAIL_FROM"], "\r\n") {
		return ErrSend
	}
	host := a.Env["SMTP_HOST"]
	conn, e := (&net.Dialer{Timeout: 12 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(host, a.Env["SMTP_PORT"]))
	if e != nil {
		return ErrSend
	}
	defer conn.Close()
	deadline := time.Now().Add(15 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	c, e := smtp.NewClient(conn, host)
	if e != nil {
		return ErrSend
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); !ok {
		return ErrSend
	}
	if c.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}) != nil {
		return ErrSend
	}
	if c.Auth(smtp.PlainAuth("", a.Env["SMTP_USER"], a.Env["SMTP_PASSWORD"], host)) != nil {
		return ErrSend
	}
	if c.Mail(from.Address) != nil || c.Rcpt(to) != nil {
		return ErrSend
	}
	w, e := c.Data()
	if e != nil {
		return ErrSend
	}
	_, e = fmt.Fprintf(w, "From: %s\r\nTo: %s\r\nSubject: Verify with your EU wallet\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n", from.String(), to, strings.ReplaceAll(body, "\n", "\r\n"))
	if e != nil {
		return ErrSend
	}
	if w.Close() != nil {
		return ErrSend
	}
	if c.Quit() != nil {
		return ErrSend
	}
	return nil
}
