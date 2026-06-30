// Package auth implements OAuth 2.1 token acquisition for MCP clients.
//
// It supports two modes:
//   - Static: a pre-obtained token is provided directly in config
//   - Client Credentials: token obtained from an OAuth token endpoint
//     using either client_secret or JWT Bearer Assertion (RFC 7523)
package auth

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Sentinel errors for token provider.
var ErrNoTokenProvider = errors.New("token provider not initialized")

// Token represents an OAuth 2.0 access token with optional refresh token.
// Fields are JSON-tagged for serialization to disk.
type Token struct {
	AccessToken  string    `json:"access_token"`
	TokenType    string    `json:"token_type"` // typically "Bearer"
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	Scopes       []string  `json:"scopes,omitempty"`

	// Refresh metadata saved alongside the token so that automatic
	// refresh works across restarts without rediscovery.
	TokenEndpoint string `json:"token_endpoint,omitempty"`
	ClientID      string `json:"client_id,omitempty"`
}

// Valid returns true if the token is still valid (not expired).
// If ExpiresAt is zero (not set), the token is considered non-expiring
// and Valid() returns true as long as AccessToken is non-empty.
func (t *Token) Valid() bool {
	if t == nil || t.AccessToken == "" {
		return false
	}
	if t.ExpiresAt.IsZero() {
		return true // no expiration known
	}
	return time.Now().Before(t.ExpiresAt)
}

// TokenProvider is the interface for obtaining OAuth tokens.
type TokenProvider interface {
	// Token returns a valid access token, refreshing or acquiring
	// a new one if the cached token is expired or missing.
	Token(ctx context.Context) (*Token, error)
}

// cachedProvider wraps a TokenProvider with thread-safe caching.
// The cached token is returned if still valid; otherwise the
// underlying provider is called to obtain a fresh token.
type cachedProvider struct {
	inner TokenProvider
	mu    sync.Mutex
	token *Token
}

// NewCached wraps a TokenProvider with read-through caching.
func NewCached(inner TokenProvider) TokenProvider {
	return &cachedProvider{inner: inner}
}

func (p *cachedProvider) Token(ctx context.Context) (*Token, error) {
	if p.inner == nil {
		return nil, ErrNoTokenProvider
	}

	p.mu.Lock()
	if p.token != nil && p.token.Valid() {
		t := *p.token
		p.mu.Unlock()
		return &t, nil
	}
	p.mu.Unlock()

	tok, err := p.inner.Token(ctx)
	if err != nil {
		return nil, err
	}
	if tok == nil {
		return nil, errors.New("token provider returned nil token without error")
	}
	if tok.AccessToken == "" {
		return nil, errors.New("token provider returned empty access token")
	}

	p.mu.Lock()
	p.token = tok
	p.mu.Unlock()

	cp := *tok
	return &cp, nil
}

// StaticProvider returns a fixed token from config.
// It implements TokenProvider by always returning the same token.
type StaticProvider struct {
	TokenValue *Token
}

func (p *StaticProvider) Token(_ context.Context) (*Token, error) {
	if p.TokenValue == nil || p.TokenValue.AccessToken == "" {
		return nil, nil
	}
	cp := *p.TokenValue
	return &cp, nil
}
