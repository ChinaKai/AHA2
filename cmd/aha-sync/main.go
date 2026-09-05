package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ChinaKai/AHA2/internal/centersync"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	listen := flag.String("listen", env("AHA2_SYNC_LISTEN", "127.0.0.1:8770"), "listen address")
	database := flag.String("db", env("AHA2_SYNC_DB", ".data/sync.db"), "SQLite database")
	registrationTTL := flag.Duration("registration-ttl", 24*time.Hour, "bootstrap registration code lifetime")
	generateRegistrationCode := flag.String("generate-registration-code", "", "generate a registration code into this restricted file and exit")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	store, err := centersync.Open(ctx, *database)
	if err != nil {
		return err
	}
	defer store.Close()
	if *generateRegistrationCode != "" {
		code, err := store.CreateRegistrationCode(ctx, *registrationTTL)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(*generateRegistrationCode), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(*generateRegistrationCode, []byte(code+"\n"), 0o600); err != nil {
			return err
		}
		return os.Chmod(*generateRegistrationCode, 0o600)
	}
	if _, _, err = store.EnsureRegistrationCode(ctx, *registrationTTL); err != nil {
		return err
	}
	server := &http.Server{Addr: *listen, Handler: store.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20}
	errs := make(chan error, 1)
	go func() { errs <- server.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	case err := <-errs:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}
func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
