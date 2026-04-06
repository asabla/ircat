package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/asabla/ircat/internal/storage"
)

type botStore struct {
	db *sql.DB
	kv *botKVStore
}

const (
	botSelectAll = `SELECT id, name, source, enabled, tick_interval_ns, created_at, updated_at FROM bots`
	botSelectOne = botSelectAll + ` WHERE id = $1`
	botSelectNm  = botSelectAll + ` WHERE name = $1`
)

func (s *botStore) Get(ctx context.Context, id string) (*storage.Bot, error) {
	return s.scanOne(ctx, botSelectOne, id)
}

func (s *botStore) GetByName(ctx context.Context, name string) (*storage.Bot, error) {
	return s.scanOne(ctx, botSelectNm, name)
}

func (s *botStore) scanOne(ctx context.Context, query, key string) (*storage.Bot, error) {
	var b storage.Bot
	var tickNs int64
	err := s.db.QueryRowContext(ctx, query, key).Scan(
		&b.ID, &b.Name, &b.Source, &b.Enabled, &tickNs, &b.CreatedAt, &b.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("bots lookup: %w", err)
	}
	b.TickInterval = time.Duration(tickNs)
	return &b, nil
}

func (s *botStore) List(ctx context.Context) ([]storage.Bot, error) {
	rows, err := s.db.QueryContext(ctx, botSelectAll+" ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("bots.List: %w", err)
	}
	defer rows.Close()
	var out []storage.Bot
	for rows.Next() {
		var b storage.Bot
		var tickNs int64
		if err := rows.Scan(&b.ID, &b.Name, &b.Source, &b.Enabled, &tickNs, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, fmt.Errorf("bots.List scan: %w", err)
		}
		b.TickInterval = time.Duration(tickNs)
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *botStore) Create(ctx context.Context, bot *storage.Bot) error {
	if bot == nil || bot.ID == "" || bot.Name == "" {
		return errors.New("bots.Create: id and name are required")
	}
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO bots(id, name, source, enabled, tick_interval_ns, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		bot.ID, bot.Name, bot.Source, bot.Enabled, int64(bot.TickInterval), now, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return storage.ErrConflict
		}
		return fmt.Errorf("bots.Create: %w", err)
	}
	bot.CreatedAt = now
	bot.UpdatedAt = now
	return nil
}

func (s *botStore) Update(ctx context.Context, bot *storage.Bot) error {
	if bot == nil || bot.ID == "" {
		return errors.New("bots.Update: id is required")
	}
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx,
		`UPDATE bots SET name = $1, source = $2, enabled = $3, tick_interval_ns = $4, updated_at = $5
		 WHERE id = $6`,
		bot.Name, bot.Source, bot.Enabled, int64(bot.TickInterval), now, bot.ID,
	)
	if err != nil {
		return fmt.Errorf("bots.Update: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return storage.ErrNotFound
	}
	bot.UpdatedAt = now
	return nil
}

func (s *botStore) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM bots WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("bots.Delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return storage.ErrNotFound
	}
	return nil
}

func (s *botStore) KV() storage.BotKVStore {
	if s.kv == nil {
		s.kv = &botKVStore{db: s.db}
	}
	return s.kv
}

type botKVStore struct {
	db *sql.DB
}

func (s *botKVStore) Get(ctx context.Context, botID, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM bot_kv WHERE bot_id = $1 AND key = $2`, botID, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", storage.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("bot_kv.Get: %w", err)
	}
	return v, nil
}

func (s *botKVStore) Set(ctx context.Context, botID, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO bot_kv(bot_id, key, value) VALUES ($1, $2, $3)
		 ON CONFLICT(bot_id, key) DO UPDATE SET value = EXCLUDED.value`,
		botID, key, value,
	)
	if err != nil {
		return fmt.Errorf("bot_kv.Set: %w", err)
	}
	return nil
}

func (s *botKVStore) Delete(ctx context.Context, botID, key string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM bot_kv WHERE bot_id = $1 AND key = $2`, botID, key)
	if err != nil {
		return fmt.Errorf("bot_kv.Delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return storage.ErrNotFound
	}
	return nil
}

func (s *botKVStore) List(ctx context.Context, botID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM bot_kv WHERE bot_id = $1`, botID)
	if err != nil {
		return nil, fmt.Errorf("bot_kv.List: %w", err)
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("bot_kv.List scan: %w", err)
		}
		out[k] = v
	}
	return out, rows.Err()
}
