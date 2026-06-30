package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientCredentialsProvider_ClientSecret(t *testing.T) {
	var (
		receivedGrantType string
		receivedClientID  string
		receivedSecret    string
		receivedScope     string
	)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		receivedGrantType = r.Form.Get("grant_type")
		receivedClientID = r.Form.Get("client_id")
		receivedSecret = r.Form.Get("client_secret")
		receivedScope = r.Form.Get("scope")

		resp := map[string]any{
			"access_token": "test-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"scope":        "read write",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	p := NewClientCredentials(ClientCredentialsConfig{
		TokenEndpoint: ts.URL,
		ClientID:      "my-client",
		ClientSecret:  "my-secret",
		Scopes:        []string{"read", "write"},
	})

	tok, err := p.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}

	if tok.AccessToken != "test-access-token" {
		t.Errorf("got access_token %q, want %q", tok.AccessToken, "test-access-token")
	}
	if tok.TokenType != "Bearer" {
		t.Errorf("got token_type %q, want %q", tok.TokenType, "Bearer")
	}
	if receivedGrantType != "client_credentials" {
		t.Errorf("got grant_type %q, want %q", receivedGrantType, "client_credentials")
	}
	if receivedClientID != "my-client" {
		t.Errorf("got client_id %q, want %q", receivedClientID, "my-client")
	}
	if receivedSecret != "my-secret" {
		t.Errorf("got client_secret %q, want %q", receivedSecret, "my-secret")
	}
	if receivedScope != "read write" {
		t.Errorf("got scope %q, want %q", receivedScope, "read write")
	}
}

func TestClientCredentialsProvider_TokenEndpointError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
	}))
	defer ts.Close()

	p := NewClientCredentials(ClientCredentialsConfig{
		TokenEndpoint: ts.URL,
		ClientID:      "my-client",
		ClientSecret:  "my-secret",
	})

	_, err := p.Token(context.Background())
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}

func TestClientCredentialsProvider_JWTAssertion(t *testing.T) {
	var receivedGrantType string
	keyPEM := generateTestRSAKey(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		receivedGrantType = r.Form.Get("grant_type")

		resp := map[string]any{
			"access_token": "jwt-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	p := NewClientCredentials(ClientCredentialsConfig{
		TokenEndpoint: ts.URL,
		ClientID:      "my-client",
		PrivateKey:    keyPEM,
		Scopes:        []string{"read"},
	})

	tok, err := p.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}

	if tok.AccessToken != "jwt-token" {
		t.Errorf("got access_token %q, want %q", tok.AccessToken, "jwt-token")
	}
	if receivedGrantType != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		t.Errorf("got grant_type %q, want jwt-bearer", receivedGrantType)
	}
}
