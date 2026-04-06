package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/asabla/ircat/internal/storage"
)

type operatorStore struct {
	db *sql.DB
}

const (
	opSelectAll = `SELECT name, host_mask, password_hash, flags, created_at, updated_at FROM operators`
	opSelectOne = opSelectAll + ` WHERE name = $1`
)

func (s *operatorStore) Get(ctx context.Context, name string) (*storage.Operator, error) {
	var op storage.Operator
	var flags string
	err := s.db.QueryRowContext(ctx, opSelectOne, name).Scan(
		&op.Name, &op.HostMask, &op.PasswordHash, &flags, &op.CreatedAt, &op.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("operators.Get %q: %w", name, err)
	}
	op.Flags = splitFlags(flags)
	return &op, nil
}

func (s *operatorStore) List(ctx context.Context) ([]storage.Operator, error) {
	rows, err := s.db.QueryContext(ctx, opSelectAll+" ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("operators.List: %w", err)
	}
	defer rows.Close()
	var out []storage.Operator
	for rows.Next() {
		var op storage.Operator
		var flags string
		if err := rows.Scan(&op.Name, &op.HostMask, &op.PasswordHash, &flags, &op.CreatedAt, &op.UpdatedAt); err != nil {
			return nil, fmt.Errorf("operators.List scan: %w", err)
		}
		op.Flags = splitFlags(flags)
		out = append(out, op)
	}
	return out, rows.Err()
}

func (s *operatorStore) Create(ctx context.Context, op *storage.Operator) error {
	if op == nil || op.Name == "" {
		return errors.New("operators.Create: name is required")
	}
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO operators(name, host_mask, password_hash, flags, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		op.Name, op.HostMask, op.PasswordHash, joinFlags(op.Flags), now, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return storage.ErrConflict
		}
		return fmt.Errorf("operators.Create: %w", err)
	}
	op.CreatedAt = now
	op.UpdatedAt = now
	return nil
}

func (s *operatorStore) Update(ctx context.Context, op *storage.Operator) error {
	if op == nil || op.Name == "" {
		return errors.New("operators.Update: name is required")
	}
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx,
		`UPDATE operators
		   SET host_mask = $1, password_hash = $2, flags = $3, updated_at = $4
		 WHERE name = $5`,
		op.HostMask, op.PasswordHash, joinFlags(op.Flags), now, op.Name,
	)
	if err != nil {
		return fmt.Errorf("operators.Update: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return storage.ErrNotFound
	}
	op.UpdatedAt = now
	return nil
}

func (s *operatorStore) Delete(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM operators WHERE name = $1`, name)
	if err != nil {
		return fmt.Errorf("operators.Delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return storage.ErrNotFound
	}
	return nil
}
