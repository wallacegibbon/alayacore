package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWellKnownURLs(t *testing.T) {
	urls := wellKnownURLs("https://auth.example.com")
	if len(urls) != 2 {
		t.Fatalf("expected 2 URLs, got %d", len(urls))
	}
	if urls[0] != "https://auth.example.com/.well-known/oauth-authorization-server" {
		t.Errorf("urls[0] = %q", urls[0])
	}
	if urls[1] != "https://auth.example.com/.well-known/openid-configuration" {
		t.Errorf("urls[1] = %q", urls[1])
	}
}

func TestDiscoverASMetadata_OAuth(t *testing.T) {
	meta := ASMetadata{
		Issuer:                        "https://auth.example.com",
		AuthorizationEndpoint:         "https://auth.example.com/authorize",
		TokenEndpoint:                 "https://auth.example.com/token",
		CodeChallengeMethodsSupported: []string{"S256"},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-authorization-server" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(meta)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	result, err := DiscoverASMetadata(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("DiscoverASMetadata() error = %v", err)
	}

	if result.Issuer != meta.Issuer {
		t.Errorf("issuer = %q, want %q", result.Issuer, meta.Issuer)
	}
	if result.AuthorizationEndpoint != meta.AuthorizationEndpoint {
		t.Errorf("auth endpoint = %q, want %q", result.AuthorizationEndpoint, meta.AuthorizationEndpoint)
	}
	if result.TokenEndpoint != meta.TokenEndpoint {
		t.Errorf("token endpoint = %q, want %q", result.TokenEndpoint, meta.TokenEndpoint)
	}
}

func TestDiscoverASMetadata_NotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	_, err := DiscoverASMetadata(context.Background(), ts.URL)
	if err == nil {
		t.Fatal("expected error for 404")
	}
}

func TestDiscoverASMetadata_Incomplete(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"issuer":"https://example.com"}`))
	}))
	defer ts.Close()

	_, err := DiscoverASMetadata(context.Background(), ts.URL)
	if err == nil {
		t.Fatal("expected error for incomplete metadata")
	}
}
