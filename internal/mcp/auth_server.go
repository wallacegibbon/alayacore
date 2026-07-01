package mcp

import (
	"context"
	"fmt"

	"github.com/alayacore/alayacore/internal/mcp/auth"
)

// ServerAuth encapsulates the OAuth lifecycle for a single MCP server.
// Each server gets its own ServerAuth instance with independent state,
// allowing parallel execution without shared state conflicts.
type ServerAuth struct {
	client *Client

	// Result populated by Run().
	token *auth.Token
}

// NewServerAuth creates a ServerAuth for the given client.
// The client must have Auth.Type == AuthTypeAuthorizationCode and no
// valid persisted token (i.e. needsPersistedAuth() returned true).
func NewServerAuth(client *Client) *ServerAuth {
	return &ServerAuth{client: client}
}

// Name returns the server name.
func (s *ServerAuth) Name() string { return s.client.config.Name }

// URL returns the server URL.
func (s *ServerAuth) URL() string { return s.client.config.URL }

// Run executes the complete OAuth flow for this server:
//  1. Discover authorization server metadata
//  2. Run authorization code flow (browser + callback)
//  3. Persist obtained token
//  4. Reconnect client with the token
//  5. Discover tools
//
// Thread-safe: no shared state with other ServerAuth instances.
func (s *ServerAuth) Run(ctx context.Context) ([]Tool, error) {
	cfg := s.client.config.Auth

	// 1. Discover authorization server metadata and resolve client credentials.
	meta, clientID, err := resolveAuthConfig(ctx, cfg, s.client.config.URL)
	if err != nil {
		return nil, fmt.Errorf("mcp server %q: %w", s.client.config.Name, err)
	}

	cfg.TokenEndpoint = meta.TokenEndpoint
	cfg.ClientID = clientID

	// 2. Run authorization code flow (browser, callback, token exchange).
	oauthToken, err := auth.RunAuthCodeFlow(ctx, meta, &auth.AuthCodeConfig{
		ClientID:     clientID,
		ClientSecret: cfg.ClientSecret,
		Scopes:       cfg.Scopes,
		Resource:     s.client.config.URL,
	})
	if err != nil {
		return nil, fmt.Errorf("mcp server %q: auth code flow: %w", s.client.config.Name, err)
	}

	if oauthToken.AccessToken == "" {
		return nil, fmt.Errorf("mcp server %q: OAuth returned empty access token", s.client.config.Name)
	}

	// 3. Store the obtained token.
	s.token = &auth.Token{
		AccessToken:   oauthToken.AccessToken,
		TokenType:     oauthToken.TokenType,
		RefreshToken:  oauthToken.RefreshToken,
		ExpiresAt:     oauthToken.ExpiresAt,
		Scopes:        oauthToken.Scopes,
		TokenEndpoint: meta.TokenEndpoint,
		ClientID:      clientID,
	}

	// Persist to disk so it survives restarts.
	if s.client.tokenStore != nil {
		_ = s.client.tokenStore.SaveToken(s.client.config.Name, s.token) // non-fatal
	}

	// Set on config so the auth provider picks it up on reconnect.
	cfg.obtainedToken = s.token

	// 4. Reconnect client (first attempt returned ErrNeedsAuth).
	s.client.resetState()
	if err := s.client.Connect(ctx); err != nil {
		cfg.obtainedToken = nil
		return nil, fmt.Errorf("mcp server %q: connect after auth: %w", s.client.config.Name, err)
	}

	// 5. Discover tools.
	if !s.client.HasTools() {
		return nil, nil
	}
	return s.client.ListTools(ctx)
}
