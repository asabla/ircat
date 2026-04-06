package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/asabla/ircat/internal/events"
	"github.com/asabla/ircat/internal/storage"
)

// EventPublisher is the small surface the server uses to hand
// events off to [internal/events.Bus]. The bus satisfies it; tests
// can swap in a fake.
type EventPublisher interface {
	Publish(ev events.Event)
}

// Audit event type names. These are the strings that land in the
// audit_events.type column. Tests and the dashboard filter on these.
const (
	AuditTypeOperUp     = "oper_up"
	AuditTypeKick       = "kick"
	AuditTypeMode       = "mode"
	AuditTypeTopic      = "topic"
	AuditTypeAdminAction = "admin_action"
)

// emitAudit writes an audit event to the configured EventStore
// AND publishes it to the event bus (if one is wired up). Both
// sides receive the same event ID so a consumer joining the two
// streams later can correlate them.
//
// The store write is still synchronous — the handler blocks until
// the row lands — but the bus publish is non-blocking per sink.
// Missing store or missing bus each short-circuit independently.
func (s *Server) emitAudit(ctx context.Context, eventType, actor, target string, data any) {
	var dataJSON string
	if data != nil {
		buf, err := json.Marshal(data)
		if err != nil {
			s.logger.Warn("emitAudit marshal failed", "type", eventType, "error", err)
			return
		}
		dataJSON = string(buf)
	}
	nowTs := s.now()
	id, err := newAuditID(nowTs.UnixNano())
	if err != nil {
		s.logger.Warn("emitAudit id failed", "error", err)
		return
	}

	if s.store != nil {
		ev := &storage.AuditEvent{
			ID:        id,
			Timestamp: nowTs,
			Type:      eventType,
			Actor:     actor,
			Target:    target,
			DataJSON:  dataJSON,
		}
		if err := s.store.Events().Append(ctx, ev); err != nil {
			s.logger.Warn("emitAudit append failed", "type", eventType, "error", err)
		}
	}

	if s.eventBus != nil {
		s.eventBus.Publish(events.Event{
			ID:        id,
			Timestamp: nowTs,
			Server:    s.cfg.Server.Name,
			Type:      eventType,
			Actor:     actor,
			Target:    target,
			DataJSON:  dataJSON,
		})
	}
}

var _ = time.Second // keep import used if future code needs it

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
