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

// eventStore implements [storage.EventStore] over the audit_events
// table. It is append-only from the application surface; deletion
// happens via retention policy outside of this interface.
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
		 VALUES (?, ?, ?, ?, ?, ?)`,
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
	if !opts.Since.IsZero() {
		query.WriteString(` AND timestamp >= ?`)
		args = append(args, opts.Since.UTC())
	}
	if opts.Type != "" {
		query.WriteString(` AND type = ?`)
		args = append(args, opts.Type)
	}
	if opts.BeforeID != "" {
		query.WriteString(` AND id < ?`)
		args = append(args, opts.BeforeID)
	}
	query.WriteString(` ORDER BY id DESC`)
	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	query.WriteString(` LIMIT ?`)
	args = append(args, limit)

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
