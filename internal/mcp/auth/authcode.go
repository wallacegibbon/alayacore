package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AuthCodeConfig holds parameters for the authorization code flow.
//
//nolint:revive // stutter is acceptable for clarity
type AuthCodeConfig struct {
	ClientID     string
	ClientSecret string // optional, for confidential clients
	Scopes       []string
	Resource     string // RFC 8707 resource indicator (MCP server URL)
}

// RunAuthCodeFlow performs the complete OAuth 2.1 Authorization Code + PKCE flow.
// It:
//  1. Generates PKCE parameters
//  2. Starts a local HTTP server on 127.0.0.1 at a random port
//  3. Constructs the authorization URL and opens it in the browser
//  4. Waits for the authorization code callback
//  5. Exchanges the code for tokens at the token endpoint
//  6. Returns the obtained Token
//
// The ctx parameter controls the overall timeout (e.g., 5 minutes).
func RunAuthCodeFlow(ctx context.Context, meta *ASMetadata, cfg *AuthCodeConfig) (*Token, error) {
	// 1. Generate PKCE params.
	pkce, err := NewPKCE()
	if err != nil {
		return nil, fmt.Errorf("pkce: %w", err)
	}

	// 2. Generate random state for CSRF protection.
	state := randomState()

	// 3. Start local callback server on a random port.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("callback listener: %w", err)
	}
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return nil, fmt.Errorf("callback listener: unexpected addr type %T", listener.Addr())
	}
	port := addr.Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	// Channel to receive the authorization code or error from callback.
	type callbackResult struct {
		code string
		err  error
	}
	resultCh := make(chan callbackResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		returnedState := r.URL.Query().Get("state")
		errStr := r.URL.Query().Get("error")
		errDesc := r.URL.Query().Get("error_description")

		if errStr != "" {
			resultCh <- callbackResult{err: fmt.Errorf("authorization error: %s: %s", errStr, errDesc)}
			_, _ = w.Write([]byte("Authorization failed. You can close this window."))
			return
		}
		if returnedState != state {
			resultCh <- callbackResult{err: fmt.Errorf("state mismatch: got %q, expected %q", returnedState, state)}
			_, _ = w.Write([]byte("State validation failed. You can close this window."))
			return
		}
		if code == "" {
			resultCh <- callbackResult{err: fmt.Errorf("no authorization code in callback")}
			_, _ = w.Write([]byte("No authorization code received. You can close this window."))
			return
		}
		resultCh <- callbackResult{code: code}
		_, _ = w.Write([]byte("Authorization successful! You can close this window and return to the terminal."))
	})

	server := &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		_ = server.Serve(listener)
	}()
	defer server.Close()

	// 4. Construct authorization URL.
	authURL, err := buildAuthURL(meta, cfg, pkce, redirectURI, state)
	if err != nil {
		return nil, fmt.Errorf("build auth URL: %w", err)
	}

	// 5. Open browser.
	if err := OpenURL(authURL); err != nil {
		return nil, fmt.Errorf("open browser: %w", err)
	}

	// 6. Wait for callback.
	select {
	case res := <-resultCh:
		if res.err != nil {
			return nil, res.err
		}
		// Proceed to token exchange.
		return exchangeCode(ctx, meta, cfg, pkce, redirectURI, res.code)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// buildAuthURL constructs the authorization URL with all required parameters.
func buildAuthURL(meta *ASMetadata, cfg *AuthCodeConfig, pkce *PKCEParams, redirectURI, state string) (string, error) {
	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", cfg.ClientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("code_challenge", pkce.CodeChallenge)
	params.Set("code_challenge_method", pkce.Method)
	params.Set("state", state)

	if cfg.Resource != "" {
		params.Set("resource", cfg.Resource)
	}
	if len(cfg.Scopes) > 0 {
		params.Set("scope", strings.Join(cfg.Scopes, " "))
	}

	u, err := url.Parse(meta.AuthorizationEndpoint)
	if err != nil {
		return "", fmt.Errorf("parse authorization_endpoint: %w", err)
	}
	u.RawQuery = params.Encode()
	return u.String(), nil
}

// exchangeCode exchanges the authorization code for tokens.
func exchangeCode(ctx context.Context, meta *ASMetadata, cfg *AuthCodeConfig, pkce *PKCEParams, redirectURI, code string) (*Token, error) {
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", redirectURI)
	data.Set("code_verifier", pkce.CodeVerifier)
	data.Set("client_id", cfg.ClientID)
	if cfg.Resource != "" {
		data.Set("resource", cfg.Resource)
	}
	if cfg.ClientSecret != "" {
		data.Set("client_secret", cfg.ClientSecret)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", meta.TokenEndpoint,
		strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
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
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope,omitempty"`
		RefreshToken string `json:"refresh_token,omitempty"`
		Error        string `json:"error,omitempty"`
		ErrorDesc    string `json:"error_description,omitempty"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse token response: %w (body: %s)", err, string(body))
	}

	if tokenResp.Error != "" {
		return nil, fmt.Errorf("token endpoint error: %s: %s", tokenResp.Error, tokenResp.ErrorDesc)
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("token endpoint returned no access_token (body: %s)", string(body))
	}

	token := &Token{
		AccessToken: tokenResp.AccessToken,
		TokenType:   tokenResp.TokenType,
	}
	if tokenResp.ExpiresIn > 0 {
		token.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	}
	if tokenResp.RefreshToken != "" {
		token.RefreshToken = tokenResp.RefreshToken
	}
	if tokenResp.Scope != "" {
		token.Scopes = strings.Split(tokenResp.Scope, " ")
	}

	return token, nil
}

// randomState generates a random state string for CSRF protection.
func randomState() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf) // always succeeds per docs
	return hex.EncodeToString(buf)
}
