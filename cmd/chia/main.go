// Command chia runs the API server and its database migrations.
//
//	chia serve          start the HTTP API (applies pending migrations first)
//	chia migrate up     apply pending migrations and exit
//	chia migrate down   roll back the latest migration and exit
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/timofeevilia/chia/internal/fx"
	"github.com/timofeevilia/chia/internal/httpapi"
	"github.com/timofeevilia/chia/internal/store"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	st, err := store.Open(ctx, env("DATABASE_URL", "postgres://chia:chia@localhost:5432/chia?sslmode=disable"))
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Close()

	switch cmd {
	case "migrate":
		if len(os.Args) > 2 && os.Args[2] == "down" {
			return st.MigrateDown(ctx)
		}
		return st.Migrate(ctx)
	case "serve":
	default:
		return fmt.Errorf("unknown command %q (use serve or migrate)", cmd)
	}

	if err := st.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	opts, err := redis.ParseURL(env("REDIS_URL", "redis://localhost:6379/0"))
	if err != nil {
		return fmt.Errorf("parse REDIS_URL: %w", err)
	}
	rdb := redis.NewClient(opts)
	defer rdb.Close()

	var source fx.Source = fx.NewHTTPSource(env("FX_URL", "https://open.er-api.com/v6/latest/USD"))
	if env("FX_SOURCE", "http") == "static" {
		source = fx.StaticSource{T: fx.Table{Rates: fx.DemoRates}}
	}
	fxs := fx.NewService(rdb, source, time.Hour)

	limit, _ := strconv.Atoi(env("RATE_LIMIT_PER_MINUTE", "120"))
	handler, err := httpapi.NewHandler(st, rdb, fxs, log, httpapi.Config{
		RateLimitPerMinute: limit,
		CORSOrigins:        strings.Split(env("CORS_ORIGINS", "*"), ","),
		TrustProxy:         env("TRUST_PROXY", "false") == "true",
	})
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              ":" + env("PORT", "8080"),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       90 * time.Second,
		// No WriteTimeout: the events endpoint is a long-lived stream.
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", srv.Addr)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}
