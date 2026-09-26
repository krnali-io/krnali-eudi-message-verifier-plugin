package verifier

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHostedEmptyPendingDoesNotAcceptClaims(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body, code string
		hosted     bool
		want       error
	}{
		{"hosted waiting", 400, "", "", true, ErrPending},
		{"hosted invalid callback", 400, "", "wrong", true, ErrCode},
		{"self hosted stays strict", 400, "", "", false, ErrVerifier},
		{"malformed error", 400, "not-json", "", true, ErrVerifier},
		{"server failure", 500, "", "", true, ErrVerifier},
		{"empty success is not verified", 200, "", "", true, ErrVerifier},
		{"structured pending", 400, `{"error":"PresentationNotSubmitted"}`, "", true, ErrPending},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer s.Close()
			c := New(s.URL)
			c.EmptyPendingResponse = tc.hosted
			out, err := c.Response(context.Background(), "own-transaction", tc.code)
			if !errors.Is(err, tc.want) || len(out.VPToken) != 0 {
				t.Fatalf("expected %v without claims, got %v", tc.want, err)
			}
		})
	}
}
