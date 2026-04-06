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

type eventStore struct {
	db *sql.DB
}

func (s *eventStore) Append(ctx context.Context, event *storage.AuditEvent) error {
	if event == nil || event.ID == "" {
		return errors.New("events.Append: id is required")
	}
	ts := event.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_events(id, timestamp, type, actor, target, data_json)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		event.ID, ts.UTC(), event.Type, event.Actor, event.Target, event.DataJSON,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return storage.ErrConflict
		}
		return fmt.Errorf("events.Append: %w", err)
	}
	event.Timestamp = ts
	return nil
}

func (s *eventStore) List(ctx context.Context, opts storage.ListEventsOptions) ([]storage.AuditEvent, error) {
	var (
		query strings.Builder
		args  []interface{}
	)
	query.WriteString(`SELECT id, timestamp, type, actor, target, data_json FROM audit_events WHERE 1=1`)
	pi := 1
	addArg := func(v interface{}) {
		args = append(args, v)
	}
	if !opts.Since.IsZero() {
		query.WriteString(fmt.Sprintf(" AND timestamp >= $%d", pi))
		addArg(opts.Since.UTC())
		pi++
	}
	if opts.Type != "" {
		query.WriteString(fmt.Sprintf(" AND type = $%d", pi))
		addArg(opts.Type)
		pi++
	}
	if opts.BeforeID != "" {
		query.WriteString(fmt.Sprintf(" AND id < $%d", pi))
		addArg(opts.BeforeID)
		pi++
	}
	query.WriteString(" ORDER BY id DESC")
	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	query.WriteString(fmt.Sprintf(" LIMIT $%d", pi))
	addArg(limit)

	rows, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("events.List: %w", err)
	}
	defer rows.Close()
	var out []storage.AuditEvent
	for rows.Next() {
		var e storage.AuditEvent
		if err := rows.Scan(&e.ID, &e.Timestamp, &e.Type, &e.Actor, &e.Target, &e.DataJSON); err != nil {
			return nil, fmt.Errorf("events.List scan: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
