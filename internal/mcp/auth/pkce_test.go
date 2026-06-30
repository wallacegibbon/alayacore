package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestNewPKCE(t *testing.T) {
	p, err := NewPKCE()
	if err != nil {
		t.Fatalf("NewPKCE() error = %v", err)
	}

	if len(p.CodeVerifier) < 43 || len(p.CodeVerifier) > 128 {
		t.Errorf("code_verifier length %d, want 43-128", len(p.CodeVerifier))
	}

	if p.Method != "S256" {
		t.Errorf("method = %q, want S256", p.Method)
	}

	// Verify the challenge is correct S256 of the verifier.
	hash := sha256.Sum256([]byte(p.CodeVerifier))
	expected := base64.RawURLEncoding.EncodeToString(hash[:])
	if p.CodeChallenge != expected {
		t.Errorf("code_challenge mismatch:\n  got:  %q\n  want: %q", p.CodeChallenge, expected)
	}
}

func TestNewPKCE_Unique(t *testing.T) {
	p1, _ := NewPKCE()
	p2, _ := NewPKCE()
	if p1.CodeVerifier == p2.CodeVerifier {
		t.Error("two PKCE params should have different verifiers")
	}
}
