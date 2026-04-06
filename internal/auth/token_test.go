package auth

import (
	"strings"
	"testing"
)

func TestGenerateAPIToken_Unique(t *testing.T) {
	a, err := GenerateAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	b, err := GenerateAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	if a.Plaintext == b.Plaintext {
		t.Errorf("two minted tokens are identical: %q", a.Plaintext)
	}
	if a.ID == b.ID {
		t.Errorf("two minted ids are identical: %q", a.ID)
	}
}

func TestGenerateAPIToken_FormatAndHash(t *testing.T) {
	tok, err := GenerateAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok.Plaintext, "ircat_") {
		t.Errorf("plaintext missing prefix: %q", tok.Plaintext)
	}
	if !strings.HasPrefix(tok.ID, "ircat_") {
		t.Errorf("id missing prefix: %q", tok.ID)
	}
	if !strings.HasPrefix(tok.Plaintext, tok.ID+"_") {
		t.Errorf("plaintext does not start with id+_: %q vs %q", tok.Plaintext, tok.ID)
	}
	if len(tok.Hash) != 64 {
		t.Errorf("hash len = %d, want 64", len(tok.Hash))
	}
	if tok.Hash != HashAPIToken(tok.Plaintext) {
		t.Errorf("hash mismatch")
	}
}

func TestVerifyAPIToken(t *testing.T) {
	tok, err := GenerateAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyAPIToken(tok.Hash, tok.Plaintext) {
		t.Errorf("matching token did not verify")
	}
	if VerifyAPIToken(tok.Hash, tok.Plaintext+"x") {
		t.Errorf("modified token verified")
	}
	if VerifyAPIToken("not-a-hash", tok.Plaintext) {
		t.Errorf("garbage hash verified")
	}
}
