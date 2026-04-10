package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/asabla/ircat/internal/storage"
)

// accountStore is the Postgres implementation of [storage.AccountStore].
type accountStore struct {
	db *sql.DB
}

const (
	acctSelectAll  = `SELECT id, username, password_hash, email, verified, created_at, updated_at FROM accounts`
	acctSelectOne  = acctSelectAll + ` WHERE username = $1`
	acctSelectByID = acctSelectAll + ` WHERE id = $1`
)

func (s *accountStore) Get(ctx context.Context, username string) (*storage.Account, error) {
	var acct storage.Account
	err := s.db.QueryRowContext(ctx, acctSelectOne, username).Scan(
		&acct.ID, &acct.Username, &acct.PasswordHash, &acct.Email, &acct.Verified, &acct.CreatedAt, &acct.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("accounts.Get %q: %w", username, err)
	}
	return &acct, nil
}

func (s *accountStore) GetByID(ctx context.Context, id string) (*storage.Account, error) {
	var acct storage.Account
	err := s.db.QueryRowContext(ctx, acctSelectByID, id).Scan(
		&acct.ID, &acct.Username, &acct.PasswordHash, &acct.Email, &acct.Verified, &acct.CreatedAt, &acct.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("accounts.GetByID %q: %w", id, err)
	}
	return &acct, nil
}

func (s *accountStore) List(ctx context.Context) ([]storage.Account, error) {
	rows, err := s.db.QueryContext(ctx, acctSelectAll+" ORDER BY username")
	if err != nil {
		return nil, fmt.Errorf("accounts.List: %w", err)
	}
	defer rows.Close()
	var out []storage.Account
	for rows.Next() {
		var acct storage.Account
		if err := rows.Scan(&acct.ID, &acct.Username, &acct.PasswordHash, &acct.Email, &acct.Verified, &acct.CreatedAt, &acct.UpdatedAt); err != nil {
			return nil, fmt.Errorf("accounts.List scan: %w", err)
		}
		out = append(out, acct)
	}
	return out, rows.Err()
}

func (s *accountStore) Create(ctx context.Context, acct *storage.Account) error {
	if acct == nil || acct.Username == "" {
		return errors.New("accounts.Create: username is required")
	}
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO accounts(id, username, password_hash, email, verified, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		acct.ID, acct.Username, acct.PasswordHash, acct.Email, acct.Verified, now, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return storage.ErrConflict
		}
		return fmt.Errorf("accounts.Create: %w", err)
	}
	acct.CreatedAt = now
	acct.UpdatedAt = now
	return nil
}

func (s *accountStore) Update(ctx context.Context, acct *storage.Account) error {
	if acct == nil || acct.Username == "" {
		return errors.New("accounts.Update: username is required")
	}
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx,
		`UPDATE accounts
		   SET password_hash = $1, email = $2, verified = $3, updated_at = $4
		 WHERE username = $5`,
		acct.PasswordHash, acct.Email, acct.Verified, now, acct.Username,
	)
	if err != nil {
		return fmt.Errorf("accounts.Update: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return storage.ErrNotFound
	}
	acct.UpdatedAt = now
	return nil
}

func (s *accountStore) Delete(ctx context.Context, username string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM accounts WHERE username = $1`, username)
	if err != nil {
		return fmt.Errorf("accounts.Delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return storage.ErrNotFound
	}
	return nil
}
