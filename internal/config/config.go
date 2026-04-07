// Package config defines ircat's configuration schema and loaders.
//
// A single Config struct is the source of truth. Both JSON and YAML
// files decode into the same struct using the json struct tags — the
// YAML loader builds a generic Go value tree and round-trips it
// through encoding/json so the two formats stay perfectly in sync.
//
// See docs/CONFIG.md for the user-facing reference.
package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// SchemaVersion is the only config schema version this binary
// understands. Bumping this is a breaking change.
const SchemaVersion = 1

// Config is the root configuration object.
type Config struct {
	Version int `json:"version"`

	Server     ServerConfig     `json:"server"`
	Storage    StorageConfig    `json:"storage"`
	Dashboard  DashboardConfig  `json:"dashboard"`
	API        APIConfig        `json:"api"`
	Auth       AuthConfig       `json:"auth"`
	Events     EventsConfig     `json:"events"`
	Bots       BotsConfig       `json:"bots"`
	Federation FederationConfig `json:"federation"`
	Logging    LoggingConfig    `json:"logging"`
	Operators  []OperatorConfig `json:"operators"`
}

// ServerConfig groups settings for the IRC daemon proper.
type ServerConfig struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Network     string       `json:"network"`
	MOTDFile    string       `json:"motd_file"`
	Admin       AdminInfo    `json:"admin"`
	Listeners   []Listener   `json:"listeners"`
	Limits      LimitsConfig `json:"limits"`
}

// AdminInfo is the contact block advertised via /ADMIN.
type AdminInfo struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// Listener describes one bound TCP listener.
type Listener struct {
	Address  string `json:"address"`
	TLS      bool   `json:"tls"`
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`
}

// LimitsConfig holds the operational limits the server enforces.
// Zero values fall back to RFC-friendly defaults in [Config.Validate].
type LimitsConfig struct {
	MaxClients          int `json:"max_clients"`
	MaxChannelsPerUser  int `json:"max_channels_per_user"`
	NickLength          int `json:"nick_length"`
	ChannelLength       int `json:"channel_length"`
	TopicLength         int `json:"topic_length"`
	KickReasonLength    int `json:"kick_reason_length"`
	AwayLength          int `json:"away_length"`
	SendQBytes          int `json:"sendq_bytes"`
	RecvQMessages       int `json:"recvq_messages"`
	PingIntervalSeconds int `json:"ping_interval_seconds"`
	PingTimeoutSeconds  int `json:"ping_timeout_seconds"`

	// Flood control parameters for PRIVMSG / NOTICE. Burst is the
	// number of messages a client can send back-to-back; refill is
	// how many tokens are credited per second; ViolationsToKick is
	// the number of dropped messages tolerated before disconnect.
	MessageBurst            int `json:"message_burst"`
	MessageRefillPerSecond  int `json:"message_refill_per_second"`
	MessageViolationsToKick int `json:"message_violations_to_kick"`
}

// StorageConfig selects between SQLite and Postgres backends.
type StorageConfig struct {
	Driver   string         `json:"driver"`
	SQLite   SQLiteConfig   `json:"sqlite"`
	Postgres PostgresConfig `json:"postgres"`
}

// SQLiteConfig configures the SQLite driver.
type SQLiteConfig struct {
	Path        string `json:"path"`
	JournalMode string `json:"journal_mode"`
}

// PostgresConfig configures the Postgres driver.
type PostgresConfig struct {
	DSN          string `json:"dsn"`
	DSNEnv       string `json:"dsn_env"`
	MaxOpenConns int    `json:"max_open_conns"`
	MaxIdleConns int    `json:"max_idle_conns"`
}

// DashboardConfig configures the htmx dashboard listener.
type DashboardConfig struct {
	Enabled bool          `json:"enabled"`
	Address string        `json:"address"`
	TLS     TLSConfig     `json:"tls"`
	Session SessionConfig `json:"session"`
}

// TLSConfig is the dashboard TLS block; the IRC TLS listeners use
// [Listener] directly.
type TLSConfig struct {
	Enabled  bool   `json:"enabled"`
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`
}

// SessionConfig configures the dashboard session cookie.
type SessionConfig struct {
	CookieName  string `json:"cookie_name"`
	MaxAgeHours int    `json:"max_age_hours"`
	Secure      bool   `json:"secure"`
	SameSite    string `json:"same_site"`
}

// APIConfig configures the admin HTTP API. The API shares the
// dashboard listener; this struct only toggles the router and the
// CORS allow-list.
type APIConfig struct {
	Enabled      bool     `json:"enabled"`
	AllowOrigins []string `json:"allow_origins"`
}

// AuthConfig configures password hashing and the bootstrap admin.
type AuthConfig struct {
	PasswordHash string             `json:"password_hash"`
	Argon2id     Argon2idParams     `json:"argon2id"`
	InitialAdmin InitialAdminConfig `json:"initial_admin"`
}

// Argon2idParams holds the cost parameters for the argon2id hasher.
type Argon2idParams struct {
	MemoryKiB   int `json:"memory_kib"`
	Iterations  int `json:"iterations"`
	Parallelism int `json:"parallelism"`
}

// InitialAdminConfig is the bootstrap admin used the first time the
// operator table is empty.
type InitialAdminConfig struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	PasswordEnv string `json:"password_env"`
}

// EventsConfig declares the configured event sinks.
type EventsConfig struct {
	Sinks []SinkConfig `json:"sinks"`
}

// SinkConfig is the union shape for all sink kinds. Fields not used
// by a given Type are ignored.
type SinkConfig struct {
	Type    string `json:"type"`
	Enabled *bool  `json:"enabled,omitempty"`

	// jsonl
	Path     string `json:"path,omitempty"`
	RotateMB int    `json:"rotate_mb,omitempty"`
	Keep     int    `json:"keep,omitempty"`

	// webhook
	URL            string      `json:"url,omitempty"`
	Secret         string      `json:"secret,omitempty"`
	SecretEnv      string      `json:"secret_env,omitempty"`
	TimeoutSeconds int         `json:"timeout_seconds,omitempty"`
	BatchMax       int         `json:"batch_max,omitempty"`
	BatchMaxAgeMs  int         `json:"batch_max_age_ms,omitempty"`
	Retry          *RetryBlock `json:"retry,omitempty"`
	DeadLetterPath string      `json:"dead_letter_path,omitempty"`

	// optional type filter (empty = all)
	Types []string `json:"types,omitempty"`
}

// RetryBlock configures the webhook sink's retry behaviour.
type RetryBlock struct {
	MaxAttempts    int   `json:"max_attempts"`
	BackoffSeconds []int `json:"backoff_seconds"`
}

// BotsConfig configures the Lua bot runtime.
type BotsConfig struct {
	Enabled                 bool `json:"enabled"`
	MaxBots                 int  `json:"max_bots"`
	PerBotMemoryMB          int  `json:"per_bot_memory_mb"`
	PerBotInstructionBudget int  `json:"per_bot_instruction_budget"`
}

// FederationConfig declares server-to-server links.
type FederationConfig struct {
	Enabled       bool   `json:"enabled"`
	MyServerName  string `json:"my_server_name"`
	ListenAddress string `json:"listen_address"`
	// ListenCertFile / ListenKeyFile turn the inbound federation
	// listener into a TLS listener. If both are set, every
	// accept=true peer connects over TLS. Optional.
	ListenCertFile string `json:"listen_cert_file"`
	ListenKeyFile  string `json:"listen_key_file"`
	// BroadcastMode picks the channel-event routing strategy.
	// Valid values:
	//
	//   - "subscription" (default): channel events route only to
	//     peers that have at least one member in the channel.
	//     JOIN is always fanned out because it is the discovery
	//     message that establishes a peer's subscription.
	//   - "fanout": channel events go to every peer regardless
	//     of subscription. v1.0 behaviour, retained for one
	//     minor cycle as a regression fallback.
	BroadcastMode string     `json:"broadcast_mode"`
	Links         []LinkSpec `json:"links"`
}

// LinkSpec is one configured federation peer.
type LinkSpec struct {
	Name           string `json:"name"`
	Accept         bool   `json:"accept"`
	Connect        bool   `json:"connect"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	PasswordIn     string `json:"password_in"`
	PasswordInEnv  string `json:"password_in_env"`
	PasswordOut    string `json:"password_out"`
	PasswordOutEnv string `json:"password_out_env"`
	TLS            bool   `json:"tls"`
	TLSFingerprint string `json:"tls_fingerprint"`
	Zip            bool   `json:"zip"`
}

// LoggingConfig is consumed by [internal/logging.New].
type LoggingConfig struct {
	Level             string `json:"level"`
	Format            string `json:"format"`
	RingBufferEntries int    `json:"ring_buffer_entries"`
}

// OperatorConfig is one statically-configured operator block. These
// are merged with the DB-backed operator table at runtime.
type OperatorConfig struct {
	Name            string   `json:"name"`
	HostMask        string   `json:"host_mask"`
	PasswordHash    string   `json:"password_hash"`
	PasswordHashEnv string   `json:"password_hash_env"`
	Flags           []string `json:"flags"`
}

// Load reads, decodes, validates, and resolves env-var indirection on
// a config file. The path's extension selects the decoder; ".json"
// uses [LoadJSON], ".yaml"/".yml" uses [LoadYAML]. A path of "-"
// reads JSON from stdin.
func Load(path string) (*Config, error) {
	var (
		data []byte
		err  error
		kind string
		base string
	)
	switch {
	case path == "-":
		kind = "json"
		data, err = io.ReadAll(os.Stdin)
	default:
		base = filepath.Dir(path)
		kind = formatFromExt(path)
		if kind == "" {
			return nil, fmt.Errorf("unknown config extension on %q (want .json, .yaml, or .yml)", path)
		}
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}

	var cfg *Config
	switch kind {
	case "json":
		cfg, err = LoadJSON(data)
	case "yaml":
		cfg, err = LoadYAML(data)
	default:
		return nil, fmt.Errorf("unsupported config kind %q", kind)
	}
	if err != nil {
		return nil, err
	}

	cfg.applyDefaults()
	cfg.resolvePathsRelativeTo(base)
	if err := cfg.resolveEnv(os.Getenv); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func formatFromExt(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	}
	return ""
}

// applyDefaults fills in zero-value fields with sensible defaults.
// Defaults are conservative — anything an operator is likely to want
// to tune is left untouched if explicitly set.
func (c *Config) applyDefaults() {
	if c.Version == 0 {
		c.Version = SchemaVersion
	}
	// Server limits
	l := &c.Server.Limits
	if l.MaxClients == 0 {
		l.MaxClients = 1024
	}
	if l.MaxChannelsPerUser == 0 {
		l.MaxChannelsPerUser = 50
	}
	if l.NickLength == 0 {
		l.NickLength = 30
	}
	if l.ChannelLength == 0 {
		l.ChannelLength = 50
	}
	if l.TopicLength == 0 {
		l.TopicLength = 390
	}
	if l.KickReasonLength == 0 {
		l.KickReasonLength = 255
	}
	if l.AwayLength == 0 {
		l.AwayLength = 255
	}
	if l.SendQBytes == 0 {
		l.SendQBytes = 1 << 20
	}
	if l.RecvQMessages == 0 {
		l.RecvQMessages = 64
	}
	if l.PingIntervalSeconds == 0 {
		l.PingIntervalSeconds = 120
	}
	if l.PingTimeoutSeconds == 0 {
		l.PingTimeoutSeconds = 240
	}
	if l.MessageBurst == 0 {
		l.MessageBurst = 10
	}
	if l.MessageRefillPerSecond == 0 {
		l.MessageRefillPerSecond = 2
	}
	if l.MessageViolationsToKick == 0 {
		l.MessageViolationsToKick = 5
	}

	if c.Storage.Driver == "" {
		c.Storage.Driver = "sqlite"
	}
	if c.Storage.Driver == "sqlite" && c.Storage.SQLite.JournalMode == "" {
		c.Storage.SQLite.JournalMode = "wal"
	}

	if c.Dashboard.Session.CookieName == "" {
		c.Dashboard.Session.CookieName = "ircat_session"
	}
	if c.Dashboard.Session.MaxAgeHours == 0 {
		c.Dashboard.Session.MaxAgeHours = 24
	}
	if c.Dashboard.Session.SameSite == "" {
		c.Dashboard.Session.SameSite = "lax"
	}

	if c.Auth.PasswordHash == "" {
		c.Auth.PasswordHash = "argon2id"
	}
	if c.Auth.Argon2id.MemoryKiB == 0 {
		c.Auth.Argon2id.MemoryKiB = 65536
	}
	if c.Auth.Argon2id.Iterations == 0 {
		c.Auth.Argon2id.Iterations = 3
	}
	if c.Auth.Argon2id.Parallelism == 0 {
		c.Auth.Argon2id.Parallelism = 2
	}

	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	}
	if c.Logging.Format == "" {
		c.Logging.Format = "json"
	}
	if c.Logging.RingBufferEntries == 0 {
		c.Logging.RingBufferEntries = 10_000
	}

	if c.Bots.MaxBots == 0 {
		c.Bots.MaxBots = 100
	}
}

// resolvePathsRelativeTo rewrites file-path fields that look relative
// to be anchored at the config file's directory. Absolute paths and
// the empty string are left alone.
func (c *Config) resolvePathsRelativeTo(base string) {
	if base == "" {
		return
	}
	rel := func(p *string) {
		if *p == "" || filepath.IsAbs(*p) {
			return
		}
		*p = filepath.Join(base, *p)
	}
	rel(&c.Server.MOTDFile)
	rel(&c.Storage.SQLite.Path)
	rel(&c.Dashboard.TLS.CertFile)
	rel(&c.Dashboard.TLS.KeyFile)
	for i := range c.Server.Listeners {
		rel(&c.Server.Listeners[i].CertFile)
		rel(&c.Server.Listeners[i].KeyFile)
	}
	for i := range c.Events.Sinks {
		rel(&c.Events.Sinks[i].Path)
		rel(&c.Events.Sinks[i].DeadLetterPath)
	}
}

// ErrInvalid is the sentinel error wrapped by [Config.Validate].
var ErrInvalid = errors.New("invalid config")
