package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// PKCEParams holds the code verifier and challenge for PKCE (RFC 7636).
type PKCEParams struct {
	CodeVerifier  string
	CodeChallenge string
	Method        string // always "S256"
}

// NewPKCE generates PKCE parameters using S256 method.
// code_verifier: 43-128 characters from unreserved character set.
// code_challenge: Base64URL-encoded SHA-256 hash of verifier.
func NewPKCE() (*PKCEParams, error) {
	// Generate 32 bytes → 43 base64url characters (within 43-128 range)
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("pkce random: %w", err)
	}

	verifier := base64.RawURLEncoding.EncodeToString(buf)
	hash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])

	return &PKCEParams{
		CodeVerifier:  verifier,
		CodeChallenge: challenge,
		Method:        "S256",
	}, nil
}
