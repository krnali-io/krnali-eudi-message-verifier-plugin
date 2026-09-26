package demo

import (
	"context"
	"errors"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/claims"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/preset"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/verifier"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func begin(t *testing.T, v *Verifier, p string) verifier.Transaction {
	t.Helper()
	query, _ := preset.Get(p)
	tx, err := v.Start(context.Background(), verifier.Init{DCQL: query.DCQL, RedirectTemplate: v.base + "/done/example?response_code={RESPONSE_CODE}"})
	if err != nil {
		t.Fatal(err)
	}
	return tx
}
func choose(v *Verifier, id, outcome, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/demo/wallet/"+id, strings.NewReader("outcome="+outcome))
	r.Header.Set("Origin", origin)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	v.ServeHTTP(w, r)
	return w
}
func TestSimulatedOutcomes(t *testing.T) {
	for _, tc := range []struct {
		preset, outcome, name string
		age                   bool
	}{
		{"confirm-name", "share", "Alex", false}, {"confirm-name", "different", "Sam", false}, {"confirm-name-mdoc", "share", "Alex", false}, {"over-18", "share", "", true}, {"over-18", "under18", "", false}, {"confirm-name", "decline", "", false},
	} {
		t.Run(tc.preset+"-"+tc.outcome, func(t *testing.T) {
			v := New("https://verify.example.test")
			tx := begin(t, v, tc.preset)
			if _, err := v.Response(context.Background(), tx.ID, ""); !errors.Is(err, verifier.ErrPending) {
				t.Fatal("not pending", err)
			}
			page := httptest.NewRecorder()
			v.ServeHTTP(page, httptest.NewRequest("GET", tx.AuthorizationURI, nil))
			if page.Code != 200 || !strings.Contains(page.Body.String(), "Synthetic identities only") {
				t.Fatal("missing demo notice")
			}
			if choose(v, tx.ID, tc.outcome, "https://other.example.test").Code != 403 {
				t.Fatal("cross-origin choice accepted")
			}
			if choose(v, tx.ID, "arbitrary", v.base).Code != 400 {
				t.Fatal("invalid outcome accepted")
			}
			if got := choose(v, tx.ID, tc.outcome, v.base); got.Code != 303 || got.Header().Get("Location") != v.base+"/done/example?response_code=demo" {
				t.Fatal("choice", got.Code)
			}
			if choose(v, tx.ID, tc.outcome, v.base).Code != 410 {
				t.Fatal("choice replay")
			}
			response, err := v.Response(context.Background(), tx.ID, "demo")
			if err != nil {
				t.Fatal(err)
			}
			if tc.outcome == "decline" {
				if response.Error != "access_denied" {
					t.Fatal(response)
				}
				return
			}
			var result claims.Result
			if tc.preset == "confirm-name-mdoc" {
				docs, e := v.DecodeMDoc(context.Background(), response.VPToken["pid_mdoc"][0], "")
				if e != nil {
					t.Fatal(e)
				}
				result, err = claims.MDoc(docs, tc.preset)
			} else {
				result, err = claims.SDJWT(response.VPToken["pid_sdjwt"][0], tc.preset)
			}
			if err != nil || result.GivenName != tc.name {
				t.Fatal(result, err)
			}
			if tc.preset == "over-18" && (result.AgeOver18 == nil || *result.AgeOver18 != tc.age) {
				t.Fatal(result)
			}
		})
	}
}
func TestOnlyOneChoiceAndExpiry(t *testing.T) {
	v := New("https://verify.example.test")
	tx := begin(t, v, "confirm-name")
	var successes atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if choose(v, tx.ID, "share", v.base).Code == 303 {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatal(successes.Load())
	}
	v.now = func() time.Time { return time.Now().Add(6 * time.Minute) }
	if _, err := v.Response(context.Background(), tx.ID, ""); !errors.Is(err, verifier.ErrVerifier) {
		t.Fatal("expired state retained")
	}
	if len(v.items) != 0 {
		t.Fatal("expired state not removed")
	}
}
