package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/asabla/ircat/internal/api"
	"github.com/asabla/ircat/internal/bots"
	"github.com/asabla/ircat/internal/config"
	"github.com/asabla/ircat/internal/dashboard"
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

	eventBus, err := buildEventBus(cfg, logger)
	if err != nil {
		return fmt.Errorf("build event bus: %w", err)
	}
	defer eventBus.Close()

	world := state.NewWorld()
	srv := server.New(cfg, world, logger,
		server.WithStore(store),
		server.WithEventBus(eventBus),
	)

	sup := bots.New(bots.Options{
		Store:       store,
		World:       world,
		IRCActuator: srv,
		Logger:      logger.With("component", "bots"),
		OnBotStart: func(id state.UserID, session *bots.Session) {
			srv.RegisterBot(id, session)
		},
		OnBotStop: func(id state.UserID) {
			srv.UnregisterBot(id)
		},
	})

	apiSrv := api.New(api.Options{
		Store:      store,
		World:      world,
		Actuator:   srv,
		BotManager: sup,
		ServerInfo: srv,
		Logger:     logger.With("component", "api"),
	})

	dash := dashboard.New(dashboard.Options{
		Config:     cfg,
		Logger:     logger.With("component", "dashboard"),
		APIHandler: apiSrv.Handler(),
		PageDeps: &dashboard.PageDeps{
			Store:      store,
			World:      world,
			ServerInfo: srv,
			Actuator:   srv,
		},
		Metrics: srv,
		ReadyFunc: func() error {
			// Ready once the IRC server has bound at least one
			// listener. Before that the IRC side is still wiring
			// up and the dashboard returns 503 to load balancers.
			if len(srv.ListenerAddrs()) == 0 {
				return errors.New("irc listener not bound yet")
			}
			return nil
		},
	})

	// Bring up the bot supervisor before the listener so any bot
	// that ctx:join's a channel in init() runs before the first
	// user connects. The OnBotStart callback wired above registers
	// each session with the server's BotDeliverer map, so both
	// boot-time and API-triggered bot creations reach the broadcast
	// hot path.
	if err := sup.Start(ctx); err != nil {
		logger.Warn("bot supervisor start failed", "error", err)
	}
	defer sup.Stop()

	// Start the federation supervisor. Each configured link with
	// connect=true is dialed on its own goroutine; the returned
	// wait func blocks until every link goroutine has drained on
	// shutdown.
	fedWait := startFederation(runCtxOrBg(ctx), cfg, srv, logger)
	defer fedWait()

	logger.Info("ircat ready",
		"listeners", listenerAddresses(cfg),
		"storage_driver", cfg.Storage.Driver,
		"dashboard_enabled", cfg.Dashboard.Enabled,
		"bots", len(sup.Sessions()),
	)

	// Run the IRC server and the dashboard in parallel; cancel both
	// via the same context. The first one to error wins; the other
	// gets the shared cancel.
	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	errs := make(chan error, 2)
	go func() {
		if err := srv.Run(runCtx); err != nil {
			errs <- fmt.Errorf("irc server: %w", err)
			return
		}
		errs <- nil
	}()
	go func() {
		if err := dash.Run(runCtx); err != nil {
			errs <- fmt.Errorf("dashboard: %w", err)
			return
		}
		errs <- nil
	}()

	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			cancel(err)
			logger.Error("subsystem stopped with error", "error", err)
		}
	}

	logger.Info("ircat shutting down", "reason", context.Cause(ctx))
	logger.Info("ircat stopped")
	return nil
}

// runCtxOrBg is a tiny helper: startFederation wants a context
// that stays alive for the lifetime of the server run, not just
// for the listener bind. We use the parent ctx (signal-bound) so
// SIGTERM drains federation links alongside the IRC listener.
func runCtxOrBg(ctx context.Context) context.Context { return ctx }

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
