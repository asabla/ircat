package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/asabla/ircat/internal/config"
	"github.com/asabla/ircat/internal/logging"
	"github.com/asabla/ircat/internal/server"
	"github.com/asabla/ircat/internal/storage"
)

// reloadDeps captures everything reloadConfig needs to apply a
// fresh config snapshot. It is built once during startup and
// shared with the SIGHUP goroutine plus the
// /api/v1/config/reload handler. The struct exists so the
// reload entry point has a single, well-typed signature instead
// of a long parameter list.
type reloadDeps struct {
	configPath string
	store      storage.Store
	srv        *server.Server
	levelVar   *slog.LevelVar
	logger     *slog.Logger

	// mu serializes concurrent reload requests so the SIGHUP
	// goroutine and the API handler do not race each other.
	// Reload itself is fast (a config re-read plus a few store
	// upserts) so the lock is held for under a millisecond in
	// practice.
	mu sync.Mutex
}

// reloadConfig re-reads the config file from disk and applies
// the hot-reloadable sections to the running process. The
// reloadable surface in v1.1 is:
//
//   - logging.level — flipped via the slog.LevelVar so the next
//     emitted record honours the new threshold.
//   - operators (statically configured ones) — re-synced into
//     the operator store via syncStaticOperators.
//   - server.motd_file — re-read by the Server via ReloadMOTD.
//
// Everything else (listeners, federation links, dashboard
// address, storage driver) still requires a restart and is
// documented as such in docs/CONFIG.md.
//
// reloadConfig is safe to call concurrently — the reloadDeps
// mutex serializes overlapping requests.
func (r *reloadDeps) reloadConfig(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	cfg, err := config.Load(r.configPath)
	if err != nil {
		return fmt.Errorf("load config %q: %w", r.configPath, err)
	}

	// 1. Logging level. Validate before applying so a typo in
	// the new config does not silently no-op.
	newLevel, err := logging.ParseLevel(cfg.Logging.Level)
	if err != nil {
		return fmt.Errorf("logging.level: %w", err)
	}
	if r.levelVar != nil {
		r.levelVar.Set(newLevel)
	}

	// 2. Static operators. Re-syncs the operator table from
	// the freshly-loaded config.
	if r.store != nil {
		if err := syncStaticOperators(ctx, r.store, cfg, r.logger); err != nil {
			return fmt.Errorf("sync static operators: %w", err)
		}
	}

	// 3. MOTD. The server reads from cfg.Server.MOTDFile via
	// the cfg pointer it was constructed with, so we update
	// the path on its config copy and ask it to re-read.
	if r.srv != nil {
		r.srv.UpdateMOTDFile(cfg.Server.MOTDFile)
		r.srv.ReloadMOTD()
	}

	r.logger.Info("config reloaded",
		"logging_level", cfg.Logging.Level,
		"operators", len(cfg.Operators),
		"motd_file", cfg.Server.MOTDFile,
	)
	return nil
}

// startReloadSignalLoop spawns a goroutine that calls
// reloadConfig on every SIGHUP. The loop exits when ctx is
// cancelled. Reload errors are logged but never crash the
// server — a misconfigured reload should leave the previous
// state intact, not take the node offline.
func startReloadSignalLoop(ctx context.Context, deps *reloadDeps) {
	hupCh := make(chan os.Signal, 1)
	signal.Notify(hupCh, syscall.SIGHUP)
	go func() {
		defer signal.Stop(hupCh)
		for {
			select {
			case <-ctx.Done():
				return
			case <-hupCh:
				deps.logger.Info("SIGHUP received, reloading config")
				if err := deps.reloadConfig(ctx); err != nil {
					deps.logger.Warn("config reload failed", "error", err)
				}
			}
		}
	}()
}
