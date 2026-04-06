// Package auth handles password hashing and verification for ircat.
//
// Two algorithms are supported, selected by config:
//
//   - argon2id (default): the modern winner of the Password Hashing
//     Competition. Memory-hard, tunable parameters per
//     [config.Argon2idParams].
//   - bcrypt: the boring battle-tested option, for operators who
//     have an existing bcrypt corpus they want to keep.
//
// All public functions take and return strings rather than byte
// slices so callers can compare hashes directly. The encoded form
// is the standard PHC string format for argon2id ($argon2id$...) or
// the bcrypt $2a$/$2b$ form, so a hash record carries enough
// information to verify itself without consulting the config.
//
// We never panic on a malformed hash; verification just returns
// false. The only error path is "I do not know how to verify this
// algorithm".
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

// ErrUnknownAlgorithm is returned by [Verify] when the supplied
// hash does not start with a recognized algorithm prefix.
var ErrUnknownAlgorithm = errors.New("auth: unknown password algorithm")

// Algorithm names accepted by [Hash].
const (
	AlgorithmArgon2id = "argon2id"
	AlgorithmBcrypt   = "bcrypt"
)

// Argon2idParams configures the argon2id KDF. Defaults from
// [DefaultArgon2idParams] match the OWASP cheat-sheet for
// "interactive login" tier.
type Argon2idParams struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

// DefaultArgon2idParams returns conservative defaults that take
// roughly 50 ms on a modern CPU.
func DefaultArgon2idParams() Argon2idParams {
	return Argon2idParams{
		MemoryKiB:   64 * 1024,
		Iterations:  3,
		Parallelism: 2,
		SaltLength:  16,
		KeyLength:   32,
	}
}

// withArgon2idDefaults fills any zero field on p with the matching
// value from [DefaultArgon2idParams]. Callers supplying only the
// cost parameters (memory, iterations, parallelism) get safe salt
// and key lengths automatically.
func withArgon2idDefaults(p Argon2idParams) Argon2idParams {
	d := DefaultArgon2idParams()
	if p.MemoryKiB == 0 {
		p.MemoryKiB = d.MemoryKiB
	}
	if p.Iterations == 0 {
		p.Iterations = d.Iterations
	}
	if p.Parallelism == 0 {
		p.Parallelism = d.Parallelism
	}
	if p.SaltLength == 0 {
		p.SaltLength = d.SaltLength
	}
	if p.KeyLength == 0 {
		p.KeyLength = d.KeyLength
	}
	return p
}

// Hash produces an encoded hash for password using algorithm.
// algorithm is one of "argon2id" or "bcrypt". The argon2id branch
// fills in any zero field on params with the [DefaultArgon2idParams]
// value, so callers can supply just the cost parameters they care
// about (memory, iterations, parallelism) and let the salt and key
// lengths default.
func Hash(algorithm, password string, params Argon2idParams) (string, error) {
	switch algorithm {
	case AlgorithmArgon2id:
		params = withArgon2idDefaults(params)
		return hashArgon2id(password, params)
	case AlgorithmBcrypt:
		out, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return "", fmt.Errorf("bcrypt: %w", err)
		}
		return string(out), nil
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownAlgorithm, algorithm)
}

// Verify checks password against an encoded hash. Returns true on
// match, false on mismatch, and an error only when the algorithm
// itself is unknown.
func Verify(encoded, password string) (bool, error) {
	switch {
	case strings.HasPrefix(encoded, "$argon2id$"):
		return verifyArgon2id(encoded, password)
	case strings.HasPrefix(encoded, "$2a$"),
		strings.HasPrefix(encoded, "$2b$"),
		strings.HasPrefix(encoded, "$2y$"):
		return bcrypt.CompareHashAndPassword([]byte(encoded), []byte(password)) == nil, nil
	}
	return false, ErrUnknownAlgorithm
}

func hashArgon2id(password string, p Argon2idParams) (string, error) {
	salt := make([]byte, p.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("argon2id salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.Iterations, p.MemoryKiB, p.Parallelism, p.KeyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		p.MemoryKiB,
		p.Iterations,
		p.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func verifyArgon2id(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	// Expected: ["", "argon2id", "v=19", "m=...,t=...,p=...", salt, key]
	if len(parts) != 6 {
		return false, nil
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, nil
	}
	var memory uint32
	var iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false, nil
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, nil
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, nil
	}
	got := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
