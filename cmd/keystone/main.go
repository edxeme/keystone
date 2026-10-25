// Command keystone serves a minimal OpenAI-compatible LLM gateway
// with provider routing, failover, streaming and usage accounting.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/edxeme/keystone/internal/config"
	"github.com/edxeme/keystone/internal/gateway"
)

// Version is the keystone release version.
const Version = "0.1.0"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Printf("keystone %s\n", Version)
		return
	}

	cfgPath := flag.String("config", "keystone.yaml", "path to the gateway configuration")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		logger.Error("load config", "error", err)
		os.Exit(1)
	}
	gw, err := gateway.New(cfg, logger)
	if err != nil {
		logger.Error("build gateway", "error", err)
		os.Exit(1)
	}

	// Streaming responses are long-lived: no WriteTimeout, generous
	// header/read timeouts instead.
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           gw.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown", "error", err)
		}
	}()

	logger.Info("keystone listening", "addr", cfg.Listen, "version", Version)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("listen", "error", err)
		os.Exit(1)
	}
}
