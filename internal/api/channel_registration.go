package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"

	"github.com/asabla/ircat/internal/storage"
)

// registeredChannelRecord is the JSON shape used by the channel
// registration endpoints.
type registeredChannelRecord struct {
	Channel   string                `json:"channel"`
	FounderID string                `json:"founder_id"`
	Guard     bool                  `json:"guard"`
	KeepTopic bool                  `json:"keep_topic"`
	CreatedAt string                `json:"created_at,omitempty"`
	UpdatedAt string                `json:"updated_at,omitempty"`
	Access    []channelAccessRecord `json:"access,omitempty"`
}

type channelAccessRecord struct {
	AccountID string `json:"account_id"`
	Flags     string `json:"flags"`
	CreatedAt string `json:"created_at,omitempty"`
}

type createRegistrationRequest struct {
	FounderID string `json:"founder_id"`
	Guard     bool   `json:"guard"`
	KeepTopic bool   `json:"keep_topic"`
}

type updateRegistrationRequest = createRegistrationRequest

type setAccessRequest struct {
	Flags string `json:"flags"`
}

func (a *API) handleGetChannelRegistration(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	rc, err := a.store.RegisteredChannels().Get(r.Context(), name)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "channel is not registered")
			return
		}
		a.logger.Warn("registered_channels.Get failed", "channel", name, "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "get registration")
		return
	}
	rec := registeredChannelRecord{
		Channel:   rc.Channel,
		FounderID: rc.FounderID,
		Guard:     rc.Guard,
		KeepTopic: rc.KeepTopic,
		CreatedAt: rc.CreatedAt.UTC().Format(rfc3339Nano),
		UpdatedAt: rc.UpdatedAt.UTC().Format(rfc3339Nano),
	}
	access, err := a.store.RegisteredChannels().ListAccess(r.Context(), name)
	if err == nil {
		for _, ca := range access {
			rec.Access = append(rec.Access, channelAccessRecord{
				AccountID: ca.AccountID,
				Flags:     ca.Flags,
				CreatedAt: ca.CreatedAt.UTC().Format(rfc3339Nano),
			})
		}
		sort.Slice(rec.Access, func(i, j int) bool { return rec.Access[i].AccountID < rec.Access[j].AccountID })
	}
	writeJSON(w, http.StatusOK, rec)
}

func (a *API) handleCreateChannelRegistration(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req createRegistrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if req.FounderID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "founder_id is required")
		return
	}
	// Verify the founder account exists.
	if _, err := a.store.Accounts().GetByID(r.Context(), req.FounderID); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusBadRequest, "bad_request", "founder account does not exist")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", "lookup founder")
		return
	}
	rc := &storage.RegisteredChannel{
		Channel:   name,
		FounderID: req.FounderID,
		Guard:     req.Guard,
		KeepTopic: req.KeepTopic,
	}
	if err := a.store.RegisteredChannels().Create(r.Context(), rc); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			writeError(w, http.StatusConflict, "conflict", "channel is already registered")
			return
		}
		a.logger.Warn("registered_channels.Create failed", "channel", name, "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "register channel")
		return
	}
	writeJSON(w, http.StatusCreated, registeredChannelRecord{
		Channel:   rc.Channel,
		FounderID: rc.FounderID,
		Guard:     rc.Guard,
		KeepTopic: rc.KeepTopic,
		CreatedAt: rc.CreatedAt.UTC().Format(rfc3339Nano),
		UpdatedAt: rc.UpdatedAt.UTC().Format(rfc3339Nano),
	})
}

func (a *API) handleUpdateChannelRegistration(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req updateRegistrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	rc, err := a.store.RegisteredChannels().Get(r.Context(), name)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "channel is not registered")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", "get registration")
		return
	}
	if req.FounderID != "" {
		if _, err := a.store.Accounts().GetByID(r.Context(), req.FounderID); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeError(w, http.StatusBadRequest, "bad_request", "founder account does not exist")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal", "lookup founder")
			return
		}
		rc.FounderID = req.FounderID
	}
	rc.Guard = req.Guard
	rc.KeepTopic = req.KeepTopic
	if err := a.store.RegisteredChannels().Update(r.Context(), rc); err != nil {
		a.logger.Warn("registered_channels.Update failed", "channel", name, "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "update registration")
		return
	}
	writeJSON(w, http.StatusOK, registeredChannelRecord{
		Channel:   rc.Channel,
		FounderID: rc.FounderID,
		Guard:     rc.Guard,
		KeepTopic: rc.KeepTopic,
		CreatedAt: rc.CreatedAt.UTC().Format(rfc3339Nano),
		UpdatedAt: rc.UpdatedAt.UTC().Format(rfc3339Nano),
	})
}

func (a *API) handleDeleteChannelRegistration(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := a.store.RegisteredChannels().Delete(r.Context(), name); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "channel is not registered")
			return
		}
		a.logger.Warn("registered_channels.Delete failed", "channel", name, "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "delete registration")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleSetChannelAccess(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	accountID := r.PathValue("account_id")
	var req setAccessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	// Confirm the registration exists so we do not silently write
	// orphan access rows when a channel is not registered.
	if _, err := a.store.RegisteredChannels().Get(r.Context(), name); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "channel is not registered")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", "get registration")
		return
	}
	if _, err := a.store.Accounts().GetByID(r.Context(), accountID); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusBadRequest, "bad_request", "account does not exist")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", "lookup account")
		return
	}
	ca := &storage.ChannelAccess{
		Channel:   name,
		AccountID: accountID,
		Flags:     req.Flags,
	}
	if err := a.store.RegisteredChannels().SetAccess(r.Context(), ca); err != nil {
		a.logger.Warn("registered_channels.SetAccess failed", "channel", name, "account", accountID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "set access")
		return
	}
	writeJSON(w, http.StatusOK, channelAccessRecord{
		AccountID: ca.AccountID,
		Flags:     ca.Flags,
		CreatedAt: ca.CreatedAt.UTC().Format(rfc3339Nano),
	})
}

func (a *API) handleDeleteChannelAccess(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	accountID := r.PathValue("account_id")
	if err := a.store.RegisteredChannels().DeleteAccess(r.Context(), name, accountID); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "access entry does not exist")
			return
		}
		a.logger.Warn("registered_channels.DeleteAccess failed", "channel", name, "account", accountID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "delete access")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
