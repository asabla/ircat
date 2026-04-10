package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/asabla/ircat/internal/storage"
)

// registeredChannelStore is the SQLite implementation of
// [storage.RegisteredChannelStore].
type registeredChannelStore struct {
	db *sql.DB
}

const (
	rcSelectAll = `SELECT channel, founder_id, guard, keep_topic, created_at, updated_at FROM registered_channels`
	rcSelectOne = rcSelectAll + ` WHERE channel = ?`
)

func (s *registeredChannelStore) Get(ctx context.Context, channel string) (*storage.RegisteredChannel, error) {
	var rc storage.RegisteredChannel
	err := s.db.QueryRowContext(ctx, rcSelectOne, channel).Scan(
		&rc.Channel, &rc.FounderID, &rc.Guard, &rc.KeepTopic, &rc.CreatedAt, &rc.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("registered_channels.Get %q: %w", channel, err)
	}
	return &rc, nil
}

func (s *registeredChannelStore) List(ctx context.Context) ([]storage.RegisteredChannel, error) {
	rows, err := s.db.QueryContext(ctx, rcSelectAll+" ORDER BY channel")
	if err != nil {
		return nil, fmt.Errorf("registered_channels.List: %w", err)
	}
	defer rows.Close()
	var out []storage.RegisteredChannel
	for rows.Next() {
		var rc storage.RegisteredChannel
		if err := rows.Scan(&rc.Channel, &rc.FounderID, &rc.Guard, &rc.KeepTopic, &rc.CreatedAt, &rc.UpdatedAt); err != nil {
			return nil, fmt.Errorf("registered_channels.List scan: %w", err)
		}
		out = append(out, rc)
	}
	return out, rows.Err()
}

func (s *registeredChannelStore) Create(ctx context.Context, rc *storage.RegisteredChannel) error {
	if rc == nil || rc.Channel == "" {
		return errors.New("registered_channels.Create: channel is required")
	}
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO registered_channels(channel, founder_id, guard, keep_topic, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		rc.Channel, rc.FounderID, rc.Guard, rc.KeepTopic, now, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return storage.ErrConflict
		}
		return fmt.Errorf("registered_channels.Create: %w", err)
	}
	rc.CreatedAt = now
	rc.UpdatedAt = now
	return nil
}

func (s *registeredChannelStore) Update(ctx context.Context, rc *storage.RegisteredChannel) error {
	if rc == nil || rc.Channel == "" {
		return errors.New("registered_channels.Update: channel is required")
	}
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx,
		`UPDATE registered_channels
		   SET founder_id = ?, guard = ?, keep_topic = ?, updated_at = ?
		 WHERE channel = ?`,
		rc.FounderID, rc.Guard, rc.KeepTopic, now, rc.Channel,
	)
	if err != nil {
		return fmt.Errorf("registered_channels.Update: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("registered_channels.Update rows: %w", err)
	}
	if n == 0 {
		return storage.ErrNotFound
	}
	rc.UpdatedAt = now
	return nil
}

func (s *registeredChannelStore) Delete(ctx context.Context, channel string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM registered_channels WHERE channel = ?`, channel)
	if err != nil {
		return fmt.Errorf("registered_channels.Delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("registered_channels.Delete rows: %w", err)
	}
	if n == 0 {
		return storage.ErrNotFound
	}
	return nil
}

// Access list operations

const (
	caSelectAll = `SELECT channel, account_id, flags, created_at FROM channel_access`
	caSelectOne = caSelectAll + ` WHERE channel = ? AND account_id = ?`
	caSelectCh  = caSelectAll + ` WHERE channel = ? ORDER BY account_id`
)

func (s *registeredChannelStore) GetAccess(ctx context.Context, channel, accountID string) (*storage.ChannelAccess, error) {
	var ca storage.ChannelAccess
	err := s.db.QueryRowContext(ctx, caSelectOne, channel, accountID).Scan(
		&ca.Channel, &ca.AccountID, &ca.Flags, &ca.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("channel_access.GetAccess: %w", err)
	}
	return &ca, nil
}

func (s *registeredChannelStore) ListAccess(ctx context.Context, channel string) ([]storage.ChannelAccess, error) {
	rows, err := s.db.QueryContext(ctx, caSelectCh, channel)
	if err != nil {
		return nil, fmt.Errorf("channel_access.ListAccess: %w", err)
	}
	defer rows.Close()
	var out []storage.ChannelAccess
	for rows.Next() {
		var ca storage.ChannelAccess
		if err := rows.Scan(&ca.Channel, &ca.AccountID, &ca.Flags, &ca.CreatedAt); err != nil {
			return nil, fmt.Errorf("channel_access.ListAccess scan: %w", err)
		}
		out = append(out, ca)
	}
	return out, rows.Err()
}

func (s *registeredChannelStore) SetAccess(ctx context.Context, ca *storage.ChannelAccess) error {
	if ca == nil || ca.Channel == "" || ca.AccountID == "" {
		return errors.New("channel_access.SetAccess: channel and account_id are required")
	}
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO channel_access(channel, account_id, flags, created_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(channel, account_id) DO UPDATE SET flags = excluded.flags`,
		ca.Channel, ca.AccountID, ca.Flags, now,
	)
	if err != nil {
		return fmt.Errorf("channel_access.SetAccess: %w", err)
	}
	ca.CreatedAt = now
	return nil
}

func (s *registeredChannelStore) DeleteAccess(ctx context.Context, channel, accountID string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM channel_access WHERE channel = ? AND account_id = ?`,
		channel, accountID,
	)
	if err != nil {
		return fmt.Errorf("channel_access.DeleteAccess: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("channel_access.DeleteAccess rows: %w", err)
	}
	if n == 0 {
		return storage.ErrNotFound
	}
	return nil
}
