package api

import (
	"net/http"
	"sort"
)

type channelRecord struct {
	Name        string             `json:"name"`
	Topic       string             `json:"topic"`
	TopicSetBy  string             `json:"topic_set_by,omitempty"`
	TopicSetAt  string             `json:"topic_set_at,omitempty"`
	ModeWord    string             `json:"mode_word"`
	Key         string             `json:"key,omitempty"`
	Limit       int                `json:"limit,omitempty"`
	MemberCount int                `json:"member_count"`
	Members     []channelMemberRec `json:"members,omitempty"`
	Bans        []channelBanRec    `json:"bans,omitempty"`
}

type channelMemberRec struct {
	Nick   string `json:"nick"`
	Op     bool   `json:"op,omitempty"`
	Voice  bool   `json:"voice,omitempty"`
	Prefix string `json:"prefix,omitempty"`
}

type channelBanRec struct {
	Mask  string `json:"mask"`
	SetAt string `json:"set_at,omitempty"`
}

func (a *API) handleListChannels(w http.ResponseWriter, r *http.Request) {
	if a.actuator == nil {
		writeJSON(w, http.StatusOK, map[string]any{"channels": []channelRecord{}})
		return
	}
	chans := a.actuator.SnapshotChannels()
	out := make([]channelRecord, 0, len(chans))
	for _, ch := range chans {
		modes, _ := ch.ModeString()
		topic, setBy, setAt := ch.Topic()
		rec := channelRecord{
			Name:        ch.Name(),
			Topic:       topic,
			TopicSetBy:  setBy,
			ModeWord:    modes,
			Key:         ch.Key(),
			Limit:       ch.Limit(),
			MemberCount: ch.MemberCount(),
		}
		if !setAt.IsZero() {
			rec.TopicSetAt = setAt.UTC().Format(rfc3339Nano)
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{"channels": out})
}

func (a *API) handleGetChannel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if a.world == nil {
		writeError(w, http.StatusNotFound, "not_found", "channel does not exist")
		return
	}
	ch := a.world.FindChannel(name)
	if ch == nil {
		writeError(w, http.StatusNotFound, "not_found", "channel does not exist")
		return
	}
	modes, _ := ch.ModeString()
	topic, setBy, setAt := ch.Topic()
	rec := channelRecord{
		Name:        ch.Name(),
		Topic:       topic,
		TopicSetBy:  setBy,
		ModeWord:    modes,
		Key:         ch.Key(),
		Limit:       ch.Limit(),
		MemberCount: ch.MemberCount(),
	}
	if !setAt.IsZero() {
		rec.TopicSetAt = setAt.UTC().Format(rfc3339Nano)
	}
	for id, mem := range ch.MemberIDs() {
		u := a.world.FindByID(id)
		if u == nil {
			continue
		}
		rec.Members = append(rec.Members, channelMemberRec{
			Nick:   u.Nick,
			Op:     mem.IsOp(),
			Voice:  mem.IsVoice(),
			Prefix: mem.Prefix(),
		})
	}
	sort.Slice(rec.Members, func(i, j int) bool { return rec.Members[i].Nick < rec.Members[j].Nick })
	for mask, at := range ch.Bans() {
		entry := channelBanRec{Mask: mask}
		if !at.IsZero() {
			entry.SetAt = at.UTC().Format(rfc3339Nano)
		}
		rec.Bans = append(rec.Bans, entry)
	}
	sort.Slice(rec.Bans, func(i, j int) bool { return rec.Bans[i].Mask < rec.Bans[j].Mask })
	writeJSON(w, http.StatusOK, rec)
}
