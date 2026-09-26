package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/channel"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/config"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/eventlog"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/server"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/session"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/verifier"
)

func main() {
	c, e := config.Load()
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	l := eventlog.New(os.Stdout)
	channels := channel.Build(c.Env, time.Now)
	for name := range channels {
		l.Enabled(name)
	}
	v := verifier.New(c.VerifierURL)
	v.EmptyPendingResponse = c.HostedSandbox
	s := server.New(c, session.New(time.Now, c.LinkTTL, c.VerifyWindow, c.ClaimsTTL), v, channels, l)
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
	if e = h.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, "server failed to listen")
		os.Exit(1)
	}
}
