package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/asabla/ircat/internal/auth"
	"github.com/asabla/ircat/internal/config"
	"github.com/asabla/ircat/internal/storage"
)

// bootstrapStore reconciles the persistent operator table with the
// declarative config sources:
//
//   - cfg.Auth.InitialAdmin: when the operator table is empty and
//     the config supplies a username + password, create the admin.
//     This makes a fresh ircat install usable without an external
//     dashboard or admin API call to mint the first credential.
//   - cfg.Operators[]: each entry is upserted into the store. The
//     password is taken pre-hashed from password_hash (resolved by
//     config.resolveEnv from password_hash_env), so static infra
//     setups can keep operator definitions in version-controlled
//     YAML/JSON.
//
// Bootstrap is idempotent and runs on every startup. A nil store
// (which the cmd path never produces but tests may) makes it a
// no-op.
func bootstrapStore(ctx context.Context, store storage.Store, cfg *config.Config, logger *slog.Logger) error {
	if store == nil {
		return nil
	}

	if err := bootstrapInitialAdmin(ctx, store, cfg, logger); err != nil {
		return fmt.Errorf("bootstrap initial admin: %w", err)
	}
	if err := syncStaticOperators(ctx, store, cfg, logger); err != nil {
		return fmt.Errorf("sync static operators: %w", err)
	}
	return nil
}

func bootstrapInitialAdmin(ctx context.Context, store storage.Store, cfg *config.Config, logger *slog.Logger) error {
	admin := cfg.Auth.InitialAdmin
	if admin.Username == "" && admin.Password == "" {
		// Both fields blank means the operator deliberately
		// opted out of the bootstrap. No warning needed.
		return nil
	}
	existing, err := store.Operators().List(ctx)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		// Operators already exist; the bootstrap is a no-op
		// on every restart after the first one.
		return nil
	}
	// At this point the operators table is empty AND the
	// config has at least partly opted into the bootstrap.
	// Three failure modes get a WARN instead of a silent
	// skip — they all leave the dashboard unloggable on a
	// fresh boot, which v1.2 hit in production:
	//
	//   - username set, password env unset → silent skip
	//   - username set, password resolved to empty string
	//   - password set but username forgotten
	//
	// In every case the WARN tells the operator what is
	// missing and how to recover via the `ircat operator
	// add` subcommand introduced alongside this fix.
	if admin.Username == "" || admin.Password == "" {
		var missing string
		switch {
		case admin.Username == "" && admin.Password != "":
			missing = "auth.initial_admin.username"
		case admin.Username != "" && admin.Password == "":
			missing = "auth.initial_admin.password (set IRCAT_INITIAL_ADMIN_PASSWORD or password_env)"
		}
		logger.Warn("initial admin bootstrap skipped — operators table is empty and config is incomplete",
			"missing", missing,
			"recovery", "run `ircat operator add <username>` against this store, or set the missing field and restart",
		)
		return nil
	}
	hash, err := auth.Hash(cfg.Auth.PasswordHash, admin.Password, auth.Argon2idParams{
		MemoryKiB:   uint32(cfg.Auth.Argon2id.MemoryKiB),
		Iterations:  uint32(cfg.Auth.Argon2id.Iterations),
		Parallelism: uint8(cfg.Auth.Argon2id.Parallelism),
	})
	if err != nil {
		return fmt.Errorf("hash initial admin password: %w", err)
	}
	op := &storage.Operator{
		Name:         admin.Username,
		HostMask:     "",
		PasswordHash: hash,
		Flags:        []string{"all"},
	}
	if err := store.Operators().Create(ctx, op); err != nil {
		// A racing bootstrap could have created the admin in
		// between our List and Create; that is the conflict path,
		// not a fatal error.
		if errors.Is(err, storage.ErrConflict) {
			return nil
		}
		return err
	}
	logger.Info("bootstrapped initial admin", "username", admin.Username)
	return nil
}

func syncStaticOperators(ctx context.Context, store storage.Store, cfg *config.Config, logger *slog.Logger) error {
	for _, entry := range cfg.Operators {
		if entry.Name == "" || entry.PasswordHash == "" {
			continue
		}
		op := &storage.Operator{
			Name:         entry.Name,
			HostMask:     entry.HostMask,
			PasswordHash: entry.PasswordHash,
			Flags:        entry.Flags,
		}
		// Try Create first; on conflict fall through to Update so
		// the static config wins on every restart for the fields
		// it carries.
		err := store.Operators().Create(ctx, op)
		switch {
		case err == nil:
			logger.Info("created static operator", "name", entry.Name)
		case errors.Is(err, storage.ErrConflict):
			if err := store.Operators().Update(ctx, op); err != nil {
				return fmt.Errorf("update static operator %q: %w", entry.Name, err)
			}
			logger.Debug("synced static operator", "name", entry.Name)
		default:
			return fmt.Errorf("create static operator %q: %w", entry.Name, err)
		}
	}
	return nil
}
