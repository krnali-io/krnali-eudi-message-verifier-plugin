// verifylink-demo is an explicitly synthetic, separately deployed executable.
package main

import (
	"context"
	"fmt"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/channel"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/config"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/demo"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/eventlog"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/server"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/session"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	c, err := config.ParseDemo(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	v := demo.New(c.PublicURL)
	s := server.New(c, session.New(time.Now, c.LinkTTL, c.VerifyWindow, c.ClaimsTTL), v, channel.Build(map[string]string{}, time.Now), eventlog.New(io.Discard))
	s.DemoWallet = v
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	go s.Run(ctx)
	h := &http.Server{Addr: c.ListenAddr, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32768, ErrorLog: log.New(io.Discard, "", 0)}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = h.Shutdown(shutdown)
	}()
	if err = h.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, "demo failed to listen")
		os.Exit(1)
	}
}
