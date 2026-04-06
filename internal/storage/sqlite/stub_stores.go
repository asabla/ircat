package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/asabla/ircat/internal/storage"
)

// The four sub-stores below are stubbed for the first sqlite commit
// so the Store can satisfy the [storage.Store] interface end-to-end
// while only the operatorStore is fully implemented. The follow-up
// commit fleshes them out with their own SQL.

type tokenStore struct{ db *sql.DB }

func (*tokenStore) Get(ctx context.Context, id string) (*storage.APIToken, error) {
	return nil, errors.New("tokenStore.Get: not implemented yet")
}
func (*tokenStore) GetByHash(ctx context.Context, hash string) (*storage.APIToken, error) {
	return nil, errors.New("tokenStore.GetByHash: not implemented yet")
}
func (*tokenStore) List(ctx context.Context) ([]storage.APIToken, error) {
	return nil, errors.New("tokenStore.List: not implemented yet")
}
func (*tokenStore) Create(ctx context.Context, token *storage.APIToken) error {
	return errors.New("tokenStore.Create: not implemented yet")
}
func (*tokenStore) TouchLastUsed(ctx context.Context, id string, at time.Time) error {
	return errors.New("tokenStore.TouchLastUsed: not implemented yet")
}
func (*tokenStore) Delete(ctx context.Context, id string) error {
	return errors.New("tokenStore.Delete: not implemented yet")
}

type botStore struct{ db *sql.DB }

func (*botStore) Get(ctx context.Context, id string) (*storage.Bot, error) {
	return nil, errors.New("botStore.Get: not implemented yet")
}
func (*botStore) GetByName(ctx context.Context, name string) (*storage.Bot, error) {
	return nil, errors.New("botStore.GetByName: not implemented yet")
}
func (*botStore) List(ctx context.Context) ([]storage.Bot, error) {
	return nil, errors.New("botStore.List: not implemented yet")
}
func (*botStore) Create(ctx context.Context, bot *storage.Bot) error {
	return errors.New("botStore.Create: not implemented yet")
}
func (*botStore) Update(ctx context.Context, bot *storage.Bot) error {
	return errors.New("botStore.Update: not implemented yet")
}
func (*botStore) Delete(ctx context.Context, id string) error {
	return errors.New("botStore.Delete: not implemented yet")
}
func (*botStore) KV() storage.BotKVStore {
	return &botKVStore{}
}

type botKVStore struct{}

func (*botKVStore) Get(ctx context.Context, botID, key string) (string, error) {
	return "", errors.New("botKVStore.Get: not implemented yet")
}
func (*botKVStore) Set(ctx context.Context, botID, key, value string) error {
	return errors.New("botKVStore.Set: not implemented yet")
}
func (*botKVStore) Delete(ctx context.Context, botID, key string) error {
	return errors.New("botKVStore.Delete: not implemented yet")
}
func (*botKVStore) List(ctx context.Context, botID string) (map[string]string, error) {
	return nil, errors.New("botKVStore.List: not implemented yet")
}

type channelStore struct{ db *sql.DB }

func (*channelStore) Get(ctx context.Context, name string) (*storage.ChannelRecord, error) {
	return nil, errors.New("channelStore.Get: not implemented yet")
}
func (*channelStore) List(ctx context.Context) ([]storage.ChannelRecord, error) {
	return nil, errors.New("channelStore.List: not implemented yet")
}
func (*channelStore) Upsert(ctx context.Context, rec *storage.ChannelRecord) error {
	return errors.New("channelStore.Upsert: not implemented yet")
}
func (*channelStore) Delete(ctx context.Context, name string) error {
	return errors.New("channelStore.Delete: not implemented yet")
}

type eventStore struct{ db *sql.DB }

func (*eventStore) Append(ctx context.Context, event *storage.AuditEvent) error {
	return errors.New("eventStore.Append: not implemented yet")
}
func (*eventStore) List(ctx context.Context, opts storage.ListEventsOptions) ([]storage.AuditEvent, error) {
	return nil, errors.New("eventStore.List: not implemented yet")
}
