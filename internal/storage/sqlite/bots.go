package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/asabla/ircat/internal/storage"
)

// botStore implements [storage.BotStore]. Per-bot KV state lives in
// a separate table accessed via [botStore.KV].
type botStore struct {
	db *sql.DB
	kv *botKVStore
}

const (
	botSelectAll = `SELECT id, name, source, enabled, tick_interval_ns, created_at, updated_at FROM bots`
	botSelectOne = botSelectAll + ` WHERE id = ?`
	botSelectNm  = botSelectAll + ` WHERE name = ?`
)

func (s *botStore) Get(ctx context.Context, id string) (*storage.Bot, error) {
	return s.scanOne(ctx, botSelectOne, id, "Get", id)
}

func (s *botStore) GetByName(ctx context.Context, name string) (*storage.Bot, error) {
	return s.scanOne(ctx, botSelectNm, name, "GetByName", name)
}

func (s *botStore) scanOne(ctx context.Context, query, key, op, identifier string) (*storage.Bot, error) {
	var b storage.Bot
	var tickNs int64
	var enabled int
	err := s.db.QueryRowContext(ctx, query, key).Scan(
		&b.ID, &b.Name, &b.Source, &enabled, &tickNs, &b.CreatedAt, &b.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("bots.%s %q: %w", op, identifier, err)
	}
	b.Enabled = enabled != 0
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
		var enabled int
		if err := rows.Scan(&b.ID, &b.Name, &b.Source, &enabled, &tickNs, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, fmt.Errorf("bots.List scan: %w", err)
		}
		b.Enabled = enabled != 0
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
	enabled := 0
	if bot.Enabled {
		enabled = 1
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO bots(id, name, source, enabled, tick_interval_ns, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		bot.ID, bot.Name, bot.Source, enabled, int64(bot.TickInterval), now, now,
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
	enabled := 0
	if bot.Enabled {
		enabled = 1
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE bots SET name = ?, source = ?, enabled = ?, tick_interval_ns = ?, updated_at = ?
		 WHERE id = ?`,
		bot.Name, bot.Source, enabled, int64(bot.TickInterval), now, bot.ID,
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
	res, err := s.db.ExecContext(ctx, `DELETE FROM bots WHERE id = ?`, id)
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

// botKVStore implements [storage.BotKVStore]. Each (bot_id, key)
// row is one entry. The bot row's foreign key cascades deletes so
// removing a bot wipes its KV automatically.
type botKVStore struct {
	db *sql.DB
}

func (s *botKVStore) Get(ctx context.Context, botID, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM bot_kv WHERE bot_id = ? AND key = ?`, botID, key).Scan(&v)
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
		`INSERT INTO bot_kv(bot_id, key, value) VALUES (?, ?, ?)
		 ON CONFLICT(bot_id, key) DO UPDATE SET value = excluded.value`,
		botID, key, value,
	)
	if err != nil {
		return fmt.Errorf("bot_kv.Set: %w", err)
	}
	return nil
}

func (s *botKVStore) Delete(ctx context.Context, botID, key string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM bot_kv WHERE bot_id = ? AND key = ?`, botID, key)
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
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM bot_kv WHERE bot_id = ?`, botID)
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
