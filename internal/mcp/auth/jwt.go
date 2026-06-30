package auth

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"time"
)

// JWT algorithm constants.
const (
	jwtAlgRS256 = "RS256"
	jwtAlgES256 = "ES256"
	jwtAlgES384 = "ES384"
	jwtAlgES512 = "ES512"
	jwtAlgEdDSA = "EdDSA"
)

// jwtSign creates a signed JWT assertion for use with the
// JWT Bearer Assertion grant type (RFC 7523).
//
// It auto-detects the signing algorithm from the private key type:
//   - RSA (PKCS1 or PKCS8) → RS256
//   - ECDSA (P-256)        → ES256
//   - Ed25519              → EdDSA
func jwtSign(keyPEM string, claims jwtClaims) (string, error) {
	key, alg, err := parsePrivateKey(keyPEM)
	if err != nil {
		return "", err
	}

	header := jwtHeader{Alg: alg, Typ: "JWT"}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("marshal header: %w", err)
	}

	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal claims: %w", err)
	}

	encodedHeader := base64.RawURLEncoding.EncodeToString(headerJSON)
	encodedClaims := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := encodedHeader + "." + encodedClaims

	sig, err := signJWT(key, []byte(signingInput))
	if err != nil {
		return "", fmt.Errorf("sign jwt: %w", err)
	}

	encodedSig := base64.RawURLEncoding.EncodeToString(sig)
	return signingInput + "." + encodedSig, nil
}

// parsePrivateKey decodes a PEM-encoded private key and determines the JWT algorithm.
func parsePrivateKey(keyPEM string) (crypto.Signer, string, error) {
	block, _ := pem.Decode([]byte(keyPEM))
	if block == nil {
		return nil, "", fmt.Errorf("no PEM block found in private key")
	}

	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, "", fmt.Errorf("parse RSA key: %w", err)
		}
		return key, jwtAlgRS256, nil

	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, "", fmt.Errorf("parse PKCS8 key: %w", err)
		}
		signer, ok := k.(crypto.Signer)
		if !ok {
			return nil, "", fmt.Errorf("PKCS8 key does not implement crypto.Signer")
		}
		return signer, jwtAlg(signer), nil

	case "EC PRIVATE KEY":
		key, err := x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			return nil, "", fmt.Errorf("parse EC key: %w", err)
		}
		return key, jwtAlg(key), nil

	default:
		return nil, "", fmt.Errorf("unsupported PEM block type: %s", block.Type)
	}
}

// signJWT signs the message with the appropriate algorithm.
// For RSA/ECDSA: hashes with SHA-256 first, then signs.
// For Ed25519: signs directly (no separate hash step).
func signJWT(key crypto.Signer, msg []byte) ([]byte, error) {
	switch key.(type) {
	case ed25519.PrivateKey:
		return key.Sign(rand.Reader, msg, crypto.Hash(0))
	default:
		// RSA and ECDSA: hash first, then sign with SHA-256
		hash := sha256.Sum256(msg)
		return key.Sign(rand.Reader, hash[:], crypto.SHA256)
	}
}

// jwtHeader is the JSON Web Token header.
type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// jwtClaims represents the standard claims for a JWT Bearer Assertion.
type jwtClaims struct {
	Iss string `json:"iss"` // Client ID
	Sub string `json:"sub"` // Subject (usually same as iss)
	Aud string `json:"aud"` // Token endpoint URL
	Exp int64  `json:"exp"` // Expiration time (Unix timestamp)
	Iat int64  `json:"iat"` // Issued at (Unix timestamp)
}

// jwtAlg returns the JWT algorithm name for the given private key.
func jwtAlg(key crypto.Signer) string {
	switch k := key.(type) {
	case *rsa.PrivateKey:
		return jwtAlgRS256
	case *ecdsa.PrivateKey:
		switch k.Curve.Params().Name {
		case "P-256":
			return jwtAlgES256
		case "P-384":
			return jwtAlgES384
		case "P-521":
			return jwtAlgES512
		}
	case ed25519.PrivateKey:
		return jwtAlgEdDSA
	}
	return jwtAlgRS256 // fallback
}

// CreateAssertion builds and signs a JWT assertion for the client
// credentials JWT Bearer Assertion flow.
//
// The assertion is valid for lifetime seconds from now.
func CreateAssertion(clientID, tokenEndpoint, keyPEM string, lifetimeSeconds int64) (string, error) {
	now := time.Now()
	claims := jwtClaims{
		Iss: clientID,
		Sub: clientID,
		Aud: tokenEndpoint,
		Exp: now.Unix() + lifetimeSeconds,
		Iat: now.Unix(),
	}
	return jwtSign(keyPEM, claims)
}
