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

// CallbackResult holds the result of the OAuth callback HTTP request.
type CallbackResult struct {
	Code string
	Err  error
}

// StartCallbackServer starts a local HTTP server on 127.0.0.1 at a random port
// to receive the OAuth authorization code callback. It validates the state
// parameter to prevent CSRF attacks.
//
// Returns a channel that receives the callback result, a redirect URI for the
// authorization request, and a cleanup function that shuts down the server.
func StartCallbackServer(state string) (<-chan CallbackResult, string, func()) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		ch := make(chan CallbackResult, 1)
		ch <- CallbackResult{Err: fmt.Errorf("callback listener: %w", err)}
		close(ch)
		return ch, "", func() {}
	}

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		listener.Close()
		ch := make(chan CallbackResult, 1)
		ch <- CallbackResult{Err: fmt.Errorf("callback listener: unexpected addr type %T", listener.Addr())}
		close(ch)
		return ch, "", func() {}
	}
	port := addr.Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	resultCh := make(chan CallbackResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		returnedState := r.URL.Query().Get("state")
		errStr := r.URL.Query().Get("error")
		errDesc := r.URL.Query().Get("error_description")

		if errStr != "" {
			select {
			case resultCh <- CallbackResult{Err: fmt.Errorf("authorization error: %s: %s", errStr, errDesc)}:
			default:
			}
			_, _ = w.Write([]byte("Authorization failed. You can close this window."))
			return
		}
		if returnedState != state {
			select {
			case resultCh <- CallbackResult{Err: fmt.Errorf("state mismatch: got %q, expected %q", returnedState, state)}:
			default:
			}
			_, _ = w.Write([]byte("Authorization failed. You can close this window."))
			return
		}
		if code == "" {
			select {
			case resultCh <- CallbackResult{Err: fmt.Errorf("no authorization code in callback")}:
			default:
			}
			_, _ = w.Write([]byte("Authorization failed. You can close this window."))
			return
		}
		select {
		case resultCh <- CallbackResult{Code: code}:
		default:
		}
		_, _ = w.Write([]byte("Authorization successful! You can close this window."))
	})

	server := &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go server.Serve(listener) //nolint:errcheck // server is closed via cleanup

	return resultCh, redirectURI, func() { server.Close() }
}

// BuildAuthorizationURL constructs the authorization URL with all required parameters.
func BuildAuthorizationURL(meta *ASMetadata, cfg *AuthCodeConfig, pkce *PKCEParams, redirectURI, state string) (string, error) {
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

// ExchangeCode exchanges the authorization code for tokens at the token endpoint.
func ExchangeCode(ctx context.Context, meta *ASMetadata, cfg *AuthCodeConfig, pkce *PKCEParams, redirectURI, code string) (*Token, error) {
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

// RandomState generates a random hex string for OAuth state (CSRF protection).
func RandomState() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
