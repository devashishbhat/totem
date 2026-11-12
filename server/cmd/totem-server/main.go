// Command totem-server is the whole Totem backend: OTLP ingest, the lineage
// worker, the read API and replay, in one binary.
//
// Usage:
//
//	totem-server            run the server
//	totem-server serve      the same
//	totem-server migrate    apply migrations and exit
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/devashishbhat/totem/server/internal/api"
	"github.com/devashishbhat/totem/server/internal/config"
	"github.com/devashishbhat/totem/server/internal/ingest"
	"github.com/devashishbhat/totem/server/internal/lineage"
	"github.com/devashishbhat/totem/server/internal/replay"
	"github.com/devashishbhat/totem/server/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "totem-server:", err)
		os.Exit(1)
	}
}

func run() error {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cmd {
	case "migrate":
		if err := store.Migrate(ctx, cfg.DatabaseURL); err != nil {
			return err
		}
		log.Info("migrations applied")
		return nil
	case "serve":
		return serve(ctx, cfg, log)
	default:
		return fmt.Errorf("unknown command %q (serve, migrate)", cmd)
	}
}

func serve(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	if err := store.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return err
	}
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	runner := replay.New(db, cfg, log.With("component", "replay"))
	// An experiment that was running when the process died would otherwise
	// leave the dashboard waiting forever.
	if stale, err := db.PendingReplays(ctx, 0); err != nil {
		log.Warn("could not check for interrupted replays", "err", err)
	} else {
		for _, id := range stale {
			_ = db.SetReplayStatus(ctx, id, store.ReplayFailed, "the server restarted while this replay was running")
		}
	}

	worker := lineage.New(db, cfg, log.With("component", "lineage"))
	go worker.Run(ctx)

	mux := http.NewServeMux()
	mux.Handle("/v1/traces", &ingest.Handler{
		Store:          db,
		IngestKey:      cfg.IngestKey,
		Salt:           cfg.ProjectSalt,
		Log:            log.With("component", "ingest"),
		RedactPayloads: cfg.RedactPayloads,
	})
	mux.Handle("/api/v1/", api.Router(db, cfg, runner, log.With("component", "api")))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := db.Pool.Ping(r.Context()); err != nil {
			http.Error(w, "database unreachable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"status":"ok"}`)
	})

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	log.Info("listening", "addr", cfg.ListenAddr, "demo_mode", cfg.DemoMode)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	runner.Wait()
	log.Info("stopped")
	return nil
}
