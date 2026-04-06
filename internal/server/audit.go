package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/asabla/ircat/internal/storage"
)

// Audit event type names. These are the strings that land in the
// audit_events.type column. Tests and the dashboard filter on these.
const (
	AuditTypeOperUp     = "oper_up"
	AuditTypeKick       = "kick"
	AuditTypeMode       = "mode"
	AuditTypeTopic      = "topic"
	AuditTypeAdminAction = "admin_action"
)

// emitAudit appends an audit event to the configured EventStore.
//
// A nil store turns this into a no-op so unit tests that do not
// exercise persistence can run without one. The event ID is a
// time-prefixed random hex string so the audit log sorts naturally
// by ID and the next milestone (M4 dashboard) can use it as a
// stable cursor.
//
// The data argument is JSON-encoded; pass nil for events that do
// not need a payload.
func (s *Server) emitAudit(ctx context.Context, eventType, actor, target string, data any) {
	if s.store == nil {
		return
	}
	var dataJSON string
	if data != nil {
		buf, err := json.Marshal(data)
		if err != nil {
			s.logger.Warn("emitAudit marshal failed", "type", eventType, "error", err)
			return
		}
		dataJSON = string(buf)
	}
	id, err := newAuditID(s.now().UnixNano())
	if err != nil {
		s.logger.Warn("emitAudit id failed", "error", err)
		return
	}
	ev := &storage.AuditEvent{
		ID:       id,
		Type:     eventType,
		Actor:    actor,
		Target:   target,
		DataJSON: dataJSON,
	}
	if err := s.store.Events().Append(ctx, ev); err != nil {
		s.logger.Warn("emitAudit append failed", "type", eventType, "error", err)
	}
}

// newAuditID returns a sortable string ID built from the supplied
// nanosecond timestamp plus 8 bytes of random padding. The format
// is two hex segments separated by '-' so it remains lexicographically
// orderable across the audit log even when many events land in the
// same nanosecond.
func newAuditID(unixNano int64) (string, error) {
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", fmt.Errorf("audit id rand: %w", err)
	}
	return fmt.Sprintf("%016x-%s", uint64(unixNano), hex.EncodeToString(rnd[:])), nil
}
