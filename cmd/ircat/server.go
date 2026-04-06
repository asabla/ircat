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

	// TODO(M1+): start IRC listener(s), dashboard, API, federation,
	//            bot supervisor, event sinks. For M0 we just announce
	//            readiness and wait for a signal.
	logger.Info("ircat ready", "listeners", listenerAddresses(cfg))

	<-ctx.Done()
	logger.Info("ircat shutting down", "reason", context.Cause(ctx))

	// TODO(M1+): drain subsystems in reverse dependency order.
	logger.Info("ircat stopped")
	return nil
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
