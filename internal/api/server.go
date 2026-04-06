package api

import (
	"net/http"
	"time"
)

// serverInfoResponse is the JSON shape returned by GET /server.
type serverInfoResponse struct {
	Name      string    `json:"name"`
	Network   string    `json:"network"`
	Version   string    `json:"version"`
	StartedAt time.Time `json:"started_at"`
	Listeners []string  `json:"listeners"`
	Users     int       `json:"users"`
	Channels  int       `json:"channels"`
}

func (a *API) handleGetServer(w http.ResponseWriter, r *http.Request) {
	resp := serverInfoResponse{}
	if a.serverInfo != nil {
		resp.Name = a.serverInfo.ServerName()
		resp.Network = a.serverInfo.NetworkName()
		resp.Version = a.serverInfo.Version()
		resp.StartedAt = a.serverInfo.StartedAt()
	}
	if a.actuator != nil {
		resp.Listeners = a.actuator.ListenerAddresses()
	}
	if a.world != nil {
		resp.Users = a.world.UserCount()
		resp.Channels = a.world.ChannelCount()
	}
	writeJSON(w, http.StatusOK, resp)
}
