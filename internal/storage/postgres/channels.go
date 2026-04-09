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
	if err := s.hydrateLists(ctx, rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// hydrateLists fills Bans, Exceptions, and Invexes on rec by
// querying the three list-mode tables.
func (s *channelStore) hydrateLists(ctx context.Context, rec *storage.ChannelRecord) error {
	bans, err := s.loadList(ctx, "channel_bans", rec.Name)
	if err != nil {
		return err
	}
	rec.Bans = bans
	excepts, err := s.loadList(ctx, "channel_exceptions", rec.Name)
	if err != nil {
		return err
	}
	rec.Exceptions = excepts
	invexes, err := s.loadList(ctx, "channel_invexes", rec.Name)
	if err != nil {
		return err
	}
	rec.Invexes = invexes
	return nil
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

// loadList reads one list-mode table for a single channel. table is
// a hardcoded literal at every call site.
func (s *channelStore) loadList(ctx context.Context, table, channelName string) ([]storage.BanRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT mask, set_by, set_at FROM `+table+` WHERE channel_name = $1`, channelName)
	if err != nil {
		return nil, fmt.Errorf("%s.Load: %w", table, err)
	}
	defer rows.Close()
	var out []storage.BanRecord
	for rows.Next() {
		var b storage.BanRecord
		if err := rows.Scan(&b.Mask, &b.SetBy, &b.SetAt); err != nil {
			return nil, fmt.Errorf("%s scan: %w", table, err)
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
		if err := s.hydrateLists(ctx, &out[i]); err != nil {
			return nil, err
		}
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

	if err := pgReplaceList(ctx, tx, "channel_bans", rec.Name, rec.Bans, now); err != nil {
		return err
	}
	if err := pgReplaceList(ctx, tx, "channel_exceptions", rec.Name, rec.Exceptions, now); err != nil {
		return err
	}
	if err := pgReplaceList(ctx, tx, "channel_invexes", rec.Name, rec.Invexes, now); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("channels.Upsert commit: %w", err)
	}
	rec.UpdatedAt = now
	return nil
}

// pgReplaceList wipes and rewrites one list-mode table for a single
// channel inside the supplied transaction.
func pgReplaceList(ctx context.Context, tx *sql.Tx, table, channel string, entries []storage.BanRecord, now time.Time) error {
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM `+table+` WHERE channel_name = $1`, channel); err != nil {
		return fmt.Errorf("channels.Upsert clear %s: %w", table, err)
	}
	for _, e := range entries {
		setAt := e.SetAt
		if setAt.IsZero() {
			setAt = now
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO `+table+`(channel_name, mask, set_by, set_at) VALUES ($1, $2, $3, $4)`,
			channel, e.Mask, e.SetBy, setAt.UTC(),
		); err != nil {
			return fmt.Errorf("channels.Upsert %s %q: %w", table, e.Mask, err)
		}
	}
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
