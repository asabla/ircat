package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/asabla/ircat/internal/storage"
)

type channelStore struct {
	db *sql.DB
}

const (
	channelSelectAll = `SELECT name, topic, topic_set_by, topic_set_at, mode_word, channel_key, user_limit, created_at, updated_at FROM channels`
	channelSelectOne = channelSelectAll + ` WHERE name = $1`
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
		`SELECT mask, set_by, set_at FROM channel_bans WHERE channel_name = $1`, channelName)
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
	for i := range out {
		bans, err := s.loadBans(ctx, out[i].Name)
		if err != nil {
			return nil, err
		}
		out[i].Bans = bans
	}
	return out, nil
}

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
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 ON CONFLICT(name) DO UPDATE SET
		   topic = EXCLUDED.topic,
		   topic_set_by = EXCLUDED.topic_set_by,
		   topic_set_at = EXCLUDED.topic_set_at,
		   mode_word = EXCLUDED.mode_word,
		   channel_key = EXCLUDED.channel_key,
		   user_limit = EXCLUDED.user_limit,
		   updated_at = EXCLUDED.updated_at`,
		rec.Name, rec.Topic, rec.TopicSetBy, topicSetAt,
		rec.ModeWord, rec.Key, rec.Limit, now, now,
	); err != nil {
		return fmt.Errorf("channels.Upsert insert: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM channel_bans WHERE channel_name = $1`, rec.Name); err != nil {
		return fmt.Errorf("channels.Upsert clear bans: %w", err)
	}
	for _, b := range rec.Bans {
		setAt := b.SetAt
		if setAt.IsZero() {
			setAt = now
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO channel_bans(channel_name, mask, set_by, set_at) VALUES ($1, $2, $3, $4)`,
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
	res, err := s.db.ExecContext(ctx, `DELETE FROM channels WHERE name = $1`, name)
	if err != nil {
		return fmt.Errorf("channels.Delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return storage.ErrNotFound
	}
	return nil
}
