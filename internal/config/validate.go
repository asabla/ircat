package config

import (
	"fmt"
	"net"
	"strings"
)

// Validate enforces the config invariants. It returns nil on success
// or an [ErrInvalid]-wrapped error whose message names the offending
// field path. Defaults are assumed already applied.
func (c *Config) Validate() error {
	if c.Version != SchemaVersion {
		return invalidf("version: got %d, want %d", c.Version, SchemaVersion)
	}

	if strings.TrimSpace(c.Server.Name) == "" {
		return invalidf("server.name: required")
	}
	if !looksLikeServerName(c.Server.Name) {
		return invalidf("server.name: %q does not look like a hostname", c.Server.Name)
	}
	if strings.TrimSpace(c.Server.Network) == "" {
		return invalidf("server.network: required")
	}

	if l := c.Server.Limits; l.PingTimeoutSeconds <= l.PingIntervalSeconds {
		return invalidf("server.limits.ping_timeout_seconds (%d) must be greater than ping_interval_seconds (%d)",
			l.PingTimeoutSeconds, l.PingIntervalSeconds)
	}
	if c.Server.Limits.WhowasHistory < 0 {
		return invalidf("server.limits.whowas_history: must be non-negative, got %d",
			c.Server.Limits.WhowasHistory)
	}
	// ClientPassword is intentionally compared verbatim with no
	// hashing — it is a single network-wide gate, not a per-user
	// secret. Reject obvious mistakes (leading/trailing spaces,
	// the literal placeholder strings env-substitution might
	// leave behind) so a misconfigured deployment fails on boot
	// rather than silently letting everyone in.
	if p := c.Server.ClientPassword; p != "" {
		if strings.TrimSpace(p) != p {
			return invalidf("server.client_password: leading or trailing whitespace not allowed")
		}
		if p == "${IRCAT_CLIENT_PASSWORD}" || p == "$IRCAT_CLIENT_PASSWORD" {
			return invalidf("server.client_password: env substitution placeholder %q was not expanded", p)
		}
	}

	if len(c.Server.Listeners) == 0 {
		return invalidf("server.listeners: at least one listener is required")
	}
	for i, l := range c.Server.Listeners {
		if l.Address == "" {
			return invalidf("server.listeners[%d].address: required", i)
		}
		if _, _, err := net.SplitHostPort(l.Address); err != nil {
			return invalidf("server.listeners[%d].address: %v", i, err)
		}
		if l.TLS {
			if l.CertFile == "" || l.KeyFile == "" {
				return invalidf("server.listeners[%d]: tls listeners require cert_file and key_file", i)
			}
		}
	}

	switch c.Storage.Driver {
	case "sqlite":
		if c.Storage.SQLite.Path == "" {
			return invalidf("storage.sqlite.path: required when driver=sqlite")
		}
	case "postgres":
		if c.Storage.Postgres.DSN == "" && c.Storage.Postgres.DSNEnv == "" {
			return invalidf("storage.postgres.dsn: required when driver=postgres (or set dsn_env)")
		}
	default:
		return invalidf("storage.driver: %q (want sqlite or postgres)", c.Storage.Driver)
	}

	if c.Dashboard.Enabled {
		if c.Dashboard.Address == "" {
			return invalidf("dashboard.address: required when dashboard.enabled=true")
		}
		if _, _, err := net.SplitHostPort(c.Dashboard.Address); err != nil {
			return invalidf("dashboard.address: %v", err)
		}
		if c.Dashboard.TLS.Enabled && (c.Dashboard.TLS.CertFile == "" || c.Dashboard.TLS.KeyFile == "") {
			return invalidf("dashboard.tls: cert_file and key_file required when tls.enabled=true")
		}
		switch strings.ToLower(c.Dashboard.Session.SameSite) {
		case "lax", "strict", "none":
		default:
			return invalidf("dashboard.session.same_site: %q (want lax, strict, or none)", c.Dashboard.Session.SameSite)
		}
	}

	switch c.Auth.PasswordHash {
	case "argon2id", "bcrypt":
	default:
		return invalidf("auth.password_hash: %q (want argon2id or bcrypt)", c.Auth.PasswordHash)
	}

	for i, sink := range c.Events.Sinks {
		if err := validateSink(i, sink); err != nil {
			return err
		}
	}

	if c.Federation.Enabled {
		if c.Federation.MyServerName == "" {
			return invalidf("federation.my_server_name: required when federation.enabled=true")
		}
		for i, link := range c.Federation.Links {
			if link.Name == "" {
				return invalidf("federation.links[%d].name: required", i)
			}
			if link.Connect && (link.Host == "" || link.Port == 0) {
				return invalidf("federation.links[%d]: host and port required when connect=true", i)
			}
		}
	}

	switch strings.ToLower(c.Logging.Format) {
	case "json", "text":
	default:
		return invalidf("logging.format: %q (want json or text)", c.Logging.Format)
	}
	switch strings.ToLower(c.Logging.Level) {
	case "debug", "info", "warn", "warning", "error", "err":
	default:
		return invalidf("logging.level: %q (want debug, info, warn, error)", c.Logging.Level)
	}

	for i, op := range c.Operators {
		if op.Name == "" {
			return invalidf("operators[%d].name: required", i)
		}
		if op.PasswordHash == "" {
			return invalidf("operators[%d].password_hash: required (resolved from password_hash_env)", i)
		}
	}

	return nil
}

func validateSink(i int, sink SinkConfig) error {
	switch sink.Type {
	case "jsonl":
		if sink.Path == "" {
			return invalidf("events.sinks[%d].path: required for jsonl sink", i)
		}
	case "webhook":
		if sink.URL == "" {
			return invalidf("events.sinks[%d].url: required for webhook sink", i)
		}
	case "":
		return invalidf("events.sinks[%d].type: required", i)
	default:
		return invalidf("events.sinks[%d].type: %q (want jsonl or webhook)", i, sink.Type)
	}
	return nil
}

func looksLikeServerName(s string) bool {
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '-':
		default:
			return false
		}
	}
	return true
}

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}
