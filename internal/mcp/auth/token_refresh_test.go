package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// Test that PersistentTokenProvider uses refresh token when access token is expired.
func TestPersistentTokenProvider_Refresh(t *testing.T) {
	var refreshCalled int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			atomic.AddInt32(&refreshCalled, 1)

			// Return a new token with new refresh token (rotation).
			resp := map[string]interface{}{
				"access_token":  "refreshed-access-token",
				"token_type":    "Bearer",
				"expires_in":    3600,
				"refresh_token": "new-refresh-token",
				"scope":         "read write",
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
	defer ts.Close()

	dir := t.TempDir()
	store := NewFileTokenStore(dir)

	// Create provider with an expired token that has a refresh token.
	inner := &StaticProvider{
		TokenValue: &Token{
			AccessToken:  "expired-token",
			TokenType:    "Bearer",
			RefreshToken: "old-refresh-token",
			ExpiresAt:    time.Now().Add(-1 * time.Hour), // expired
			Scopes:       []string{"read"},
		},
	}

	pp := NewPersistentTokenProvider(inner, store, "test-server", &RefreshConfig{
		TokenEndpoint: ts.URL + "/token",
		ClientID:      "test-client",
		ClientSecret:  "test-secret",
	})

	// Token() should trigger refresh because cached token is expired.
	tok, err := pp.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	if tok.AccessToken != "refreshed-access-token" {
		t.Errorf("AccessToken = %q, want %q", tok.AccessToken, "refreshed-access-token")
	}
	if tok.RefreshToken != "new-refresh-token" {
		t.Errorf("RefreshToken = %q, want %q", tok.RefreshToken, "new-refresh-token")
	}

	if atomic.LoadInt32(&refreshCalled) != 1 {
		t.Errorf("refresh endpoint called %d times, want 1", refreshCalled)
	}

	// Token should be persisted.
	loaded, err := store.LoadToken("test-server")
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil {
		t.Fatal("token not persisted")
	}
	if loaded.AccessToken != "refreshed-access-token" { //nolint:staticcheck // checked nil above
		t.Errorf("persisted AccessToken = %q", loaded.AccessToken)
	}
}

// Test that PersistentTokenProvider falls back to inner provider when
// there's no refresh token and the cached token is expired.
func TestPersistentTokenProvider_FallbackToInner(t *testing.T) {
	dir := t.TempDir()
	store := NewFileTokenStore(dir)

	innerCalls := 0
	inner := &callCountingProvider{fn: func() (*Token, error) {
		innerCalls++
		return &Token{
			AccessToken: "fresh-from-inner",
			TokenType:   "Bearer",
			ExpiresAt:   time.Now().Add(1 * time.Hour),
		}, nil
	}}

	pp := NewPersistentTokenProvider(inner, store, "test-server", nil)

	// First call — no cache, falls through to inner.
	tok, err := pp.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	if tok.AccessToken != "fresh-from-inner" {
		t.Errorf("got %q, want %q", tok.AccessToken, "fresh-from-inner")
	}
	if innerCalls != 1 {
		t.Errorf("inner called %d times, want 1", innerCalls)
	}

	// Second call — should use cache.
	_, err = pp.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if innerCalls != 1 {
		t.Errorf("inner called %d times after cache, want 1", innerCalls)
	}

	// Token should be persisted.
	loaded, err := store.LoadToken("test-server")
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil {
		t.Fatal("token not persisted")
	}
}

// Test that PersistentTokenProvider loads persisted token on startup.
func TestPersistentTokenProvider_LoadPersisted(t *testing.T) {
	dir := t.TempDir()
	store := NewFileTokenStore(dir)

	// Save a token to disk first.
	savedToken := &Token{
		AccessToken:  "persisted-token",
		TokenType:    "Bearer",
		RefreshToken: "persisted-refresh",
		ExpiresAt:    time.Now().Add(1 * time.Hour),
	}
	if err := store.SaveToken("test-server", savedToken); err != nil {
		t.Fatal(err)
	}

	// Create a provider with nil inner — should load from disk.
	inner := &callCountingProvider{fn: func() (*Token, error) {
		return &Token{AccessToken: "should-not-be-called", TokenType: "Bearer", ExpiresAt: time.Now().Add(1 * time.Hour)}, nil
	}}

	pp := NewPersistentTokenProvider(inner, store, "test-server", nil)

	// Token() should return the persisted token without calling inner.
	tok, err := pp.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	if tok.AccessToken != "persisted-token" {
		t.Errorf("got %q, want %q", tok.AccessToken, "persisted-token")
	}
}

// Test that InvalidateToken clears cache and deletes persisted token.
func TestPersistentTokenProvider_Invalidate(t *testing.T) {
	dir := t.TempDir()
	store := NewFileTokenStore(dir)

	inner := &StaticProvider{
		TokenValue: &Token{
			AccessToken: "test-token",
			TokenType:   "Bearer",
			ExpiresAt:   time.Now().Add(1 * time.Hour),
		},
	}

	pp := NewPersistentTokenProvider(inner, store, "test-server", nil)

	// Get token to populate cache and persist.
	_, _ = pp.Token(context.Background())

	// Verify persisted.
	loaded, _ := store.LoadToken("test-server")
	if loaded == nil {
		t.Fatal("expected persisted token")
	}

	// Invalidate.
	if err := pp.InvalidateToken(context.Background()); err != nil {
		t.Fatalf("InvalidateToken() error = %v", err)
	}

	// Verify deleted from disk.
	loaded, _ = store.LoadToken("test-server")
	if loaded != nil {
		t.Error("expected nil after invalidation")
	}

	// Next call should get fresh token from inner.
	tok, err := pp.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "test-token" {
		t.Errorf("got %q, want %q", tok.AccessToken, "test-token")
	}
}

// Test refresh with no refresh token configured — should fall through to inner.
func TestPersistentTokenProvider_NoRefreshToken(t *testing.T) {
	dir := t.TempDir()
	store := NewFileTokenStore(dir)

	inner := &StaticProvider{
		TokenValue: &Token{
			AccessToken: "no-refresh-token",
			TokenType:   "Bearer",
			ExpiresAt:   time.Now().Add(-1 * time.Hour), // expired
			// No RefreshToken.
		},
	}

	pp := NewPersistentTokenProvider(inner, store, "test-server", &RefreshConfig{
		TokenEndpoint: "https://auth.example.com/token",
		ClientID:      "client",
	})

	// Token is expired with no refresh — should fall through to inner.
	tok, err := pp.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	if tok.AccessToken != "no-refresh-token" {
		t.Errorf("got %q, want %q", tok.AccessToken, "no-refresh-token")
	}
}

// Test that Token() returns expired token if no inner provider and no refresh.
func TestPersistentTokenProvider_ReturnsExpiredIfNoFallback(t *testing.T) {
	dir := t.TempDir()
	store := NewFileTokenStore(dir)

	// Save an expired token with no refresh token.
	saved := &Token{
		AccessToken: "expired-no-refresh",
		TokenType:   "Bearer",
		ExpiresAt:   time.Now().Add(-1 * time.Hour),
		// No RefreshToken.
	}
	_ = store.SaveToken("test-server", saved)

	// No inner provider, no refresh config.
	pp := NewPersistentTokenProvider(nil, store, "test-server", nil)

	// Should return the expired token anyway (better than nothing).
	tok, err := pp.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	if tok.AccessToken != "expired-no-refresh" {
		t.Errorf("got %q, want %q", tok.AccessToken, "expired-no-refresh")
	}
}

// Test that refresh keeps the old refresh token if server doesn't rotate it.
func TestPersistentTokenProvider_RefreshWithoutRotation(t *testing.T) {
	var refreshCalled int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			atomic.AddInt32(&refreshCalled, 1)
			// Return new access token but NO refresh_token rotation.
			resp := map[string]interface{}{
				"access_token": "refreshed-access",
				"token_type":   "Bearer",
				"expires_in":   3600,
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
	defer ts.Close()

	store := NewFileTokenStore(t.TempDir())

	inner := &StaticProvider{
		TokenValue: &Token{
			AccessToken:  "expired",
			TokenType:    "Bearer",
			RefreshToken: "original-refresh-token",
			ExpiresAt:    time.Now().Add(-1 * time.Hour),
		},
	}

	pp := NewPersistentTokenProvider(inner, store, "test", &RefreshConfig{
		TokenEndpoint: ts.URL + "/token",
		ClientID:      "client",
	})

	tok, err := pp.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tok.RefreshToken != "original-refresh-token" {
		t.Errorf("RefreshToken = %q, want %q (unchanged)", tok.RefreshToken, "original-refresh-token")
	}
}
