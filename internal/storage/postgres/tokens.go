package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/asabla/ircat/internal/storage"
)

type tokenStore struct {
	db *sql.DB
}

const (
	tokenSelectAll = `SELECT id, label, hash, scopes, created_at, last_used_at FROM api_tokens`
	tokenSelectOne = tokenSelectAll + ` WHERE id = $1`
	tokenSelectByH = tokenSelectAll + ` WHERE hash = $1`
)

func (s *tokenStore) Get(ctx context.Context, id string) (*storage.APIToken, error) {
	return s.scanOne(ctx, tokenSelectOne, id)
}

func (s *tokenStore) GetByHash(ctx context.Context, hash string) (*storage.APIToken, error) {
	return s.scanOne(ctx, tokenSelectByH, hash)
}

func (s *tokenStore) scanOne(ctx context.Context, query, key string) (*storage.APIToken, error) {
	var t storage.APIToken
	var scopes string
	var lastUsed sql.NullTime
	err := s.db.QueryRowContext(ctx, query, key).Scan(&t.ID, &t.Label, &t.Hash, &scopes, &t.CreatedAt, &lastUsed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("tokens lookup: %w", err)
	}
	t.Scopes = splitFlags(scopes)
	if lastUsed.Valid {
		t.LastUsedAt = lastUsed.Time
	}
	return &t, nil
}

func (s *tokenStore) List(ctx context.Context) ([]storage.APIToken, error) {
	rows, err := s.db.QueryContext(ctx, tokenSelectAll+" ORDER BY created_at DESC")
	if err != nil {
		return nil, fmt.Errorf("tokens.List: %w", err)
	}
	defer rows.Close()
	var out []storage.APIToken
	for rows.Next() {
		var t storage.APIToken
		var scopes string
		var lastUsed sql.NullTime
		if err := rows.Scan(&t.ID, &t.Label, &t.Hash, &scopes, &t.CreatedAt, &lastUsed); err != nil {
			return nil, fmt.Errorf("tokens.List scan: %w", err)
		}
		t.Scopes = splitFlags(scopes)
		if lastUsed.Valid {
			t.LastUsedAt = lastUsed.Time
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *tokenStore) Create(ctx context.Context, token *storage.APIToken) error {
	if token == nil || token.ID == "" || token.Hash == "" {
		return errors.New("tokens.Create: id and hash are required")
	}
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO api_tokens(id, label, hash, scopes, created_at) VALUES ($1, $2, $3, $4, $5)`,
		token.ID, token.Label, token.Hash, strings.Join(token.Scopes, ","), now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return storage.ErrConflict
		}
		return fmt.Errorf("tokens.Create: %w", err)
	}
	token.CreatedAt = now
	return nil
}

func (s *tokenStore) TouchLastUsed(ctx context.Context, id string, at time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = $1 WHERE id = $2`, at.UTC(), id)
	if err != nil {
		return fmt.Errorf("tokens.TouchLastUsed: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return storage.ErrNotFound
	}
	return nil
}

func (s *tokenStore) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM api_tokens WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("tokens.Delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return storage.ErrNotFound
	}
	return nil
}
