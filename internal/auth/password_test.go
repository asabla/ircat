package auth

import (
	"strings"
	"testing"
)

func TestArgon2id_HashVerifyRoundTrip(t *testing.T) {
	hash, err := Hash(AlgorithmArgon2id, "correct horse battery staple", Argon2idParams{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("hash prefix wrong: %q", hash)
	}
	ok, err := Verify(hash, "correct horse battery staple")
	if err != nil || !ok {
		t.Errorf("Verify match: ok=%v err=%v", ok, err)
	}
	ok, err = Verify(hash, "wrong password")
	if err != nil || ok {
		t.Errorf("Verify mismatch: ok=%v err=%v", ok, err)
	}
}

func TestArgon2id_DifferentPasswordsDifferentHashes(t *testing.T) {
	a, _ := Hash(AlgorithmArgon2id, "alpha", Argon2idParams{})
	b, _ := Hash(AlgorithmArgon2id, "beta", Argon2idParams{})
	if a == b {
		t.Error("expected different hashes")
	}
}

func TestArgon2id_SaltMakesEachHashUnique(t *testing.T) {
	a, _ := Hash(AlgorithmArgon2id, "same", Argon2idParams{})
	b, _ := Hash(AlgorithmArgon2id, "same", Argon2idParams{})
	if a == b {
		t.Error("two hashes of the same password should differ (salt)")
	}
}

func TestBcrypt_HashVerifyRoundTrip(t *testing.T) {
	hash, err := Hash(AlgorithmBcrypt, "hunter2", Argon2idParams{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$2a$") && !strings.HasPrefix(hash, "$2b$") {
		t.Errorf("bcrypt prefix wrong: %q", hash)
	}
	ok, err := Verify(hash, "hunter2")
	if err != nil || !ok {
		t.Errorf("ok=%v err=%v", ok, err)
	}
	ok, err = Verify(hash, "hunter3")
	if err != nil || ok {
		t.Errorf("mismatch ok=%v err=%v", ok, err)
	}
}

func TestVerify_UnknownAlgorithm(t *testing.T) {
	_, err := Verify("plaintext-password", "anything")
	if err != ErrUnknownAlgorithm {
		t.Errorf("err = %v, want ErrUnknownAlgorithm", err)
	}
}

func TestHash_UnknownAlgorithm(t *testing.T) {
	_, err := Hash("rot13", "x", Argon2idParams{})
	if err == nil {
		t.Error("expected error")
	}
}

func TestArgon2id_GarbledHashRejected(t *testing.T) {
	ok, err := Verify("$argon2id$broken", "x")
	if err != nil || ok {
		t.Errorf("ok=%v err=%v", ok, err)
	}
}
