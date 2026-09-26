package server

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"
)

// An opt-in, loopback-only browser fixture. It is never part of the service binary.
func TestBrowserPreview(t *testing.T) {
	if os.Getenv("VERIFYLINK_BROWSER_FIXTURE") != "1" {
		t.Skip("browser fixture disabled")
	}
	f := setup(t)
	f.s.Config.PublicURL = "http://127.0.0.1:18092"
	f.s.auth.cfg.PublicURL = f.s.Config.PublicURL
	f.s.Config.Operators["agent@example.test"] = "supervisor"
	f.s.Store.Now = time.Now
	f.answer("sdjwt")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.s.Run(ctx)
	h := &http.Server{Addr: "127.0.0.1:18092", Handler: f.s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	t.Cleanup(func() { _ = h.Close() })
	t.Log("Browser fixture ready: synthetic credentials only")
	if e := h.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		t.Fatal(e)
	}
}
