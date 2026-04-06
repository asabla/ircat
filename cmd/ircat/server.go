package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/asabla/ircat/internal/config"
	"github.com/asabla/ircat/internal/logging"
	"github.com/asabla/ircat/internal/server"
	"github.com/asabla/ircat/internal/state"
	"github.com/asabla/ircat/internal/storage"
	"github.com/asabla/ircat/internal/storage/postgres"
	"github.com/asabla/ircat/internal/storage/sqlite"
)

// runServer is the default subcommand: load config, initialize
// logging, announce readiness, and wait for a signal. Subsystems
// (listener, dashboard, federation, bots) get wired in here as later
// milestones land them.
func runServer(args []string) error {
	fs := flag.NewFlagSet("ircat server", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var (
		configPath  string
		showVersion bool
	)
	fs.StringVar(&configPath, "config", defaultConfigPath(), "path to config file (.json, .yaml, .yml), or '-' for stdin")
	fs.BoolVar(&showVersion, "version", false, "print version and exit")

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "usage: ircat server [flags]\n\nflags:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if showVersion {
		fmt.Printf("ircat %s (commit %s, built %s)\n", version, commit, date)
		return nil
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config %q: %w", configPath, err)
	}

	logger, _, err := logging.New(logging.Options{
		Level:             cfg.Logging.Level,
		Format:            cfg.Logging.Format,
		RingBufferEntries: cfg.Logging.RingBufferEntries,
	})
	if err != nil {
		return fmt.Errorf("initialize logging: %w", err)
	}

	logger.Info("ircat starting",
		"version", version,
		"commit", commit,
		"server_name", cfg.Server.Name,
		"network", cfg.Server.Network,
		"storage_driver", cfg.Storage.Driver,
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := openStore(ctx, cfg)
	if err != nil {
		return fmt.Errorf("open storage: %w", err)
	}
	defer store.Close()

	if err := bootstrapStore(ctx, store, cfg, logger); err != nil {
		return fmt.Errorf("bootstrap storage: %w", err)
	}

	world := state.NewWorld()
	srv := server.New(cfg, world, logger, server.WithStore(store))

	logger.Info("ircat ready",
		"listeners", listenerAddresses(cfg),
		"storage_driver", cfg.Storage.Driver,
	)

	// Server.Run blocks until ctx is done; it owns the listeners and
	// the per-connection drain. Future milestones add the dashboard,
	// API, federation links, bot supervisor, and event sinks here in
	// parallel and orchestrate their shutdown alongside the IRC
	// listener.
	if err := srv.Run(ctx); err != nil {
		logger.Error("server stopped with error", "error", err)
		return err
	}

	logger.Info("ircat shutting down", "reason", context.Cause(ctx))
	logger.Info("ircat stopped")
	return nil
}

// openStore wires up the persistent storage backend selected in
// config. Both sqlite (default, file-backed, pure-Go) and postgres
// (pgx via the database/sql adapter) are supported.
func openStore(ctx context.Context, cfg *config.Config) (storage.Store, error) {
	switch cfg.Storage.Driver {
	case "sqlite":
		s, err := sqlite.Open(cfg.Storage.SQLite.Path)
		if err != nil {
			return nil, err
		}
		if err := s.Migrate(ctx); err != nil {
			_ = s.Close()
			return nil, fmt.Errorf("migrate: %w", err)
		}
		return s, nil
	case "postgres":
		dsn := cfg.Storage.Postgres.DSN
		if dsn == "" {
			return nil, errors.New("storage.postgres.dsn is empty")
		}
		s, err := postgres.Open(dsn)
		if err != nil {
			return nil, err
		}
		if err := s.Migrate(ctx); err != nil {
			_ = s.Close()
			return nil, fmt.Errorf("migrate: %w", err)
		}
		return s, nil
	}
	return nil, fmt.Errorf("unknown storage driver %q", cfg.Storage.Driver)
}

func defaultConfigPath() string {
	if p := os.Getenv("IRCAT_CONFIG"); p != "" {
		return p
	}
	return "/etc/ircat/config.yaml"
}

func listenerAddresses(cfg *config.Config) []string {
	out := make([]string, 0, len(cfg.Server.Listeners))
	for _, l := range cfg.Server.Listeners {
		out = append(out, l.Address)
	}
	return out
}
