// Package demo implements a synthetic wallet simulator for the demo executable.
// The live verifylink executable does not import this package.
package demo

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/claims"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/preset"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/verifier"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/web"
)

type transaction struct {
	expires           time.Time
	redirect, outcome string
	age, mdoc         bool
}
type Verifier struct {
	mu    sync.Mutex
	items map[string]transaction
	base  string
	now   func() time.Time
	page  *template.Template
}

func New(base string) *Verifier {
	return &Verifier{items: map[string]transaction{}, base: base, now: time.Now, page: template.Must(template.ParseFS(web.Files, "*.html"))}
}
func (v *Verifier) sweep() {
	for id, tx := range v.items {
		if !v.now().Before(tx.expires) {
			delete(v.items, id)
		}
	}
}
func (v *Verifier) Start(_ context.Context, in verifier.Init) (verifier.Transaction, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.sweep()
	if len(v.items) >= 1000 {
		return verifier.Transaction{}, verifier.ErrVerifier
	}
	u, err := url.Parse(in.RedirectTemplate)
	if err != nil || !strings.HasPrefix(in.RedirectTemplate, v.base+"/done/") || u.User != nil {
		return verifier.Transaction{}, verifier.ErrVerifier
	}
	var key [32]byte
	if _, err = rand.Read(key[:]); err != nil {
		return verifier.Transaction{}, err
	}
	id := base64.RawURLEncoding.EncodeToString(key[:])
	query := string(in.DCQL)
	v.items[id] = transaction{expires: v.now().Add(5 * time.Minute), redirect: strings.ReplaceAll(in.RedirectTemplate, "{RESPONSE_CODE}", "demo"), age: strings.Contains(query, "age_equal_or_over") || strings.Contains(query, "age_over_18"), mdoc: !strings.Contains(query, "pid_sdjwt")}
	return verifier.Transaction{ID: id, AuthorizationURI: v.base + "/demo/wallet/" + id}, nil
}
func (v *Verifier) Response(_ context.Context, id, code string) (verifier.Response, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.sweep()
	tx, ok := v.items[id]
	if !ok {
		return verifier.Response{}, verifier.ErrVerifier
	}
	if code != "" && code != "demo" {
		return verifier.Response{}, verifier.ErrCode
	}
	if tx.outcome == "" {
		return verifier.Response{}, verifier.ErrPending
	}
	if tx.outcome == "decline" {
		return verifier.Response{Error: "access_denied"}, nil
	}
	if tx.mdoc {
		return verifier.Response{VPToken: map[string][]string{"pid_mdoc": {id}}}, nil
	}
	name := "Alex"
	family := "Demo"
	if tx.outcome == "different" {
		name = "Sam"
		family = "Sample"
	}
	payload := map[string]any{"vct": "urn:eudi:pid:1"}
	if tx.age {
		payload["age_equal_or_over"] = map[string]bool{"18": tx.outcome != "under18"}
	} else {
		payload["given_name"] = name
		payload["family_name"] = family
	}
	b, _ := json.Marshal(payload)
	// Deliberately not a signed credential; it never leaves the demo process.
	token := "synthetic-demo." + base64.RawURLEncoding.EncodeToString(b) + ".not-a-signature~"
	return verifier.Response{VPToken: map[string][]string{"pid_sdjwt": {token}}}, nil
}
func (v *Verifier) DecodeMDoc(_ context.Context, id, _ string) ([]claims.Document, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.sweep()
	tx, ok := v.items[id]
	if !ok || !tx.mdoc || tx.outcome == "" || tx.outcome == "decline" {
		return nil, verifier.ErrVerifier
	}
	name, family := "Alex", "Demo"
	if tx.outcome == "different" {
		name = "Sam"
		family = "Sample"
	}
	attributes := map[string]any{"given_name": name, "family_name": family, "age_over_18": tx.outcome != "under18"}
	return []claims.Document{{DocType: preset.Namespace, Attributes: map[string]map[string]any{preset.Namespace: attributes}}}, nil
}
func (v *Verifier) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/demo/wallet/")
	outcome := ""
	if r.Method == http.MethodPost {
		if r.Header.Get("Origin") != v.base {
			http.Error(w, "Invalid origin", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		if r.ParseForm() != nil {
			http.Error(w, "Invalid choice", http.StatusBadRequest)
			return
		}
		outcome = r.PostForm.Get("outcome")
	} else if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	v.mu.Lock()
	v.sweep()
	tx, ok := v.items[id]
	if !ok || tx.outcome != "" {
		v.mu.Unlock()
		http.Error(w, "This demo check is no longer available.", http.StatusGone)
		return
	}
	if r.Method == http.MethodGet {
		v.mu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = v.page.ExecuteTemplate(w, "demo-wallet.html", map[string]any{"Demo": true, "Age": tx.age, "Action": r.URL.Path})
		return
	}
	if outcome != "share" && outcome != "decline" && !(tx.age && outcome == "under18") && !(!tx.age && outcome == "different") {
		v.mu.Unlock()
		http.Error(w, "Invalid choice", http.StatusBadRequest)
		return
	}
	tx.outcome = outcome
	v.items[id] = tx
	v.mu.Unlock()
	if r.Header.Get("Accept") == "application/json" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"redirect": tx.redirect})
		return
	}
	http.Redirect(w, r, tx.redirect, http.StatusSeeOther)
}
