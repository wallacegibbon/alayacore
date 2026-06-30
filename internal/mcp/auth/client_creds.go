package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ClientCredentialsConfig holds the configuration for the OAuth 2.0
// Client Credentials flow (RFC 6749 Section 4.4).
type ClientCredentialsConfig struct {
	TokenEndpoint string   // OAuth token endpoint URL
	ClientID      string   // OAuth client identifier
	ClientSecret  string   // Client secret (mutually exclusive with PrivateKey)
	PrivateKey    string   // PEM-encoded private key for JWT assertion (RFC 7523)
	Scopes        []string // Requested scopes
}

// ClientCredentialsProvider implements TokenProvider using the
// OAuth 2.0 Client Credentials flow.
type ClientCredentialsProvider struct {
	config ClientCredentialsConfig
	client *http.Client
}

// NewClientCredentials creates a new ClientCredentialsProvider.
func NewClientCredentials(cfg ClientCredentialsConfig) *ClientCredentialsProvider {
	return &ClientCredentialsProvider{
		config: cfg,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// Token obtains a fresh access token from the token endpoint.
func (p *ClientCredentialsProvider) Token(ctx context.Context) (*Token, error) {
	data := url.Values{}

	if p.config.PrivateKey != "" {
		assertion, err := CreateAssertion(p.config.ClientID, p.config.TokenEndpoint, p.config.PrivateKey, 300)
		if err != nil {
			return nil, fmt.Errorf("create JWT assertion: %w", err)
		}
		data.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
		data.Set("assertion", assertion)
	} else {
		data.Set("grant_type", "client_credentials")
		data.Set("client_id", p.config.ClientID)
		if p.config.ClientSecret != "" {
			data.Set("client_secret", p.config.ClientSecret)
		}
	}

	if len(p.config.Scopes) > 0 {
		data.Set("scope", strings.Join(p.config.Scopes, " "))
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.config.TokenEndpoint,
		strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
		Scope       string `json:"scope,omitempty"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}

	token := &Token{
		AccessToken: tokenResp.AccessToken,
		TokenType:   tokenResp.TokenType,
	}
	if tokenResp.ExpiresIn > 0 {
		token.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	}
	if tokenResp.Scope != "" {
		token.Scopes = strings.Split(tokenResp.Scope, " ")
	}

	return token, nil
}
