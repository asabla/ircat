// Package storage defines ircat's persistence boundary.
//
// Every persistent record in ircat — operators, API tokens, Lua bots,
// channel state worth keeping across restarts, audit events — flows
// through the [Store] interface declared here. Handler code never
// touches database/sql directly; instead it asks for one of the
// sub-stores ([Store.Operators], [Store.APITokens], etc.) and works
// against the typed methods on those interfaces.
//
// The package is intentionally driver-agnostic. The two production
// drivers, internal/storage/sqlite and internal/storage/postgres,
// import this package; this package imports neither. That keeps the
// dependency arrow pointing outward and lets unit tests in this
// package run with no external dependencies.
package storage

import (
	"context"
	"errors"
	"time"
)

// Sentinel errors. Drivers wrap with fmt.Errorf("...: %w", err) so
// callers can distinguish them with errors.Is.
var (
	// ErrNotFound is returned by Get-style methods when the requested
	// record does not exist.
	ErrNotFound = errors.New("storage: not found")
	// ErrConflict is returned when an insert would violate a unique
	// constraint (e.g. duplicate operator name).
	ErrConflict = errors.New("storage: conflict")
)

// Store is the top-level handle on persistent ircat state. Drivers
// construct one in their Open function.
//
// All methods are safe to call concurrently from multiple goroutines.
// Sub-store handles returned from Operators / APITokens / etc. are
// long-lived and should be cached on the server struct, not fetched
// per-request.
type Store interface {
	// Operators returns the operator account store.
	Operators() OperatorStore
	// APITokens returns the dashboard / admin API token store.
	APITokens() TokenStore
	// Bots returns the Lua bot definition store.
	Bots() BotStore
	// Channels returns the persistent channel state store. Only
	// channels that have been explicitly persisted (e.g. via a TOPIC
	// or MODE write-through) appear here; ephemeral channels live
	// only in [internal/state].
	Channels() PersistentChannelStore
	// Events returns the audit log event store.
	Events() EventStore
	// Migrate runs any pending schema migrations. Idempotent.
	Migrate(ctx context.Context) error
	// Close releases all resources held by the driver.
	Close() error
}

// Operator is one operator account record.
type Operator struct {
	Name         string
	HostMask     string // glob mask the OPER attempt is matched against
	PasswordHash string // bcrypt or argon2id encoded hash
	Flags        []string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// OperatorStore is the persistence interface for operator accounts.
type OperatorStore interface {
	Get(ctx context.Context, name string) (*Operator, error)
	List(ctx context.Context) ([]Operator, error)
	Create(ctx context.Context, op *Operator) error
	Update(ctx context.Context, op *Operator) error
	Delete(ctx context.Context, name string) error
}

// APIToken is one admin API token record. The plaintext token is
// shown to the operator exactly once at creation time; only the hash
// is stored.
type APIToken struct {
	ID         string // ULID; user-visible identifier
	Label      string
	Hash       string // sha256 hex of the plaintext token
	Scopes     []string
	CreatedAt  time.Time
	LastUsedAt time.Time
}

// TokenStore is the persistence interface for API tokens.
type TokenStore interface {
	Get(ctx context.Context, id string) (*APIToken, error)
	GetByHash(ctx context.Context, hash string) (*APIToken, error)
	List(ctx context.Context) ([]APIToken, error)
	Create(ctx context.Context, token *APIToken) error
	TouchLastUsed(ctx context.Context, id string, at time.Time) error
	Delete(ctx context.Context, id string) error
}

// Bot is one Lua bot definition.
type Bot struct {
	ID            string // ULID
	Name          string // display name, unique per node
	Source        string // raw Lua source
	Enabled       bool
	TickInterval  time.Duration
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// BotStore is the persistence interface for Lua bot definitions.
// Per-bot KV state lives in a separate table accessed via
// [BotKVStore]; the supervisor in internal/bots glues them together.
type BotStore interface {
	Get(ctx context.Context, id string) (*Bot, error)
	GetByName(ctx context.Context, name string) (*Bot, error)
	List(ctx context.Context) ([]Bot, error)
	Create(ctx context.Context, bot *Bot) error
	Update(ctx context.Context, bot *Bot) error
	Delete(ctx context.Context, id string) error

	// KV returns the per-bot key/value substore. Implementations
	// may share storage with the bot record table; callers should
	// treat the returned handle as long-lived.
	KV() BotKVStore
}

// BotKVStore is a small key-value substore scoped per bot. Used by
// the Lua runtime ctx.kv_get/ctx.kv_set surface.
type BotKVStore interface {
	Get(ctx context.Context, botID, key string) (string, error)
	Set(ctx context.Context, botID, key, value string) error
	Delete(ctx context.Context, botID, key string) error
	List(ctx context.Context, botID string) (map[string]string, error)
}

// ChannelRecord is one persistent channel.
//
// We do not persist the membership list — channels rebuild that on
// reconnect. We persist anything an operator might lose if the
// process restarted: the topic and who set it, the boolean modes,
// the key and limit, and the ban list.
type ChannelRecord struct {
	Name         string
	Topic        string
	TopicSetBy   string
	TopicSetAt   time.Time
	ModeWord     string // "+nt", "+ntik", etc.
	Key          string
	Limit        int
	Bans         []BanRecord
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// BanRecord is one channel ban entry.
type BanRecord struct {
	Mask  string
	SetBy string
	SetAt time.Time
}

// PersistentChannelStore is the persistence interface for channel
// state that should survive a server restart.
type PersistentChannelStore interface {
	Get(ctx context.Context, name string) (*ChannelRecord, error)
	List(ctx context.Context) ([]ChannelRecord, error)
	// Upsert writes the full record (including bans) atomically.
	Upsert(ctx context.Context, rec *ChannelRecord) error
	Delete(ctx context.Context, name string) error
}

// AuditEvent is one entry in the audit log.
type AuditEvent struct {
	ID        string         // ULID
	Timestamp time.Time
	Type      string         // "oper_up", "kick", "mode", "admin_action", ...
	Actor     string         // hostmask or operator name
	Target    string         // affected user or channel, optional
	DataJSON  string         // JSON-encoded payload, optional
}

// EventStore is the persistence interface for audit events. The
// store is append-only from the application perspective; deletion
// happens via retention policy outside of the API surface.
type EventStore interface {
	Append(ctx context.Context, event *AuditEvent) error
	List(ctx context.Context, opts ListEventsOptions) ([]AuditEvent, error)
}

// ListEventsOptions controls EventStore.List queries.
type ListEventsOptions struct {
	Since      time.Time // zero = no lower bound
	Type       string    // empty = all types
	Limit      int       // <=0 = driver default (typically 100)
	BeforeID   string    // cursor; "" = newest first
}
