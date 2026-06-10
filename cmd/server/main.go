package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	flag "github.com/spf13/pflag"

	"github.com/trondhindenes/caramba/internal/config"
	"github.com/trondhindenes/caramba/internal/repo"
	"github.com/trondhindenes/caramba/internal/store"
	"github.com/trondhindenes/caramba/internal/store/localdir"
	"github.com/trondhindenes/caramba/internal/web"
)

func main() {
	configPath := flag.StringP("config", "c", "config.yml", "path to the config file")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(*configPath, logger); err != nil {
		logger.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(configPath string, logger *slog.Logger) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	var st store.Store
	switch cfg.Store.Type {
	case "local":
		st, err = localdir.New(cfg.Store.Path)
		if err != nil {
			return err
		}
	case "gcs":
		return errors.New("gcs store is not implemented yet")
	}

	alerts := repo.NewAlertRepo(st)
	srv := &http.Server{
		Addr:    cfg.Listen,
		Handler: web.NewHandler(cfg, alerts, logger),
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	logger.Info("caramba listening", "addr", cfg.Listen, "store", cfg.Store.Type)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
