package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/asabla/ircat/internal/storage"
)

// memoStore is the Postgres implementation of [storage.MemoStore].
type memoStore struct {
	db *sql.DB
}

const (
	memoSelectAll = `SELECT id, sender_id, recipient_id, body, read, created_at FROM memos`
	memoSelectOne = memoSelectAll + ` WHERE id = $1`
)

func (s *memoStore) Send(ctx context.Context, memo *storage.Memo) error {
	if memo == nil || memo.ID == "" {
		return errors.New("memos.Send: id is required")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memos(id, sender_id, recipient_id, body, read, created_at)
		 VALUES ($1, $2, $3, $4, FALSE, $5)`,
		memo.ID, memo.SenderID, memo.RecipientID, memo.Body, memo.CreatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return storage.ErrConflict
		}
		return fmt.Errorf("memos.Send: %w", err)
	}
	return nil
}

func (s *memoStore) ListForRecipient(ctx context.Context, recipientID string) ([]storage.Memo, error) {
	rows, err := s.db.QueryContext(ctx,
		memoSelectAll+` WHERE recipient_id = $1 ORDER BY created_at ASC`, recipientID)
	if err != nil {
		return nil, fmt.Errorf("memos.ListForRecipient: %w", err)
	}
	defer rows.Close()
	var out []storage.Memo
	for rows.Next() {
		var m storage.Memo
		if err := rows.Scan(&m.ID, &m.SenderID, &m.RecipientID, &m.Body, &m.Read, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("memos.ListForRecipient scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *memoStore) Get(ctx context.Context, id string) (*storage.Memo, error) {
	var m storage.Memo
	err := s.db.QueryRowContext(ctx, memoSelectOne, id).Scan(
		&m.ID, &m.SenderID, &m.RecipientID, &m.Body, &m.Read, &m.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("memos.Get %q: %w", id, err)
	}
	return &m, nil
}

func (s *memoStore) MarkRead(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE memos SET read = TRUE WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("memos.MarkRead: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return storage.ErrNotFound
	}
	return nil
}

func (s *memoStore) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM memos WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("memos.Delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return storage.ErrNotFound
	}
	return nil
}

func (s *memoStore) PurgeOlderThan(ctx context.Context, before time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM memos WHERE created_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("memos.PurgeOlderThan: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *memoStore) CountUnread(ctx context.Context, recipientID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memos WHERE recipient_id = $1 AND read = FALSE`, recipientID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("memos.CountUnread: %w", err)
	}
	return count, nil
}
