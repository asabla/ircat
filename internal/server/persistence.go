package server

import (
	"context"
	"time"

	"github.com/asabla/ircat/internal/state"
	"github.com/asabla/ircat/internal/storage"
)

// restorePersistentChannels recreates channels in the World from
// the persistent channel store. Called from Server.Run before any
// listener is bound so first-joiner-becomes-op handling does not
// race against the restore.
func (s *Server) restorePersistentChannels(ctx context.Context) error {
	if s.store == nil {
		return nil
	}
	records, err := s.store.Channels().List(ctx)
	if err != nil {
		return err
	}
	for _, rec := range records {
		ch, _ := s.world.EnsureChannel(rec.Name)
		ch.RestoreState(
			rec.Topic, rec.TopicSetBy, rec.TopicSetAt,
			rec.ModeWord, rec.Key, rec.Limit,
			banRecordsToMap(rec.Bans),
		)
		s.logger.Info("restored channel", "name", rec.Name, "modes", rec.ModeWord)
	}
	return nil
}

// persistChannel writes the in-memory state of ch back to the
// persistent channel store. Called after any successful mutation
// (TOPIC, MODE) on a channel. A nil store turns this into a no-op
// so tests that do not exercise persistence can run unchanged.
func (s *Server) persistChannel(ctx context.Context, ch *state.Channel) {
	if s.store == nil {
		return
	}
	modeWord, _ := ch.ModeString()
	topic, setBy, setAt := ch.Topic()
	rec := &storage.ChannelRecord{
		Name:       ch.Name(),
		Topic:      topic,
		TopicSetBy: setBy,
		TopicSetAt: setAt,
		ModeWord:   modeWord,
		Key:        ch.Key(),
		Limit:      ch.Limit(),
		Bans:       banRecordsFromMap(ch.Bans()),
	}
	if err := s.store.Channels().Upsert(ctx, rec); err != nil {
		s.logger.Warn("persist channel failed", "channel", ch.Name(), "error", err)
	}
}

// banRecordsFromMap converts the channel ban map (mask -> set time)
// into a slice of BanRecords for storage. The set_by field is left
// blank because state.Channel does not track per-ban actor; the
// dashboard / API path will populate it once those land in M4.
func banRecordsFromMap(in map[string]time.Time) []storage.BanRecord {
	if len(in) == 0 {
		return nil
	}
	out := make([]storage.BanRecord, 0, len(in))
	for mask, at := range in {
		out = append(out, storage.BanRecord{Mask: mask, SetAt: at})
	}
	return out
}

// banRecordsToMap reverses banRecordsFromMap for the restore path.
func banRecordsToMap(in []storage.BanRecord) map[string]time.Time {
	out := make(map[string]time.Time, len(in))
	for _, b := range in {
		out[b.Mask] = b.SetAt
	}
	return out
}
