package server

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/channel"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/claims"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/config"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/eventlog"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/preset"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/session"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/verifier"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/web"
)

type Server struct {
	Config     config.Config
	Store      *session.Store
	Verifier   verifier.Service
	Channels   map[string]channel.Channel
	Log        *eventlog.Logger
	DemoWallet http.Handler
	auth       *authenticator
	limits     limiter
	dedup      limiter
	templates  *template.Template
	proxy      *httputil.ReverseProxy
}

func New(c config.Config, st *session.Store, v verifier.Service, channels map[string]channel.Channel, l *eventlog.Logger) *Server {
	u, _ := url.Parse(c.VerifierURL)
	p := httputil.NewSingleHostReverseProxy(u)
	director := p.Director
	p.Director = func(r *http.Request) {
		director(r)
		r.Header.Del("Authorization")
		r.Header.Del("Cookie")
		r.Header.Del("Cf-Access-Jwt-Assertion")
		r.Header.Del("Cf-Access-Authenticated-User-Email")
		r.Host = u.Host
	}
	p.ErrorLog = log.New(io.Discard, "", 0)
	p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) { problem(w, 502, "verifier_unavailable") }
	p.Transport = &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, ResponseHeaderTimeout: 15 * time.Second, IdleConnTimeout: 60 * time.Second}
	p.ModifyResponse = func(r *http.Response) error { r.Header.Del("Set-Cookie"); secureHeaders(r.Header); return nil }
	return &Server{Config: c, Store: st, Verifier: v, Channels: channels, Log: l, auth: &authenticator{cfg: c, client: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, limits: limiter{now: st.Now}, dedup: limiter{now: st.Now}, templates: template.Must(template.ParseFS(web.Files, "*.html")), proxy: p}
}
func secureHeaders(h http.Header) {
	h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' https://cdnjs.cloudflare.com; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Robots-Tag", "noindex, nofollow")
}
func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func problem(w http.ResponseWriter, status int, code string) {
	jsonOut(w, status, map[string]string{"error": code})
}
func (s *Server) render(w http.ResponseWriter, name string, v any) {
	if values, ok := v.(map[string]any); ok {
		values["Demo"] = s.Config.Demo
		values["HostedSandbox"] = s.Config.HostedSandbox
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.templates.ExecuteTemplate(w, name, v)
}
func (s *Server) sameOrigin(r *http.Request) bool {
	return r.Header.Get("Origin") == s.Config.PublicURL
}
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	if s.Config.Demo && s.DemoWallet != nil {
		m.Handle("/demo/wallet/", s.DemoWallet)
	}
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { jsonOut(w, 200, map[string]string{"status": "ok"}) })
	m.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/console", http.StatusSeeOther) })
	m.HandleFunc("GET /setup", func(w http.ResponseWriter, r *http.Request) {
		s.render(w, "setup.html", map[string]any{})
	})
	m.HandleFunc("GET /assets/{file}", func(w http.ResponseWriter, r *http.Request) {
		f := r.PathValue("file")
		if f != "app.css" && f != "qrcode.min.js" && f != "console.js" && f != "handoff.js" && f != "demo-handoff.js" && f != "demo-wallet.js" && f != "krnali-mark.png" && f != "krnali-icon.png" {
			http.NotFound(w, r)
			return
		}
		b, e := web.Files.ReadFile(f)
		if e != nil {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(f, ".css") {
			w.Header().Set("Content-Type", "text/css")
		} else if strings.HasSuffix(f, ".png") {
			w.Header().Set("Content-Type", "image/png")
		} else {
			w.Header().Set("Content-Type", "text/javascript")
		}
		_, _ = w.Write(b)
	})
	m.HandleFunc("GET /v/{token}", s.handoff)
	m.HandleFunc("POST /v/{token}/start", s.start)
	m.HandleFunc("GET /s/{key}", s.poll)
	m.HandleFunc("GET /done/{id}", s.done)
	for _, method := range []string{"GET", "POST"} {
		m.HandleFunc(method+" /wallet/{rest...}", s.wallet)
	}
	m.HandleFunc("POST /hooks/telegram", s.telegram)
	m.HandleFunc("GET /hooks/whatsapp", s.whatsappChallenge)
	m.HandleFunc("POST /hooks/whatsapp", s.whatsapp)
	operator := func(fn func(http.ResponseWriter, *http.Request, Identity)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			id, e := s.auth.identity(r)
			if e != nil {
				if s.Config.OperatorUser != "" {
					w.Header().Set("WWW-Authenticate", `Basic realm="krnali eudi message verifier plugin", charset="UTF-8"`)
				}
				problem(w, 401, "unauthorized")
				return
			}
			if r.Method == "POST" && !s.sameOrigin(r) {
				problem(w, 403, "invalid_origin")
				return
			}
			fn(w, r, id)
		}
	}
	m.HandleFunc("GET /console", operator(func(w http.ResponseWriter, r *http.Request, id Identity) {
		s.render(w, "console.html", map[string]any{"Business": s.Config.BusinessName, "Minutes": int(math.Ceil(s.Config.LinkTTL.Minutes()))})
	}))
	m.HandleFunc("GET /api/me", operator(func(w http.ResponseWriter, r *http.Request, id Identity) { jsonOut(w, 200, id) }))
	m.HandleFunc("GET /api/channels", operator(s.channelList))
	m.HandleFunc("POST /api/sessions", operator(s.create))
	m.HandleFunc("GET /api/sessions", operator(s.list))
	m.HandleFunc("GET /api/sessions/export.csv", operator(s.export))
	m.HandleFunc("GET /api/sessions/{id}", operator(s.get))
	m.HandleFunc("POST /api/sessions/{id}/cancel", operator(s.cancel))
	m.HandleFunc("POST /api/sessions/{id}/resend", operator(s.resend))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secureHeaders(w.Header())
		if s.Config.HostedSandbox {
			w.Header().Set("X-Verify-Link-Mode", "eudi-sandbox")
			// Wallets talk directly to the official verifier named in its signed request.
			if strings.HasPrefix(r.URL.Path, "/wallet/") {
				http.NotFound(w, r)
				return
			}
		}
		if s.Config.Demo {
			w.Header().Set("X-Verify-Link-Mode", "demo")
			if strings.HasPrefix(r.URL.Path, "/wallet/") || strings.HasPrefix(r.URL.Path, "/hooks/") {
				http.NotFound(w, r)
				return
			}
		}
		if !config.Local(s.Config.PublicURL) {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		if path.Clean(r.URL.Path) != r.URL.Path || strings.ContainsAny(r.URL.Path, "\\\x00") || strings.Contains(r.URL.EscapedPath(), "%") {
			http.NotFound(w, r)
			return
		}
		m.ServeHTTP(w, r)
	})
}
func (s *Server) wallet(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/wallet/") {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	s.proxy.ServeHTTP(w, r)
}

type createRequest struct {
	Channel      string `json:"channel"`
	Recipient    string `json:"recipient"`
	Preset       string `json:"preset"`
	ExpectedName string `json:"expected_name"`
	CaseRef      string `json:"case_ref"`
}

func (s *Server) newSession(ctx context.Context, in createRequest, who, origin string) (session.Session, string, error) {
	// The hosted TEST-01 registration covers PID names, not our PID age claims.
	if s.Config.HostedSandbox && in.Preset == "over-18" {
		return session.Session{}, "", errors.New("preset_not_registered")
	}
	if _, ok := preset.Get(in.Preset); !ok {
		return session.Session{}, "", errors.New("unknown_preset")
	}
	if utf8.RuneCountInString(in.CaseRef) > 64 || len(in.ExpectedName) > 512 || strings.ContainsAny(in.ExpectedName+in.CaseRef, "\r\n\x00") {
		return session.Session{}, "", errors.New("invalid_field")
	}
	if in.Preset == "over-18" && strings.TrimSpace(in.ExpectedName) != "" {
		return session.Session{}, "", errors.New("name_not_requested")
	}
	a, ok := s.Channels[in.Channel]
	if !ok {
		return session.Session{}, "", errors.New("channel_disabled")
	}
	if e := a.Validate(in.Recipient); e != nil {
		return session.Session{}, "", e
	}
	v, token, e := s.Store.Create(session.Session{Channel: in.Channel, Origin: origin, RecipientMasked: a.Mask(in.Recipient), RecipientRef: in.Recipient, Preset: in.Preset, ExpectedName: in.ExpectedName, SentBy: who, CaseRef: in.CaseRef})
	if e != nil {
		return v, "", e
	}
	s.Log.Session("session.created", v)
	link := s.Config.PublicURL + "/v/" + token
	if e = a.Send(ctx, in.Recipient, link, channel.Message{Business: s.Config.BusinessName, Minutes: int(math.Ceil(s.Config.LinkTTL.Minutes()))}); e != nil {
		code := "send_failed"
		if errors.Is(e, channel.ErrWindow) {
			code = "whatsapp_window_closed"
		}
		v, _ = s.Store.Complete(v.ID, session.Failed, nil, code)
		s.Log.Session("session.completed", v)
		return v, "", errors.New(code)
	}
	v = s.Store.MarkSent(v.ID)
	s.Log.Session("session.sent", v)
	if in.Channel != "copylink" {
		link = ""
	}
	return v, link, nil
}
func (s *Server) create(w http.ResponseWriter, r *http.Request, id Identity) {
	if !s.limits.allow("operator:"+id.Email, 10, time.Minute) {
		problem(w, 429, "rate_limited")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	var in createRequest
	if e := d.Decode(&in); e != nil {
		code := "invalid_body"
		if strings.HasPrefix(e.Error(), "json: unknown field") {
			code = "unknown_field"
		}
		problem(w, 400, code)
		return
	}
	if d.Decode(new(any)) != io.EOF {
		problem(w, 400, "invalid_body")
		return
	}
	v, link, e := s.newSession(r.Context(), in, id.Email, "operator")
	s.created(w, v, link, e)
}
func (s *Server) created(w http.ResponseWriter, v session.Session, link string, e error) {
	if e != nil {
		code := e.Error()
		status := 400
		if code == "send_failed" {
			status = 502
		}
		if code == "capacity_reached" {
			status = 503
		}
		problem(w, status, code)
		return
	}
	jsonOut(w, 201, struct {
		session.Session
		Link string `json:"link,omitempty"`
	}{v, link})
}
func visible(v session.Session, id Identity) bool {
	return id.Role == "supervisor" || v.SentBy == id.Email
}
func (s *Server) selected(r *http.Request, id Identity) []session.Session {
	out := []session.Session{}
	for _, v := range s.Store.List() {
		if !visible(v, id) {
			continue
		}
		q := r.URL.Query()
		if q.Get("mine") == "true" && v.SentBy != id.Email {
			continue
		}
		if status := q.Get("status"); status != "" && string(v.Status) != status {
			continue
		}
		if c := q.Get("channel"); c != "" && v.Channel != c {
			continue
		}
		out = append(out, v)
		if len(out) == 100 {
			break
		}
	}
	return out
}
func (s *Server) list(w http.ResponseWriter, r *http.Request, id Identity) {
	jsonOut(w, 200, s.selected(r, id))
}
func (s *Server) own(w http.ResponseWriter, r *http.Request, id Identity) (session.Session, bool) {
	v, ok := s.Store.Get(r.PathValue("id"))
	if !ok || !visible(v, id) {
		problem(w, 404, "not_found")
		return v, false
	}
	return v, true
}
func (s *Server) get(w http.ResponseWriter, r *http.Request, id Identity) {
	if v, ok := s.own(w, r, id); ok {
		jsonOut(w, 200, v)
	}
}
func (s *Server) cancel(w http.ResponseWriter, r *http.Request, id Identity) {
	v, ok := s.own(w, r, id)
	if !ok {
		return
	}
	v, ok = s.Store.Cancel(v.ID)
	if !ok {
		problem(w, 409, "not_pending")
		return
	}
	s.Log.Session("session.completed", v)
	jsonOut(w, 200, v)
}
func (s *Server) resend(w http.ResponseWriter, r *http.Request, id Identity) {
	if id.Role != "supervisor" {
		problem(w, 403, "forbidden")
		return
	}
	v, ok := s.own(w, r, id)
	if !ok {
		return
	}
	if v.Status != session.Expired && v.Status != session.Declined && v.Status != session.Failed {
		problem(w, 409, "not_resendable")
		return
	}
	if !s.limits.allow("operator:"+id.Email, 10, time.Minute) {
		problem(w, 429, "rate_limited")
		return
	}
	next, link, e := s.newSession(r.Context(), createRequest{v.Channel, v.RecipientRef, v.Preset, v.ExpectedName, v.CaseRef}, id.Email, "operator")
	s.created(w, next, link, e)
}
func csvSafe(s string) string {
	if strings.ContainsAny(strings.TrimLeft(s, " \t\r\n")[:min(1, len(strings.TrimLeft(s, " \t\r\n")))], "=+-@") || strings.HasPrefix(s, "\t") || strings.HasPrefix(s, "\r") {
		return "'" + s
	}
	return s
}
func (s *Server) export(w http.ResponseWriter, r *http.Request, id Identity) {
	if id.Role != "supervisor" {
		problem(w, 403, "forbidden")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="verify-link-activity.csv"`)
	c := csv.NewWriter(w)
	header := []string{"id", "status", "channel", "preset", "staff", "case_ref", "created_at", "opened_at", "started_at", "completed_at"}
	if s.Config.Demo {
		header = append([]string{"mode"}, header...)
	}
	_ = c.Write(header)
	for _, v := range s.selected(r, id) {
		row := []string{v.ID, string(v.Status), v.Channel, v.Preset, csvSafe(v.SentBy), csvSafe(v.CaseRef), v.CreatedAt.Format(time.RFC3339), stamp(v.OpenedAt), stamp(v.StartedAt), stamp(v.CompletedAt)}
		if s.Config.Demo {
			row = append([]string{"synthetic-demo"}, row...)
		}
		_ = c.Write(row)
	}
	c.Flush()
}
func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
func (s *Server) channelList(w http.ResponseWriter, r *http.Request, id Identity) {
	type info struct {
		ID     string   `json:"id"`
		Recent []string `json:"recent_chats,omitempty"`
	}
	out := []info{}
	for _, name := range []string{"whatsapp", "sms", "email", "telegram", "copylink"} {
		if a, ok := s.Channels[name]; ok {
			i := info{ID: name}
			if a, ok := a.(*channel.Adapter); ok && name == "telegram" {
				i.Recent = a.Recent()
			}
			out = append(out, i)
		}
	}
	jsonOut(w, 200, out)
}
func (s *Server) handoff(w http.ResponseWriter, r *http.Request) {
	v, e := s.Store.ByToken(r.PathValue("token"), false)
	if e != nil {
		w.WriteHeader(410)
		s.render(w, "done.html", map[string]any{"Business": s.Config.BusinessName, "Title": "This link is no longer available", "Message": "It may have expired, been used or been cancelled. Ask for a new link."})
		return
	}
	p, _ := preset.Get(v.Preset)
	ua := strings.ToLower(r.UserAgent())
	inApp := false
	for _, v := range []string{"telegram", "instagram", "fban", "fbav", "linkedin"} {
		if strings.Contains(ua, v) {
			inApp = true
		}
	}
	s.render(w, "handoff.html", map[string]any{"Business": s.Config.BusinessName, "Session": v, "Purpose": p.Purpose, "StartPath": r.URL.Path + "/start", "Expires": v.ExpiresAt.Format(time.RFC3339), "InApp": inApp})
}
func (s *Server) start(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		problem(w, 403, "invalid_origin")
		return
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if s.Config.TrustProxy {
		if v := net.ParseIP(r.Header.Get("CF-Connecting-IP")); v != nil {
			ip = v.String()
		}
	}
	if !s.limits.allow("start:"+ip, 20, time.Minute) {
		problem(w, 429, "rate_limited")
		return
	}
	v, e := s.Store.ByToken(r.PathValue("token"), true)
	if e != nil {
		problem(w, 410, "link_unavailable")
		return
	}
	p, _ := preset.Get(v.Preset)
	tx, e := s.Verifier.Start(r.Context(), verifier.Init{DCQL: p.DCQL, Nonce: v.Nonce, ResponseMode: s.Config.ResponseMode, JARMode: "by_reference", RequestURIMethod: "get", RedirectTemplate: s.Config.PublicURL + "/done/" + v.ID + "?response_code={RESPONSE_CODE}", IssuerChain: s.Config.IssuerChain, Scheme: s.Config.WalletScheme, Profile: "openid4vp", RegistrationCertificate: s.Config.RegistrationCertificate})
	if e != nil {
		s.complete(v, session.Failed, nil, "verifier_error")
		problem(w, 502, "verifier_unavailable")
		return
	}
	if !s.Store.SetTransaction(v.ID, tx.ID) {
		problem(w, 410, "link_unavailable")
		return
	}
	s.Log.Session("session.started", v)
	ua := strings.ToLower(r.UserAgent())
	jsonOut(w, 200, map[string]any{"authorization_request_uri": tx.AuthorizationURI, "poll_key": v.PollKey, "same_device": strings.Contains(ua, "android") || strings.Contains(ua, "iphone")})
}
func (s *Server) poll(w http.ResponseWriter, r *http.Request) {
	status, ok := s.Store.Poll(r.PathValue("key"))
	if !ok {
		problem(w, 404, "not_found")
		return
	}
	jsonOut(w, 200, map[string]session.Status{"status": status})
}
func (s *Server) complete(v session.Session, status session.Status, result *claims.Result, code string) {
	if next, ok := s.Store.Complete(v.ID, status, result, code); ok {
		s.Log.Session("session.completed", next)
	}
}
func (s *Server) process(ctx context.Context, v session.Session, code string) error {
	if v.TransactionID == "" {
		return verifier.ErrPending
	}
	response, e := s.Verifier.Response(ctx, v.TransactionID, code)
	if e != nil {
		if errors.Is(e, verifier.ErrPending) || errors.Is(e, verifier.ErrCode) {
			return e
		}
		s.complete(v, session.Failed, nil, "verifier_error")
		return e
	}
	if response.Error != "" {
		if response.Error == "access_denied" {
			s.complete(v, session.Declined, nil, "access_denied")
		} else {
			s.complete(v, session.Failed, nil, "wallet_error")
		}
		return nil
	}
	// The query asks for exactly one alternative. Reject ambiguous or extra presentations.
	if len(response.VPToken) != 1 {
		s.complete(v, session.Failed, nil, "invalid_claims")
		return claims.ErrClaims
	}
	var result claims.Result
	for key, values := range response.VPToken {
		if len(values) != 1 {
			e = claims.ErrClaims
			break
		}
		switch key {
		case "pid_sdjwt":
			if v.Preset == "confirm-name-mdoc" {
				e = claims.ErrClaims
				break
			}
			result, e = claims.SDJWT(values[0], v.Preset)
		case "pid_mdoc":
			if v.Preset == "confirm-name-sdjwt" {
				e = claims.ErrClaims
				break
			}
			var docs []claims.Document
			docs, e = s.Verifier.DecodeMDoc(ctx, values[0], s.Config.IssuerChain)
			if e == nil {
				result, e = claims.MDoc(docs, v.Preset)
			}
		default:
			e = claims.ErrClaims
		}
	}
	if e != nil {
		s.complete(v, session.Failed, nil, "invalid_claims")
		return e
	}
	status := session.Verified
	if v.ExpectedName != "" && !claims.Matches(v.ExpectedName, result) {
		status = session.Mismatch
	}
	s.complete(v, status, &result, "")
	return nil
}
func (s *Server) done(w http.ResponseWriter, r *http.Request) {
	message := "You can go back to your chat. The business will see the verification status."
	title := "Return to your conversation"
	if v, ok := s.Store.Get(r.PathValue("id")); ok {
		if code := r.URL.Query().Get("response_code"); code != "" && len(code) <= 1024 {
			if e := s.process(r.Context(), v, code); e == nil {
				title = "Done"
				message = "Done. You can go back to your chat."
			}
		}
	}
	s.render(w, "done.html", map[string]any{"Business": s.Config.BusinessName, "Title": title, "Message": message})
}
func (s *Server) PollOnce(ctx context.Context) {
	for _, v := range s.Store.List() {
		if ctx.Err() != nil {
			return
		}
		if v.Status == session.Verifying && v.TransactionID != "" {
			_ = s.process(ctx, v, "")
		}
	}
}
func (s *Server) Run(ctx context.Context) {
	poll := time.NewTicker(2 * time.Second)
	sweep := time.NewTicker(30 * time.Second)
	defer poll.Stop()
	defer sweep.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
			s.PollOnce(ctx)
		case <-sweep.C:
			s.Store.Sweep()
		}
	}
}
func parseUnix(s string) time.Time { n, _ := strconv.ParseInt(s, 10, 64); return time.Unix(n, 0) }
