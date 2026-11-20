// Command server runs the CloudSim backend API.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cloudsim/backend/internal/api"
	"cloudsim/backend/internal/config"
	"cloudsim/backend/internal/db"
	"cloudsim/backend/internal/repositories"
	"cloudsim/backend/internal/simulator"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(2)
	}
	level := slog.LevelInfo
	_ = level.UnmarshalText([]byte(cfg.LogLevel))
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	for {
		restore, err := run(cfg, log)
		if err != nil {
			log.Error("fatal", "err", err)
			os.Exit(1)
		}
		if restore == "" {
			return
		}
		if err := swapDatabase(cfg.DatabasePath, restore); err != nil {
			log.Error("restore failed; continuing with current database", "err", err)
		} else {
			log.Info("database restored from backup", "file", restore)
		}
	}
}

// run serves until a signal (returns "") or a restore request (returns backup path).
func run(cfg *config.Config, log *slog.Logger) (string, error) {
	database, err := db.Open(cfg.DatabasePath)
	if err != nil {
		return "", fmt.Errorf("open database: %w", err)
	}
	defer database.Close()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	applied, err := database.Migrate(ctx)
	if err != nil {
		return "", fmt.Errorf("migrate: %w", err)
	}
	if len(applied) > 0 {
		log.Info("migrations applied", "versions", applied)
	}
	store := repositories.NewStore(database)
	eng := simulator.New(cfg.EngineURL, cfg.EngineToken)
	srv := api.New(cfg, store, eng, log)
	if err := srv.Init(ctx); err != nil {
		return "", fmt.Errorf("init: %w", err)
	}
	httpSrv := &http.Server{Addr: cfg.Addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
	bgCtx, bgCancel := context.WithCancel(context.Background())
	bgDone := make(chan struct{})
	go func() { srv.Start(bgCtx); close(bgDone) }()
	errCh := make(chan error, 1)
	go func() {
		log.Info("backend listening", "addr", cfg.Addr, "engine", cfg.EngineURL, "db", cfg.DatabasePath)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	restore := ""
	select {
	case <-ctx.Done():
		log.Info("shutdown signal received")
	case err := <-errCh:
		bgCancel()
		return "", err
	case restore = <-api.RestoreRequested:
		log.Info("restore requested", "file", restore)
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutCtx)
	bgCancel()
	select {
	case <-bgDone:
	case <-time.After(10 * time.Second):
		log.Warn("background workers did not stop in time")
	}
	log.Info("backend stopped")
	return restore, nil
}

func swapDatabase(dbPath, backup string) error {
	src, err := os.Open(backup)
	if err != nil {
		return err
	}
	defer src.Close()
	tmp := dbPath + ".restore"
	dst, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")
	return os.Rename(tmp, dbPath)
}

func healthcheck() int {
	addr := os.Getenv("BACKEND_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	if addr[0] == ':' {
		addr = "127.0.0.1" + addr
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + addr + "/healthz")
	if err != nil {
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return 1
	}
	return 0
}
