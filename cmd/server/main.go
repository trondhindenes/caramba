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
	"github.com/trondhindenes/caramba/internal/dispatch"
	"github.com/trondhindenes/caramba/internal/mcpserver"
	"github.com/trondhindenes/caramba/internal/notify"
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
	templates := repo.NewTemplateRepo(st)
	rules := repo.NewRuleRepo(st)
	dispatches := repo.NewDispatchRepo(st)
	destinations, err := notify.NewRegistry(cfg.Destinations, cfg.Colors)
	if err != nil {
		return err
	}
	dispatcher := dispatch.New(rules, templates, dispatches, destinations, logger)
	mux := http.NewServeMux()
	mux.Handle("/", web.NewHandler(web.Deps{
		WebhookToken: cfg.WebhookToken,
		Alerts:       alerts,
		Templates:    templates,
		Rules:        rules,
		Dispatches:   dispatches,
		Dispatcher:   dispatcher,
		Destinations: destinations.Names(),
		Logger:       logger,
	}))
	if cfg.MCPToken != "" {
		mux.Handle("/mcp", mcpserver.NewHandler(alerts, templates, rules, cfg.MCPToken, logger))
	}
	srv := &http.Server{
		Addr:    cfg.Listen,
		Handler: mux,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	logger.Info("caramba listening", "addr", cfg.Listen, "store", cfg.Store.Type,
		"mcp", cfg.MCPToken != "", "destinations", len(cfg.Destinations))
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	// Once handlers have drained no new routing can start; let in-flight
	// routing finish so received alerts are not left unsent.
	<-drained
	dispatcher.Wait()
	return nil
}
