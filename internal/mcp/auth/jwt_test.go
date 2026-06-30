package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
)

// generateTestRSAKey returns a PEM-encoded RSA private key for testing.
func generateTestRSAKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	block := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}
	return string(pem.EncodeToMemory(block))
}

// generateTestECDSAKey returns a PEM-encoded ECDSA P-256 private key for testing.
func generateTestECDSAKey(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: block}))
}

func TestCreateAssertion_RSA(t *testing.T) {
	keyPEM := generateTestRSAKey(t)
	assertion, err := CreateAssertion("my-client", "https://auth.example.com/token", keyPEM, 300)
	if err != nil {
		t.Fatalf("CreateAssertion failed: %v", err)
	}

	parts := strings.Split(assertion, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts, got %d", len(parts))
	}
	if parts[0] == "" || parts[1] == "" || parts[2] == "" {
		t.Fatal("JWT parts should not be empty")
	}
}

func TestCreateAssertion_ECDSA(t *testing.T) {
	keyPEM := generateTestECDSAKey(t)
	assertion, err := CreateAssertion("my-client", "https://auth.example.com/token", keyPEM, 300)
	if err != nil {
		t.Fatalf("CreateAssertion failed: %v", err)
	}

	parts := strings.Split(assertion, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts, got %d", len(parts))
	}
}

func TestCreateAssertion_InvalidKey(t *testing.T) {
	_, err := CreateAssertion("c", "https://example.com/token", "invalid-key", 300)
	if err == nil {
		t.Fatal("expected error for invalid key")
	}
}

func TestCreateAssertion_EmptyKey(t *testing.T) {
	_, err := CreateAssertion("c", "https://example.com/token", "", 300)
	if err == nil {
		t.Fatal("expected error for empty key")
	}
}
