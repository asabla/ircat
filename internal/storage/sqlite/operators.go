package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/asabla/ircat/internal/storage"
)

// operatorStore is the SQLite implementation of [storage.OperatorStore].
type operatorStore struct {
	db *sql.DB
}

const (
	opSelectAll = `SELECT name, host_mask, password_hash, flags, created_at, updated_at FROM operators`
	opSelectOne = opSelectAll + ` WHERE name = ?`
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
		 VALUES (?, ?, ?, ?, ?, ?)`,
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
		   SET host_mask = ?, password_hash = ?, flags = ?, updated_at = ?
		 WHERE name = ?`,
		op.HostMask, op.PasswordHash, joinFlags(op.Flags), now, op.Name,
	)
	if err != nil {
		return fmt.Errorf("operators.Update: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("operators.Update rows: %w", err)
	}
	if n == 0 {
		return storage.ErrNotFound
	}
	op.UpdatedAt = now
	return nil
}

func (s *operatorStore) Delete(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM operators WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("operators.Delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("operators.Delete rows: %w", err)
	}
	if n == 0 {
		return storage.ErrNotFound
	}
	return nil
}

// splitFlags / joinFlags handle the comma-separated flag list. The
// table column is a TEXT for portability across SQLite and Postgres
// (Postgres has TEXT[] but we want one schema source of truth).
func splitFlags(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func joinFlags(flags []string) string {
	return strings.Join(flags, ",")
}

// isUniqueViolation is the SQLite-specific check for a duplicate
// primary key. modernc.org/sqlite surfaces this as a string error
// (no exported error code constants in this version), so we
// substring-match.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "UNIQUE") || strings.Contains(s, "constraint failed")
}
