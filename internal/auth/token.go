package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
)

// API token format and sizes.
//
// Tokens are issued as `ircat_<24-char-id>_<32-char-secret>`. The
// id half is the storage primary key (visible in audit logs and the
// dashboard); the secret half is what the client presents in the
// Authorization header. The store keeps only the sha256 hash of the
// (id || secret) pair so a database leak does not yield usable
// tokens.
const (
	tokenPrefix    = "ircat_"
	tokenIDBytes   = 12 // hex-encodes to 24 chars
	tokenSecretLen = 16 // hex-encodes to 32 chars
)

// MintedToken is the result of [GenerateAPIToken]. The Plaintext is
// shown to the operator exactly once at creation; the ID and Hash
// are what land in the database.
type MintedToken struct {
	ID        string // the public id half, e.g. "ircat_aabb...:" (without the secret)
	Plaintext string // the full token to hand to the operator
	Hash      string // sha256 hex of Plaintext, ready for the store
}

// GenerateAPIToken returns a freshly minted token. The full
// plaintext form is "ircat_<id>_<secret>"; the ID and Hash are
// derived from it. Callers should hand Plaintext to the operator
// exactly once and persist (ID, Hash) only.
func GenerateAPIToken() (*MintedToken, error) {
	idBytes := make([]byte, tokenIDBytes)
	if _, err := rand.Read(idBytes); err != nil {
		return nil, fmt.Errorf("token id: %w", err)
	}
	secretBytes := make([]byte, tokenSecretLen)
	if _, err := rand.Read(secretBytes); err != nil {
		return nil, fmt.Errorf("token secret: %w", err)
	}
	id := tokenPrefix + hex.EncodeToString(idBytes)
	plaintext := id + "_" + hex.EncodeToString(secretBytes)
	return &MintedToken{
		ID:        id,
		Plaintext: plaintext,
		Hash:      HashAPIToken(plaintext),
	}, nil
}

// HashAPIToken returns the sha256 hex digest of plaintext, the form
// stored alongside an [storage.APIToken] record.
func HashAPIToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// VerifyAPIToken reports whether plaintext matches the stored hash.
// Comparison is constant-time.
func VerifyAPIToken(storedHash, plaintext string) bool {
	got := HashAPIToken(plaintext)
	return subtle.ConstantTimeCompare([]byte(got), []byte(storedHash)) == 1
}
