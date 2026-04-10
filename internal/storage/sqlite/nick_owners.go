package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/asabla/ircat/internal/storage"
)

// nickOwnerStore is the SQLite implementation of [storage.NickOwnerStore].
type nickOwnerStore struct {
	db *sql.DB
}

const (
	noSelectAll       = `SELECT nick, account_id, is_primary, created_at FROM nick_owners`
	noSelectOne       = noSelectAll + ` WHERE nick = ?`
	noSelectByAccount = noSelectAll + ` WHERE account_id = ? ORDER BY is_primary DESC, nick`
)

func (s *nickOwnerStore) Get(ctx context.Context, nick string) (*storage.NickOwner, error) {
	var no storage.NickOwner
	err := s.db.QueryRowContext(ctx, noSelectOne, nick).Scan(
		&no.Nick, &no.AccountID, &no.Primary, &no.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("nick_owners.Get %q: %w", nick, err)
	}
	return &no, nil
}

func (s *nickOwnerStore) ListByAccount(ctx context.Context, accountID string) ([]storage.NickOwner, error) {
	rows, err := s.db.QueryContext(ctx, noSelectByAccount, accountID)
	if err != nil {
		return nil, fmt.Errorf("nick_owners.ListByAccount: %w", err)
	}
	defer rows.Close()
	var out []storage.NickOwner
	for rows.Next() {
		var no storage.NickOwner
		if err := rows.Scan(&no.Nick, &no.AccountID, &no.Primary, &no.CreatedAt); err != nil {
			return nil, fmt.Errorf("nick_owners.ListByAccount scan: %w", err)
		}
		out = append(out, no)
	}
	return out, rows.Err()
}

func (s *nickOwnerStore) Create(ctx context.Context, no *storage.NickOwner) error {
	if no == nil || no.Nick == "" {
		return errors.New("nick_owners.Create: nick is required")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO nick_owners(nick, account_id, is_primary, created_at)
		 VALUES (?, ?, ?, ?)`,
		no.Nick, no.AccountID, no.Primary, no.CreatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return storage.ErrConflict
		}
		return fmt.Errorf("nick_owners.Create: %w", err)
	}
	return nil
}

func (s *nickOwnerStore) Delete(ctx context.Context, nick string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM nick_owners WHERE nick = ?`, nick)
	if err != nil {
		return fmt.Errorf("nick_owners.Delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("nick_owners.Delete rows: %w", err)
	}
	if n == 0 {
		return storage.ErrNotFound
	}
	return nil
}
