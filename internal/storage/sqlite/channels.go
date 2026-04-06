package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/asabla/ircat/internal/storage"
)

// channelStore implements [storage.PersistentChannelStore]. Channel
// state and ban list live in two tables joined on channel_name; the
// Upsert path writes both atomically inside a single transaction.
type channelStore struct {
	db *sql.DB
}

const (
	channelSelectAll = `SELECT name, topic, topic_set_by, topic_set_at, mode_word, channel_key, user_limit, created_at, updated_at FROM channels`
	channelSelectOne = channelSelectAll + ` WHERE name = ?`
)

func (s *channelStore) Get(ctx context.Context, name string) (*storage.ChannelRecord, error) {
	rec, err := s.scanOne(ctx, channelSelectOne, name)
	if err != nil {
		return nil, err
	}
	bans, err := s.loadBans(ctx, name)
	if err != nil {
		return nil, err
	}
	rec.Bans = bans
	return rec, nil
}

func (s *channelStore) scanOne(ctx context.Context, query, name string) (*storage.ChannelRecord, error) {
	var rec storage.ChannelRecord
	var topicSetAt sql.NullTime
	err := s.db.QueryRowContext(ctx, query, name).Scan(
		&rec.Name, &rec.Topic, &rec.TopicSetBy, &topicSetAt,
		&rec.ModeWord, &rec.Key, &rec.Limit, &rec.CreatedAt, &rec.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("channels.Get %q: %w", name, err)
	}
	if topicSetAt.Valid {
		rec.TopicSetAt = topicSetAt.Time
	}
	return &rec, nil
}

func (s *channelStore) loadBans(ctx context.Context, channelName string) ([]storage.BanRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT mask, set_by, set_at FROM channel_bans WHERE channel_name = ?`, channelName)
	if err != nil {
		return nil, fmt.Errorf("channel_bans.Load: %w", err)
	}
	defer rows.Close()
	var out []storage.BanRecord
	for rows.Next() {
		var b storage.BanRecord
		if err := rows.Scan(&b.Mask, &b.SetBy, &b.SetAt); err != nil {
			return nil, fmt.Errorf("channel_bans scan: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *channelStore) List(ctx context.Context) ([]storage.ChannelRecord, error) {
	rows, err := s.db.QueryContext(ctx, channelSelectAll+" ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("channels.List: %w", err)
	}
	defer rows.Close()
	var out []storage.ChannelRecord
	for rows.Next() {
		var rec storage.ChannelRecord
		var topicSetAt sql.NullTime
		if err := rows.Scan(
			&rec.Name, &rec.Topic, &rec.TopicSetBy, &topicSetAt,
			&rec.ModeWord, &rec.Key, &rec.Limit, &rec.CreatedAt, &rec.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("channels.List scan: %w", err)
		}
		if topicSetAt.Valid {
			rec.TopicSetAt = topicSetAt.Time
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Hydrate bans for each row. The N+1 here is fine for the
	// expected channel counts (a few hundred at most); we can
	// switch to a join+grouping if it ever shows up in profiles.
	for i := range out {
		bans, err := s.loadBans(ctx, out[i].Name)
		if err != nil {
			return nil, err
		}
		out[i].Bans = bans
	}
	return out, nil
}

// Upsert writes the channel row plus its full ban list inside one
// transaction. Existing bans are wiped and replaced; this is the
// only safe way to keep the in-memory state and the persisted
// state in sync without per-ban deltas.
func (s *channelStore) Upsert(ctx context.Context, rec *storage.ChannelRecord) error {
	if rec == nil || rec.Name == "" {
		return errors.New("channels.Upsert: name is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("channels.Upsert begin: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	var topicSetAt interface{}
	if !rec.TopicSetAt.IsZero() {
		topicSetAt = rec.TopicSetAt.UTC()
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO channels(name, topic, topic_set_by, topic_set_at, mode_word, channel_key, user_limit, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET
		   topic = excluded.topic,
		   topic_set_by = excluded.topic_set_by,
		   topic_set_at = excluded.topic_set_at,
		   mode_word = excluded.mode_word,
		   channel_key = excluded.channel_key,
		   user_limit = excluded.user_limit,
		   updated_at = excluded.updated_at`,
		rec.Name, rec.Topic, rec.TopicSetBy, topicSetAt,
		rec.ModeWord, rec.Key, rec.Limit, now, now,
	); err != nil {
		return fmt.Errorf("channels.Upsert insert: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM channel_bans WHERE channel_name = ?`, rec.Name); err != nil {
		return fmt.Errorf("channels.Upsert clear bans: %w", err)
	}
	for _, b := range rec.Bans {
		setAt := b.SetAt
		if setAt.IsZero() {
			setAt = now
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO channel_bans(channel_name, mask, set_by, set_at) VALUES (?, ?, ?, ?)`,
			rec.Name, b.Mask, b.SetBy, setAt.UTC(),
		); err != nil {
			return fmt.Errorf("channels.Upsert ban %q: %w", b.Mask, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("channels.Upsert commit: %w", err)
	}
	rec.UpdatedAt = now
	return nil
}

func (s *channelStore) Delete(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM channels WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("channels.Delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return storage.ErrNotFound
	}
	return nil
}
