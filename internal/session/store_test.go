package session

import (
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/claims"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAtomicConsumptionAndTerminalState(t *testing.T) {
	st := New(time.Now, time.Minute, time.Minute, time.Minute)
	v, token, _ := st.Create(Session{})
	st.MarkSent(v.ID)
	for i := 0; i < 2; i++ {
		if _, e := st.ByToken(token, false); e != nil {
			t.Fatal(e)
		}
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Go(func() {
			if _, e := st.ByToken(token, true); e == nil {
				wins.Add(1)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal(wins.Load())
	}
	st.Cancel(v.ID)
	if _, ok := st.Complete(v.ID, Verified, &claims.Result{GivenName: "Anna"}, ""); ok {
		t.Fatal("terminal state changed")
	}
}
func TestExpiryWipeAndRetention(t *testing.T) {
	now := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	st := New(func() time.Time { return now }, time.Minute, 2*time.Minute, 3*time.Minute)
	v, token, _ := st.Create(Session{})
	st.MarkSent(v.ID)
	now = now.Add(time.Minute)
	if _, e := st.ByToken(token, true); e == nil {
		t.Fatal("expired token worked")
	}
	v, token, _ = st.Create(Session{ExpectedName: "Anna Larsen"})
	st.MarkSent(v.ID)
	v, _ = st.ByToken(token, true)
	result := claims.Result{GivenName: "Anna", FamilyName: "Larsen"}
	st.Complete(v.ID, Verified, &result, "")
	now = now.Add(3 * time.Minute)
	v, _ = st.Get(v.ID)
	if v.Result != nil || !v.ResultWiped || v.ExpectedName != "" {
		t.Fatal("not wiped")
	}
	now = now.Add(24 * time.Hour)
	st.Sweep()
	if len(st.List()) != 0 {
		t.Fatal("not removed")
	}
}
