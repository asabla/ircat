package config

import "fmt"

// resolveEnv copies env-var-backed secret fields into their
// corresponding direct fields. Each *_env field is paired with a
// non-env counterpart; if the env field is set, the value of the
// referenced environment variable replaces whatever was in the
// direct field.
//
// The lookup func is parameterized for testability.
func (c *Config) resolveEnv(lookup func(string) string) error {
	pull := func(directField *string, envName, fieldPath string) error {
		if envName == "" {
			return nil
		}
		v := lookup(envName)
		if v == "" {
			return fmt.Errorf("%w: %s references env %q which is empty", ErrInvalid, fieldPath, envName)
		}
		*directField = v
		return nil
	}

	if err := pull(&c.Storage.Postgres.DSN, c.Storage.Postgres.DSNEnv, "storage.postgres.dsn_env"); err != nil {
		return err
	}
	if err := pull(&c.Auth.InitialAdmin.Password, c.Auth.InitialAdmin.PasswordEnv, "auth.initial_admin.password_env"); err != nil {
		return err
	}
	for i := range c.Events.Sinks {
		s := &c.Events.Sinks[i]
		if err := pull(&s.Secret, s.SecretEnv, fmt.Sprintf("events.sinks[%d].secret_env", i)); err != nil {
			return err
		}
	}
	for i := range c.Federation.Links {
		l := &c.Federation.Links[i]
		if err := pull(&l.PasswordIn, l.PasswordInEnv, fmt.Sprintf("federation.links[%d].password_in_env", i)); err != nil {
			return err
		}
		if err := pull(&l.PasswordOut, l.PasswordOutEnv, fmt.Sprintf("federation.links[%d].password_out_env", i)); err != nil {
			return err
		}
	}
	for i := range c.Operators {
		op := &c.Operators[i]
		if err := pull(&op.PasswordHash, op.PasswordHashEnv, fmt.Sprintf("operators[%d].password_hash_env", i)); err != nil {
			return err
		}
	}
	return nil
}
